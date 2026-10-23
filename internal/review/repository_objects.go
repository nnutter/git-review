package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/storage/filesystem"
)

func openRepositoryObjects(ctx context.Context, dir string) (*git.Repository, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dir == "" {
		dir = "."
	}
	objects, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return nil, fmt.Errorf("git-review must be used from inside a Git repository")
	}
	return objects, err
}

func singleMergeBase(objects *git.Repository, head, target string) (string, error) {
	headCommit, err := objects.CommitObject(plumbing.NewHash(head))
	if err != nil {
		return "", err
	}
	targetCommit, err := objects.CommitObject(plumbing.NewHash(target))
	if err != nil {
		return "", err
	}
	bases, err := headCommit.MergeBase(targetCommit)
	if err != nil {
		return "", err
	}
	if len(bases) != 1 {
		return "", fmt.Errorf("review requires one merge-base with upstream")
	}
	return bases[0].Hash.String(), nil
}

func repositoryCommonDirectory(objects *git.Repository) (string, error) {
	common := objects.Storer.(*filesystem.Storage).Filesystem().Root()
	data, err := os.ReadFile(filepath.Join(common, "commondir"))
	if errors.Is(err, os.ErrNotExist) {
		return common, nil
	}
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(data))
	if filepath.IsAbs(path) {
		return path, nil
	}
	return filepath.Join(common, path), nil
}

func configuredUpstream(objects *git.Repository, name string) (plumbing.ReferenceName, error) {
	cfg, err := objects.ConfigScoped(config.SystemScope)
	if err != nil {
		return "", err
	}
	branch := cfg.Branches[name]
	if branch == nil {
		return "", nil
	}
	if branch.Remote == "" {
		return "", nil
	}
	if branch.Merge == "" {
		return "", nil
	}
	if branch.Remote == "." {
		return branch.Merge, nil
	}
	remote := cfg.Remotes[branch.Remote]
	if remote == nil {
		return "", nil
	}
	for _, spec := range remote.Fetch {
		if spec.Match(branch.Merge) {
			return spec.Dst(branch.Merge), nil
		}
	}
	return "", nil
}

func symbolicHEAD(ctx context.Context, dir string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if dir == "" {
		dir = "."
	}
	objects, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "", err
	}
	head, err := objects.Reference(plumbing.HEAD, false)
	if err = errors.Join(err, objects.Close()); err != nil {
		return "", err
	}
	if head.Type() != plumbing.SymbolicReference {
		return "", fmt.Errorf("review requires a branch, not detached HEAD")
	}
	return head.Target().String(), nil
}

func workingTreeRoot(ctx context.Context, dir string) (root string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if dir == "" {
		dir = "."
	}
	objects, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return "", fmt.Errorf("git-review must be used from inside a Git repository")
	}
	if err != nil {
		return "", err
	}
	worktree, err := objects.Worktree()
	if err = errors.Join(err, objects.Close()); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(worktree.Filesystem().Root())
}

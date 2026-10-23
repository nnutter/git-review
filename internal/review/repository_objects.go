package review

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

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

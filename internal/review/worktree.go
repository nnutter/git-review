package review

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/storage/filesystem"
)

// WorktreePath shares one persistent review checkout across a repository's branches.
func WorktreePath(repo string) (path string, err error) {
	if repo == "" {
		repo = "."
	}
	objects, err := git.PlainOpenWithOptions(repo, &git.PlainOpenOptions{DetectDotGit: true})
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return "", fmt.Errorf("git-review must be used from inside a Git repository")
	}
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, objects.Close()) }()
	common := objects.Storer.(*filesystem.Storage).Filesystem().Root()
	data, err := os.ReadFile(filepath.Join(common, "commondir"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err == nil {
		path := strings.TrimSpace(string(data))
		if filepath.IsAbs(path) {
			common = path
		} else {
			common = filepath.Join(common, path)
		}
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return "", err
	}
	return filepath.Join(common, "git-review", "worktree"), nil
}

// reviewRunning probes an existing lock without creating it.
func reviewRunning(ctx context.Context, path, branch string) (running bool, err error) {
	file, err := os.OpenFile(path+".git-review.lock", os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		owner, err := io.ReadAll(file)
		if err != nil {
			return false, err
		}
		// Metadata left after process exit is not evidence of a live review.
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); !errors.Is(err, syscall.EWOULDBLOCK) {
			return false, err
		}
		return string(owner) == branch, nil
	}
	return false, err
}

// lockWorktree holds an OS lock until close. Process death releases it.
// Never unlink this file: a new inode could allow concurrent holders.
func lockWorktree(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path+".git-review.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.Join(fmt.Errorf("review worktree is in use; wait for the active review to exit: %w", err), file.Close())
	}
	return file, nil
}

// prepareWorktree discards incidental file edits only in the owned review checkout.
// The caller must hold its worktree lock throughout preparation and review.
func prepareWorktree(ctx context.Context, source Repository, path string, stack Stack) error {
	tracked, err := registeredReviewWorktree(ctx, source, path)
	if err != nil {
		return err
	}
	if !tracked {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("review checkout path already exists or is unreadable: %s", path)
		}
		_, err := source.git(ctx, "worktree", "add", "--detach", path, stack.Tip)
		return err
	}
	if err := validateReviewCheckout(ctx, source, path); err != nil {
		return err
	}
	checkout := Repository{Dir: path}
	if _, err := checkout.git(ctx, "reset", "--hard"); err != nil {
		return err
	}
	if _, err := checkout.git(ctx, "clean", "-fdx"); err != nil {
		return err
	}
	_, err = checkout.git(ctx, "checkout", "--detach", "--no-overwrite-ignore", stack.Tip)
	return err
}

func registeredReviewWorktree(ctx context.Context, source Repository, path string) (bool, error) {
	list, err := source.git(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return false, err
	}
	for record := range strings.SplitSeq(list, "\n\n") {
		lines := strings.Split(record, "\n")
		if lines[0] != "worktree "+path {
			continue
		}
		if !slices.Contains(lines, "detached") {
			return false, fmt.Errorf("review checkout is not detached: %s", path)
		}
		return true, nil
	}
	return false, nil
}

func validateReviewCheckout(ctx context.Context, source Repository, path string) error {
	expected, err := WorktreePath(source.Dir)
	if err != nil {
		return err
	}
	if path != expected {
		return fmt.Errorf("unexpected review checkout path: %s", path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("review checkout must not be a symlink: %s", path)
	}
	checkout := Repository{Dir: path}
	root, err := workingTreeRoot(ctx, path)
	if err != nil {
		return err
	}
	if root != path {
		return fmt.Errorf("review checkout root mismatch: %s", path)
	}
	owned, err := WorktreePath(path)
	if err != nil {
		return err
	}
	if owned != expected {
		return fmt.Errorf("review checkout belongs to another repository: %s", path)
	}
	return validateReviewRegistration(ctx, checkout, path)
}

func validateReviewRegistration(ctx context.Context, checkout Repository, path string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := checkout.Dir
	if dir == "" {
		dir = "."
	}
	objects, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, objects.Close()) }()
	gitDir := objects.Storer.(*filesystem.Storage).Filesystem().Root()
	backlink, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
	if err != nil {
		return fmt.Errorf("invalid review worktree registration: %w", err)
	}
	if filepath.Clean(strings.TrimSpace(string(backlink))) != filepath.Join(path, ".git") {
		return fmt.Errorf("review worktree registration points to another checkout: %s", path)
	}
	return nil
}

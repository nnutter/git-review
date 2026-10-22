package review

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The cache is disposable and independent of review state. Each entry includes
// the fingerprint version, immutable endpoints, and Git's effective diff inputs.
type patchCache struct {
	dir, context string
	writable     bool
}

func patchCachePath(view Snapshot) string {
	return filepath.Join(filepath.Dir(filepath.Dir(view.Path)), "git-review", "cache", "patch-v2")
}

func newPatchCache(ctx context.Context, r Repository, view Snapshot, repo string) *patchCache {
	if len(view.Stacks) == 0 {
		return nil
	}
	// Git resolves attributes from the worktree, index, info/attributes, and
	// global/system sources. Ask Git rather than trying to discover these files.
	root := r
	root.Dir = repo
	var pairs strings.Builder
	for _, stack := range view.Stacks {
		fmt.Fprintf(&pairs, "%s %s\n", stack.Base, stack.Tip)
	}
	paths, err := root.gitInput(ctx, pairs.String(), "diff-tree", "--stdin", "--no-commit-id", "--name-only", "--no-renames", "-r", "-z")
	if err != nil {
		r.logger().Debug("Patch cache unavailable", "error", err)
		return nil
	}
	names := strings.Split(strings.TrimSuffix(paths, "\x00"), "\x00")
	slices.Sort(names)
	names = slices.Compact(names)
	input := ""
	if paths != "" {
		input = strings.Join(names, "\x00") + "\x00"
	}
	attributes, err := root.gitInput(ctx, input, "check-attr", "--all", "--stdin", "-z")
	if err != nil {
		r.logger().Debug("Patch cache unavailable", "error", err)
		return nil
	}
	config, err := root.gitOutput(ctx, "config", "--null", "--list")
	if err != nil {
		r.logger().Debug("Patch cache unavailable", "error", err)
		return nil
	}
	version, err := root.git(ctx, "--version")
	if err != nil {
		r.logger().Debug("Patch cache unavailable", "error", err)
		return nil
	}
	inputs := []string{"patch-v2", version, config, attributes, os.Getenv("GIT_DIFF_OPTS"), os.Getenv("GIT_ATTR_SOURCE")}
	cache := &patchCache{dir: patchCachePath(view), context: fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(inputs, "\x00"))))}
	if err := os.MkdirAll(cache.dir, 0o700); err != nil {
		r.logger().Debug("Patch cache is read-only", "error", err)
	} else {
		cache.writable = true
	}
	return cache
}

func (c *patchCache) hash(ctx context.Context, r Repository, base, tip string) (string, error) {
	if c == nil {
		return r.patchHash(ctx, base, tip)
	}
	key := fmt.Sprintf("%x.patch", sha256.Sum256([]byte(c.context+"\x00"+base+"\x00"+tip)))
	path := filepath.Join(c.dir, key)
	if hash, ok := readPatchCache(path); ok {
		r.logger().Debug("Patch cache hit", "tip", tip)
		return hash, nil
	}
	r.logger().Debug("Patch cache miss", "tip", tip)
	hash, err := r.patchHash(ctx, base, tip)
	if err != nil {
		return "", err
	}
	if c.writable {
		if err := writePatchCache(path, hash); err != nil {
			// Do not report the same unavailable cache once per stack.
			c.writable = false
			r.logger().Debug("Patch cache write failed", "error", err)
		}
	}
	return hash, nil
}

func readPatchCache(path string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 256))
	if err != nil {
		return "", false
	}
	value := strings.TrimSuffix(string(data), "\n")
	if value == "empty" {
		return "", true
	}
	if strings.HasPrefix(value, "v2:") && patchHashPattern.MatchString(value) {
		return value, true
	}
	return "", false
}

func writePatchCache(path, hash string) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".patch-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if hash == "" {
		hash = "empty"
	}
	_, writeErr := io.WriteString(file, hash+"\n")
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}

package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPatchCacheMaintenance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "patch-v2")
	require.NoError(t, os.Mkdir(dir, 0o700))
	old := time.Now().Add(-patchCacheMaxAge - time.Hour)
	put := func(name string, modified time.Time) string {
		t.Helper()
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte("empty\n"), 0o600))
		require.NoError(t, os.Chtimes(path, modified, modified))
		return path
	}
	stale := put(strings.Repeat("a", 64)+".patch", old)
	fresh := put(strings.Repeat("b", 64)+".patch", time.Now())
	staleTemp := put(".patch-abandoned", old)
	freshTemp := put(".patch-active", time.Now())
	unrelated := put("review-state.json", old)
	outside := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(outside, []byte("keep"), 0o600))
	symlink := filepath.Join(dir, strings.Repeat("c", 64)+".patch")
	require.NoError(t, os.Symlink(outside, symlink))

	// Another process can maintain the same shared cache. A busy lock skips
	// this pass rather than blocking the user or pruning concurrently.
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(dir), "maintenance.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lock.Close() })
	require.NoError(t, syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	require.NoError(t, prunePatchCache(t.Context(), dir))
	require.FileExists(t, stale)
	require.NoError(t, syscall.Flock(int(lock.Fd()), syscall.LOCK_UN))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, prunePatchCache(ctx, dir), context.Canceled)
	require.FileExists(t, stale)
	require.NoError(t, prunePatchCache(t.Context(), dir))
	require.NoFileExists(t, stale)
	require.NoFileExists(t, staleTemp)
	for _, path := range []string{fresh, freshTemp, unrelated, symlink, outside} {
		require.FileExists(t, path)
	}
}

func TestPatchCacheMaintenanceBoundsSize(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "patch-v2")
	require.NoError(t, os.Mkdir(dir, 0o700))
	paths := []string{}
	for i, prefix := range []string{"a", "b"} {
		path := filepath.Join(dir, strings.Repeat(prefix, 64)+".patch")
		file, err := os.Create(path)
		require.NoError(t, err)
		require.NoError(t, file.Truncate(patchCacheMaxBytes/2+1))
		require.NoError(t, file.Close())
		modified := time.Now().Add(time.Duration(i-2) * time.Hour)
		require.NoError(t, os.Chtimes(path, modified, modified))
		paths = append(paths, path)
	}
	require.NoError(t, prunePatchCache(t.Context(), dir))
	require.NoFileExists(t, paths[0], "evict oldest entries first")
	require.FileExists(t, paths[1])
}

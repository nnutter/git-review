package review

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/log/v2"
	"github.com/stretchr/testify/require"
)

func TestSnapshotPatchCache(t *testing.T) {
	r := testRepo(t)
	var logs bytes.Buffer
	r.Logger = log.NewWithOptions(&logs, log.Options{Level: log.DebugLevel})
	require.NoError(t, os.WriteFile(filepath.Join(r.Dir, "file"), []byte("one\ntwo\n"), 0o600))
	gitTest(t, r.Dir, "add", "file")
	gitTest(t, r.Dir, "commit", "-m", "Feature")
	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Empty")
	cold, err := r.Snapshot(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, cold.Stacks[0].PatchHash)
	require.Empty(t, cold.Stacks[1].PatchHash)
	logs.Reset()
	warm, err := r.Snapshot(t.Context())
	require.NoError(t, err)
	require.Equal(t, cold, warm)
	require.Equal(t, 2, strings.Count(logs.String(), "Patch cache hit"))
	require.NotContains(t, logs.String(), "Patch cache miss")

	// Appending ordinary content reuses existing fingerprints, including empty
	// diffs. A cache keyed solely by branch HEAD would miss all three stacks.
	require.NoError(t, os.WriteFile(filepath.Join(r.Dir, ".gitattributes"), []byte("another -diff\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(r.Dir, "another"), []byte("new\n"), 0o600))
	gitTest(t, r.Dir, "add", "another")
	gitTest(t, r.Dir, "commit", "-m", "Another feature")
	logs.Reset()
	appended, err := r.Snapshot(t.Context())
	require.NoError(t, err)
	require.Len(t, appended.Stacks, 3)
	require.Equal(t, cold.Stacks, appended.Stacks[:2])
	require.Equal(t, 2, strings.Count(logs.String(), "Patch cache hit"))
	require.Equal(t, 1, strings.Count(logs.String(), "Patch cache miss"))
}

func TestSnapshotPatchCacheTracksDiffInputs(t *testing.T) {
	for _, source := range []string{"worktree", "info", "global", "driver-config"} {
		t.Run(source, func(t *testing.T) {
			r := testRepo(t)
			require.NoError(t, os.WriteFile(filepath.Join(r.Dir, "file"), []byte("one\ntwo\n"), 0o600))
			gitTest(t, r.Dir, "add", "file")
			gitTest(t, r.Dir, "commit", "-m", "Feature")
			if source == "driver-config" {
				require.NoError(t, os.WriteFile(filepath.Join(r.Dir, ".gitattributes"), []byte("file diff=custom\n"), 0o600))
			}
			before, err := r.Snapshot(t.Context())
			require.NoError(t, err)
			switch source {
			case "worktree":
				require.NoError(t, os.WriteFile(filepath.Join(r.Dir, ".gitattributes"), []byte("file -diff\n"), 0o600))
			case "info":
				require.NoError(t, os.WriteFile(filepath.Join(r.Dir, ".git", "info", "attributes"), []byte("file -diff\n"), 0o600))
			case "global":
				path := filepath.Join(t.TempDir(), "attributes")
				require.NoError(t, os.WriteFile(path, []byte("file -diff\n"), 0o600))
				gitTest(t, r.Dir, "config", "core.attributesFile", path)
			case "driver-config":
				gitTest(t, r.Dir, "config", "diff.custom.binary", "true")
			}
			after, err := r.Snapshot(t.Context())
			require.NoError(t, err)
			require.Equal(t, before.Head, after.Head)
			require.NotEqual(t, before.Stacks[0].PatchHash, after.Stacks[0].PatchHash)
			uncached, err := r.patchHash(t.Context(), after.Base, after.Head)
			require.NoError(t, err)
			require.Equal(t, uncached, after.Stacks[0].PatchHash)
		})
	}
}

func TestSnapshotPatchCacheFailureIsDisposable(t *testing.T) {
	for _, failure := range []string{"corrupt", "unavailable"} {
		t.Run(failure, func(t *testing.T) {
			r := testRepo(t)
			gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Feature")
			before, err := r.Snapshot(t.Context())
			require.NoError(t, err)
			dir := patchCachePath(before)
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			if failure == "corrupt" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, entries[0].Name()), []byte("truncated"), 0o600))
			} else {
				require.NoError(t, os.RemoveAll(dir))
				require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o600))
			}
			after, err := r.Snapshot(t.Context())
			require.NoError(t, err)
			require.Equal(t, before, after)
			_, err = os.Stat(before.Path)
			require.ErrorIs(t, err, os.ErrNotExist, "cache writes must not create review history")
		})
	}
}

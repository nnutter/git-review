package review

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Snapshot owns the persisted fingerprint contract. These cases guard against
// omitting binary data, whitespace, paths, or modes from content identity.
func TestSnapshotPatchIdentity(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after []byte
		mode          bool
		rename        bool
	}{
		{name: "text", before: []byte("one\n"), after: []byte("two\n")},
		{name: "whitespace", before: []byte("one\n"), after: []byte("one \n")},
		{name: "binary", before: []byte{0, 1, 2}, after: []byte{0, 1, 3}},
		{name: "mode", before: []byte("one\n"), mode: true},
		{name: "path", before: []byte("one\n"), rename: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRepo(t)
			path := filepath.Join(r.Dir, "file")
			require.NoError(t, os.WriteFile(path, tc.before, 0o600))
			gitTest(t, r.Dir, "add", ".")
			gitTest(t, r.Dir, "commit", "-m", "Feature")
			original, err := r.Snapshot(t.Context())
			require.NoError(t, err)
			require.NotEmpty(t, original.Stacks[0].PatchHash)

			// Commit metadata and presentation preferences do not define the patch.
			gitTest(t, r.Dir, "commit", "--amend", "-m", "Reworded")
			for key, value := range map[string]string{
				"color.ui": "always", "diff.noprefix": "true", "diff.context": "0",
				"diff.algorithm": "histogram", "diff.renames": "true",
				"diff.mnemonicPrefix": "true", "diff.interHunkContext": "10",
				"diff.relative": "true", "diff.suppressBlankEmpty": "true",
			} {
				gitTest(t, r.Dir, "config", key, value)
			}
			subdir := filepath.Join(r.Dir, "subdir")
			require.NoError(t, os.Mkdir(subdir, 0o700))
			rewritten, err := (Repository{Dir: subdir}).Snapshot(t.Context())
			require.NoError(t, err)
			require.NotEqual(t, original.Stacks[0].Key, rewritten.Stacks[0].Key)
			require.Equal(t, original.Stacks[0].PatchHash, rewritten.Stacks[0].PatchHash)

			switch {
			case tc.mode:
				gitTest(t, r.Dir, "update-index", "--chmod=+x", "file")
			case tc.rename:
				gitTest(t, r.Dir, "mv", "file", "renamed")
			default:
				require.NoError(t, os.WriteFile(path, tc.after, 0o600))
				gitTest(t, r.Dir, "add", ".")
			}
			gitTest(t, r.Dir, "commit", "-m", "fixup! Reworded")
			changed, err := r.Snapshot(t.Context())
			require.NoError(t, err)
			require.Len(t, changed.Stacks, 1)
			require.NotEqual(t, original.Stacks[0].PatchHash, changed.Stacks[0].PatchHash)
		})
	}
}

func TestEmptyStacksHaveNoPatchIdentity(t *testing.T) {
	r := testRepo(t)
	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Empty")
	view, err := r.Snapshot(t.Context())
	require.NoError(t, err)
	require.Empty(t, view.Stacks[0].PatchHash)
}

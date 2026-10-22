package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCacheWarmTargetsCurrentStack(t *testing.T) {
	for _, selection := range []string{"default", "original", "fixup"} {
		t.Run(selection, func(t *testing.T) {
			dir := featureRepo(t)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "file"), []byte("feature\n"), 0o600))
			git(t, dir, "add", "file")
			git(t, dir, "commit", "-m", "Content")
			original := git(t, dir, "rev-parse", "HEAD")
			require.NoError(t, os.WriteFile(filepath.Join(dir, "file"), []byte("feature\nfeedback\n"), 0o600))
			git(t, dir, "add", "file")
			git(t, dir, "commit", "--fixup", original)
			fixup := git(t, dir, "rev-parse", "HEAD")
			// The other stack has different attributes. Full status must still
			// reuse the selected stack's entry, including from a subdirectory.
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("file -diff\n"), 0o600))
			subdir := filepath.Join(dir, "subdir")
			require.NoError(t, os.Mkdir(subdir, 0o700))
			args := []string{"cache", "warm", "--debug"}
			switch selection {
			case "original":
				args = append(args, original)
			case "fixup":
				args = append(args, fixup)
			}
			out, terminal, err := execute(t, subdir, args...)
			require.NoError(t, err)
			require.Empty(t, out)
			require.Equal(t, 1, strings.Count(terminal, "Patch cache miss"))
			require.NotContains(t, terminal, "Patch cache hit")
			state, err := filepath.Glob(filepath.Join(dir, ".git", "tuicr-reviews", "*.json"))
			require.NoError(t, err)
			require.Empty(t, state)

			_, terminal, err = execute(t, dir, "status", "--debug")
			require.NoError(t, err)
			require.Equal(t, 1, strings.Count(terminal, "Patch cache hit"))
			require.Equal(t, 1, strings.Count(terminal, "Patch cache miss"))
		})
	}
}

func TestCacheWarmErrorsAndJSON(t *testing.T) {
	dir := featureRepo(t)
	out, _, err := execute(t, dir, "cache", "warm", "--json")
	require.NoError(t, err)
	require.JSONEq(t, `{"version":1,"command":"warm","status":"ok"}`, out)
	for _, args := range [][]string{
		{"cache", "warm", "main", "--json"},
		{"cache", "warm", "missing-ref", "--json"},
		{"cache", "warm", "HEAD", "HEAD", "--json"},
	} {
		out, _, err := execute(t, dir, args...)
		require.Error(t, err)
		require.Contains(t, out, `"status":"error"`)
	}
	_, _, err = execute(t, t.TempDir(), "cache", "warm")
	require.ErrorContains(t, err, "inside a Git repository")
	git(t, dir, "branch", "--unset-upstream")
	_, _, err = execute(t, dir, "cache", "warm")
	require.ErrorContains(t, err, "requires a configured Git upstream")
}

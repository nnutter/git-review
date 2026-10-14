package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLogShowsUpstreamCommitsAndOptionalPatches(t *testing.T) {
	dir := featureRepo(t)
	path := filepath.Join(dir, "example.txt")
	require.NoError(t, os.WriteFile(path, []byte("value = 1\n"), 0o600))
	git(t, dir, "add", "example.txt")
	git(t, dir, "commit", "-m", "Add example")
	require.NoError(t, os.WriteFile(path, []byte("value   =   1\n"), 0o600))
	git(t, dir, "commit", "-am", "Change whitespace")

	for _, tc := range []struct {
		name    string
		flags   []string
		patches int
		stat    bool
	}{
		{name: "default"},
		{name: "stat", flags: []string{"--stat"}, stat: true},
		{name: "stat with patch", flags: []string{"--stat", "-p"}, patches: 2, stat: true},
		{name: "patch", flags: []string{"-p"}, patches: 2},
		{name: "whitespace without patch", flags: []string{"-w"}},
		{name: "patch ignoring whitespace", flags: []string{"-p", "-w"}, patches: 1},
		{name: "combined short flags", flags: []string{"-pw"}, patches: 1},
		{name: "long flags", flags: []string{"--patch", "--ignore-all-space"}, patches: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, terminal, err := execute(t, dir, append([]string{"log"}, tc.flags...)...)
			require.NoError(t, err)
			require.Empty(t, terminal)
			require.NotContains(t, out, "    Base\n")
			first := strings.Index(out, "    Add feature\n")
			second := strings.Index(out, "    Add example\n")
			third := strings.Index(out, "    Change whitespace\n")
			require.GreaterOrEqual(t, first, 0)
			require.Greater(t, second, first)
			require.Greater(t, third, second)
			require.Equal(t, tc.patches, strings.Count(out, "diff --git"))
			if tc.stat {
				require.Contains(t, out, "example.txt |")
				require.Contains(t, out, "1 file changed, 1 insertion(+)")
			} else {
				require.NotContains(t, out, "example.txt |")
			}
			if tc.patches > 0 {
				require.Contains(t, out, "+value = 1\n")
			}
			if tc.patches == 2 {
				require.Contains(t, out, "+value   =   1\n")
			} else {
				require.NotContains(t, out, "+value   =   1\n")
			}
		})
	}

	git(t, dir, "reset", "--hard", "@{u}")
	out, terminal, err := execute(t, dir, "log")
	require.NoError(t, err)
	require.Empty(t, out)
	require.Empty(t, terminal)
}

func TestLogReportsGitAndUsageErrors(t *testing.T) {
	dir := featureRepo(t)
	git(t, dir, "branch", "--unset-upstream")
	out, terminal, err := execute(t, dir, "log")
	require.Error(t, err)
	require.Empty(t, out)
	require.Contains(t, terminal, "no upstream configured")

	out, _, err = execute(t, t.TempDir(), "log")
	require.Error(t, err)
	require.Empty(t, out)

	for _, tc := range []struct {
		args    []string
		message string
	}{
		{args: []string{"log", "HEAD"}, message: "unknown command"},
		{args: []string{"log", "--unknown"}, message: "unknown flag"},
		{args: []string{"log", "--json"}, message: "log does not support --json"},
	} {
		_, _, err := execute(t, dir, tc.args...)
		require.ErrorContains(t, err, tc.message)
	}
}

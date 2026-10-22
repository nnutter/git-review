package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommitMsgMatchesGerritHook(t *testing.T) {
	binary := buildGitReview(t)
	reference, err := filepath.Abs("../../contrib/commit-msg")
	require.NoError(t, err)
	t.Setenv("GIT_COMMITTER_DATE", "2001-02-03T04:05:06 +0000")
	id := "I" + strings.Repeat("a", 40)
	for _, tc := range []struct {
		name    string
		message string
		config  []string
		head    bool
	}{
		{name: "initial commit", message: "Add feature\n"},
		{name: "existing head", message: "Add feature\n\nDetails.\n", head: true},
		{name: "signed off", message: "Add feature\n\nSigned-off-by: Test <test@example.com>\n"},
		{name: "existing id", message: "Add feature\n\nChange-Id: " + id + "\n"},
		{name: "id in body", message: "Add feature\n\nChange-Id: not a trailer\n\nMore text.\n"},
		{name: "comments", message: "Add feature\n\n# comment\n"},
		{name: "fixup", message: "fixup! Add feature\n"},
		{name: "squash", message: "squash! Add feature\n"},
		{name: "disabled", message: "Add feature\n", config: []string{"gerrit.createChangeId", "false"}},
		{name: "disabled empty", message: "", config: []string{"gerrit.createChangeId", "false"}},
		{name: "enabled", message: "Add feature\n", config: []string{"gerrit.createChangeId", "true"}},
		{name: "review url", message: "Add feature\n", config: []string{"gerrit.reviewUrl", "https://review.example/"}},
		{name: "existing link", message: "Add feature\n\nLink: https://review.example/id/" + id + "\n\n# comment\n", config: []string{"gerrit.reviewUrl", "https://review.example"}},
		{name: "unrelated link", message: "Add feature\n\nLink: https://example.com\n", config: []string{"gerrit.reviewUrl", "https://review.example"}},
		{name: "empty", message: ""},
		{name: "comments only", message: "# comment\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			git(t, dir, "init", "-b", "main")
			git(t, dir, "config", "user.name", "Test")
			git(t, dir, "config", "user.email", "test@example.com")
			if tc.head {
				git(t, dir, "commit", "--allow-empty", "-m", "Base")
			}
			if tc.config != nil {
				git(t, dir, append([]string{"config"}, tc.config...)...)
			}
			for _, name := range []string{"reference message", "actual message"} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(tc.message), 0o600))
			}
			cmd := exec.CommandContext(t.Context(), "sh", reference, "reference message")
			cmd.Dir = dir
			refOut, refErr := cmd.CombinedOutput()
			out, stderr, actualErr := executeBinary(t, binary, dir, "commit-msg", "actual message")
			require.Equal(t, refErr == nil, actualErr == nil, "reference: %s; actual: %s", refOut, stderr)
			require.Empty(t, out)
			expected, err := os.ReadFile(filepath.Join(dir, "reference message"))
			require.NoError(t, err)
			actual, err := os.ReadFile(filepath.Join(dir, "actual message"))
			require.NoError(t, err)
			require.Equal(t, string(expected), string(actual))
			if actualErr == nil {
				_, stderr, err = executeBinary(t, binary, dir, "commit-msg", "actual message")
				require.NoError(t, err, "%s", stderr)
				again, err := os.ReadFile(filepath.Join(dir, "actual message"))
				require.NoError(t, err)
				require.Equal(t, string(actual), string(again), "hook must be idempotent")
			}
		})
	}
}

func TestCommitMsgRejectsInvalidInputs(t *testing.T) {
	for _, args := range [][]string{
		{"commit-msg"},
		{"commit-msg", "one", "two"},
		{"commit-msg", "missing"},
		{"commit-msg", "."},
	} {
		_, _, err := execute(t, t.TempDir(), args...)
		require.Error(t, err)
	}
}

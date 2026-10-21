package cli

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nnutter/git-review/internal/review"
)

func TestRealTuicrFileScopes(t *testing.T) {
	env := realTuicrEnvironment(t)
	binary := buildGitReview(t)
	for _, tc := range []struct {
		name       string
		args       []string
		cwd        string
		previous   bool
		noUpstream bool
	}{
		{name: "nested file", args: []string{"src/nested.txt"}},
		{name: "annotation without upstream", args: []string{"src/nested.txt"}, noUpstream: true},
		{name: "nested file after root review", args: []string{"src/nested.txt"}, previous: true},
		{name: "nested directory", args: []string{"src"}, previous: true},
		{name: "untracked from subdirectory", args: []string{"--untracked"}, cwd: "src", previous: true},
		{name: "tracked from subdirectory", args: []string{"--all"}, cwd: "src", previous: true},
		{name: "symlink to nested file", args: []string{"linked.txt"}, previous: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := realFeatureRepo(t)
			require.NoError(t, os.Mkdir(filepath.Join(dir, "src"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "nested.txt"), []byte("Nested file to review\n"), 0o600))
			require.NoError(t, os.Symlink(filepath.Join("src", "nested.txt"), filepath.Join(dir, "linked.txt")))
			git(t, dir, "add", "src/nested.txt")
			git(t, dir, "commit", "--amend", "--no-edit")
			if tc.previous {
				reviewRealFile(t, binary, dir, env, "feature.txt", "Prior root feedback", "feature.txt")
			}
			if tc.noUpstream {
				git(t, dir, "branch", "--unset-upstream")
			}
			result := reviewRealFile(t, binary, filepath.Join(dir, tc.cwd), env, "nested.txt", "Nested feedback", tc.args...)
			require.Equal(t, dir, result.Repo)
			require.Len(t, result.Review.Comments, 1)
			require.Equal(t, "Nested feedback", result.Review.Comments[0]["content"])
			require.Equal(t, "src/nested.txt", result.Review.Comments[0]["path"])
			checkRealFileFeedback(t, binary, filepath.Join(dir, tc.cwd), env, result)
		})
	}
}

func TestRealTuicrBranchFiles(t *testing.T) {
	env := realTuicrEnvironment(t)
	binary := buildGitReview(t)
	for _, tc := range []struct {
		name  string
		cwd   string
		merge bool
	}{
		{name: "root"},
		{name: "subdirectory", cwd: "src"},
		{name: "merged upstream", merge: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := realFeatureRepo(t)
			require.NoError(t, os.Mkdir(filepath.Join(dir, "src"), 0o700))
			// The upstream advances independently. Its new file must not be
			// included in the branch diff, nor should an untracked file.
			git(t, dir, "checkout", "main")
			require.NoError(t, os.WriteFile(filepath.Join(dir, "upstream.txt"), []byte("Upstream only\n"), 0o600))
			git(t, dir, "add", "upstream.txt")
			git(t, dir, "commit", "-m", "Advance upstream")
			git(t, dir, "checkout", "feature")
			if tc.merge {
				git(t, dir, "merge", "--no-edit", "main")
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("Not committed\n"), 0o600))
			reviewRealFile(t, binary, dir, env, "feature.txt", "Prior annotation", "--all")
			result := reviewRealFile(t, binary, filepath.Join(dir, tc.cwd), env, "feature.txt", "Branch feedback")
			require.Equal(t, dir, result.Repo)
			require.False(t, result.AllFiles)
			raw, err := os.ReadFile(result.Session)
			require.NoError(t, err)
			var saved struct {
				Files map[string]any `json:"files"`
			}
			require.NoError(t, json.Unmarshal(raw, &saved))
			require.Contains(t, saved.Files, "feature.txt")
			require.NotContains(t, saved.Files, "upstream.txt")
			require.NotContains(t, saved.Files, "untracked.txt")
			require.Len(t, result.Review.Comments, 1)
			require.Equal(t, "Branch feedback", result.Review.Comments[0]["content"])
			require.Equal(t, "feature.txt", result.Review.Comments[0]["path"])
			git(t, dir, "commit", "--allow-empty", "-m", "Advance branch after file review")
			checkRealFileFeedback(t, binary, filepath.Join(dir, tc.cwd), env, result)
		})
	}
}

func checkRealFileFeedback(t *testing.T, binary, dir string, env []string, reviewed review.FilesResult) {
	t.Helper()
	before, err := os.ReadFile(reviewed.Session)
	require.NoError(t, err)
	out := runRealCommand(t, binary, dir, env, "feedback", "--json")
	var result struct {
		Status       string                  `json:"status"`
		Feedback     []review.FeedbackRecord `json:"feedback"`
		FileFeedback []review.FilesResult    `json:"fileFeedback"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.Equal(t, "pending", result.Status)
	require.Empty(t, result.Feedback, "file feedback must not become a stack attempt")
	var found bool
	for _, file := range result.FileFeedback {
		if file.Session == reviewed.Session {
			found = true
			require.Equal(t, "files", file.Command)
			require.Equal(t, reviewed.Review, file.Review)
		}
	}
	require.True(t, found, "feedback must include the saved file review")
	terminal := runRealCommand(t, binary, dir, env, "feedback")
	require.Contains(t, terminal, "File feedback:")
	require.Contains(t, terminal, reviewed.Review.Comments[0]["content"])
	after, err := os.ReadFile(reviewed.Session)
	require.NoError(t, err)
	require.Equal(t, before, after, "feedback must not change the tuicr session")
}

func reviewRealFile(t *testing.T, binary, dir string, env []string, file, feedback string, args ...string) review.FilesResult {
	t.Helper()
	command := append([]string{"files", "--json"}, args...)
	session := startTerminal(t, binary, dir, env, command...)
	session.waitFor(t, file)
	list := runRealCommand(t, "tuicr", dir, env, "review", "list", "--all")
	var sessions []struct {
		Path   string `json:"path"`
		Active bool   `json:"active"`
	}
	require.NoError(t, json.Unmarshal([]byte(list), &sessions))
	var active []string
	for _, saved := range sessions {
		if saved.Active {
			active = append(active, saved.Path)
		}
	}
	require.Len(t, active, 1)
	runRealCommand(t, "tuicr", dir, env, "review", "add", "--session", active[0], "--target-file", file, "--line", "1", feedback)
	session.send(t, ":wq\r")
	var result review.FilesResult
	require.NoError(t, json.Unmarshal([]byte(session.waitExit(t)), &result))
	require.Equal(t, "feedback", result.Status)
	require.Equal(t, active[0], result.Session)
	require.NotNil(t, result.Review)
	return result
}

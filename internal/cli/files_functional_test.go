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
		name     string
		args     []string
		cwd      string
		previous bool
	}{
		{name: "nested file", args: []string{"src/nested.txt"}},
		{name: "nested file after root review", args: []string{"src/nested.txt"}, previous: true},
		{name: "nested directory", args: []string{"src"}, previous: true},
		{name: "untracked from subdirectory", args: []string{"--untracked"}, cwd: "src", previous: true},
		{name: "tracked from subdirectory", cwd: "src", previous: true},
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
			result := reviewRealFile(t, binary, filepath.Join(dir, tc.cwd), env, "nested.txt", "Nested feedback", tc.args...)
			require.Equal(t, dir, result.Repo)
			require.Len(t, result.Review.Comments, 1)
			require.Equal(t, "Nested feedback", result.Review.Comments[0]["content"])
			require.Equal(t, "src/nested.txt", result.Review.Comments[0]["path"])
		})
	}
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

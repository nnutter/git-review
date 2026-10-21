package cli

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nnutter/git-review/internal/review"
)

func TestFilesCommandArgMapping(t *testing.T) {
	dir := featureRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("File to review\n"), 0o600))
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "launch")
	t.Setenv("TEST_LAUNCH_LOG", log)
	require.NoError(t, os.WriteFile(filepath.Join(bin, "tuicr"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TEST_LAUNCH_LOG\"\n"), 0o700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tc := range []struct {
		name     string
		args     []string
		wantArgs string
	}{
		{"all files", []string{"files", "--json"}, "tui\n--no-update-check\n--stdout\n--all-files"},
		{"explicit all files", []string{"files", "--all", "--json"}, "tui\n--no-update-check\n--stdout\n--all-files"},
		{"single file", []string{"files", "file.txt", "--json"}, "tui\n--no-update-check\n--stdout\n--file\nfile.txt"},
		{"untracked defaults to dot", []string{"files", "--untracked", "--json"}, "tui\n--no-update-check\n--stdout\n--file\n."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(log)
			out, terminal, err := execute(t, dir, tc.args...)
			require.NoError(t, err)
			require.Empty(t, terminal)
			var result review.FilesResult
			require.NoError(t, json.Unmarshal([]byte(out), &result))
			require.Equal(t, 1, result.Version)
			require.Equal(t, "files", result.Command)
			require.Equal(t, "cancelled", result.Status)
			launch, err := os.ReadFile(log)
			require.NoError(t, err)
			require.Equal(t, tc.wantArgs+"\n", string(launch))
		})
	}
}

func TestFilesCommandDistinguishesCommits(t *testing.T) {
	dir := featureRepo(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "tuicr"), []byte("#!/bin/sh\nexit 0\n"), 0o700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	commitsOut, _, err := execute(t, dir, "commits", "--json")
	require.NoError(t, err)
	var commits review.Result
	require.NoError(t, json.Unmarshal([]byte(commitsOut), &commits))
	require.Equal(t, "commits", commits.Command)
	filesOut, _, err := execute(t, dir, "files", "--json")
	require.NoError(t, err)
	var files review.FilesResult
	require.NoError(t, json.Unmarshal([]byte(filesOut), &files))
	require.Equal(t, "files", files.Command)
	require.NotEqual(t, commits.Command, files.Command)
}

func TestFilesCommandTerminalAndErrors(t *testing.T) {
	dir := featureRepo(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "tuicr"), []byte("#!/bin/sh\nexit 0\n"), 0o700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, terminal, err := execute(t, dir, "files")
	require.NoError(t, err)
	require.Empty(t, terminal)
	require.Contains(t, out, "Files review:")
	require.Contains(t, out, "Review cancelled.")
	require.Contains(t, out, "No file feedback was saved.")
	for _, args := range [][]string{
		{"files", "a", "b", "--json"},
		{"files", "--untracked", "file.txt", "--json"},
		{"files", "--unknown", "--json"},
		{"files", "--all", "--untracked", "--json"},
		{"files", "--all", "file.txt", "--json"},
	} {
		out, terminal, err := execute(t, dir, args...)
		require.Error(t, err)
		require.Empty(t, terminal)
		require.Contains(t, out, `"status":"error"`)
		require.Contains(t, out, `"command":"files"`)
	}
}

func TestFilesKeepsTuicrOutputOffJSON(t *testing.T) {
	dir := featureRepo(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "tuicr"), []byte("#!/bin/sh\nprintf 'tuicr terminal output\\n' >&2\nprintf 'exported feedback\\n'\n"), 0o700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, terminal, err := execute(t, dir, "files", "--json")
	require.NoError(t, err)
	require.Equal(t, "tuicr terminal output\n", terminal)
	var result review.FilesResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.Equal(t, "cancelled", result.Status)
	require.NotContains(t, out, "tuicr terminal output")
	require.NotContains(t, out, "exported feedback")
}

func TestFilesHoldsWorktreeLock(t *testing.T) {
	dir := featureRepo(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	bin := t.TempDir()
	started := filepath.Join(bin, "started")
	require.NoError(t, os.WriteFile(filepath.Join(bin, "tuicr"), []byte("#!/bin/sh\nprintf started > \""+started+"\"\nsleep 3\n"), 0o700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Launch files in background via binary to check lock held during TUI.
	binary := buildGitReview(t)
	t.Setenv("PATH", filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	done := make(chan struct{})
	go func() {
		defer close(done)
		out, _, _ := executeBinary(t, binary, dir, "files", "--json")
		if strings.Contains(out, `"status":"cancelled"`) {
			return
		}
	}()
	require.Eventually(t, func() bool {
		_, err := os.Stat(started)
		return err == nil
	}, 5_000_000_000, 10_000_000)
	// While files holds the repository-wide lock, status --is-reviewing
	// reports true, just as it does during a commits review.
	out, _, err := executeBinary(t, binary, dir, "status", "--is-reviewing", "--json")
	require.NoError(t, err)
	require.Contains(t, out, `"is_reviewing":true`)
	<-done
}

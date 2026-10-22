package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
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
		oneline bool
	}{
		{name: "default"},
		{name: "oneline", flags: []string{"--oneline"}, oneline: true},
		{name: "oneline with patch", flags: []string{"--oneline", "-p"}, patches: 2, oneline: true},
		{name: "oneline with stat", flags: []string{"--oneline", "--stat"}, stat: true, oneline: true},
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
			require.NotContains(t, out, " Base\n")
			if tc.oneline {
				require.Regexp(t, `(?m)^[0-9a-f]+ Add feature$`, out)
				require.Regexp(t, `(?m)^[0-9a-f]+ Add example$`, out)
				require.Regexp(t, `(?m)^[0-9a-f]+ Change whitespace$`, out)
				require.NotContains(t, out, "Change-Id:")
				if len(tc.flags) == 1 {
					require.Equal(t, 3, strings.Count(out, "\n"))
				}
			}
			first := strings.Index(out, " Add feature\n")
			second := strings.Index(out, " Add example\n")
			third := strings.Index(out, " Change whitespace\n")
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

func TestLogDifft(t *testing.T) {
	dir := featureRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "example.txt"), []byte("example content\n"), 0o600))
	git(t, dir, "add", "example.txt")
	git(t, dir, "commit", "-m", "Add example")
	git(t, dir, "config", "diff.external", "false")
	tools := t.TempDir()
	difft := filepath.Join(tools, "difft")
	require.NoError(t, os.WriteFile(difft, []byte("#!/bin/sh\nprintf 'External diff: %s\\n' \"$1\"\ncat \"$5\"\n"), 0o700))
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, terminal, err := execute(t, dir, "log", "-p")
	require.NoError(t, err)
	require.Empty(t, terminal)
	require.Contains(t, out, "+example content\n")
	require.NotContains(t, out, "External diff:")

	out, terminal, err = execute(t, dir, "log", "--difft")
	require.NoError(t, err)
	require.Empty(t, terminal)
	require.Contains(t, out, "External diff: example.txt\nexample content\n")
	require.NotContains(t, out, "diff --git")
	require.Equal(t, "false", git(t, dir, "config", "diff.external"))

	require.NoError(t, os.WriteFile(difft, []byte("#!/bin/sh\necho 'difft failed' >&2\nexit 1\n"), 0o700))
	_, terminal, err = execute(t, dir, "log", "--difft")
	require.ErrorContains(t, err, "git log:")
	require.Contains(t, terminal, "difft failed")
}

func TestLogHunkPager(t *testing.T) {
	binary := buildGitReview(t)
	dir := featureRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "example.txt"), []byte("example content\n"), 0o600))
	git(t, dir, "add", "example.txt")
	git(t, dir, "commit", "-m", "Add example")
	git(t, dir, "config", "core.pager", "false")
	git(t, dir, "config", "color.ui", "false")
	tools := t.TempDir()
	capture := filepath.Join(tools, "pager-output")
	require.NoError(t, os.WriteFile(filepath.Join(tools, "hunk"), []byte("#!/bin/sh\n[ \"$1\" = pager ] || exit 1\ncat > \"$HUNK_OUTPUT\"\n"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(tools, "difft"), []byte("#!/bin/sh\nprintf 'External diff: %s\\n' \"$1\"\ncat \"$5\"\n"), 0o700))
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HUNK_OUTPUT", capture)
	t.Setenv("TERM", "xterm")

	out, terminal, err := executeBinary(t, binary, dir, "log", "--hunk")
	require.NoError(t, err)
	require.Empty(t, terminal)
	require.Contains(t, out, "+example content\n")
	_, err = os.Stat(capture)
	require.ErrorIs(t, err, os.ErrNotExist)

	for _, tc := range []struct {
		name  string
		flags []string
		want  string
	}{
		{name: "hunk", flags: []string{"--hunk"}, want: "+example content\n"},
		{name: "hunk and difft", flags: []string{"--hunk", "--difft"}, want: "External diff: example.txt\nexample content\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master, slave, err := pty.Open()
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, slave.Close())
				require.NoError(t, master.Close())
			})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, append([]string{"log"}, tc.flags...)...)
			cmd.Dir = dir
			cmd.Stdout = slave
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			// The fake pager writes to capture, so the terminal needs no reader.
			require.NoError(t, cmd.Run(), "%s", stderr.String())
			saved, err := os.ReadFile(capture)
			require.NoError(t, err)
			require.Contains(t, string(saved), tc.want)
			require.Contains(t, string(saved), "Add example")
			require.NoError(t, os.Remove(capture))
		})
	}
	require.Equal(t, "false", git(t, dir, "config", "core.pager"))
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

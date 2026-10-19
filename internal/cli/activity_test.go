package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nnutter/git-review/internal/review"
)

func TestReviewRunningReportsLiveLockNotPersistedAttempt(t *testing.T) {
	binary := buildGitReview(t)
	dir := featureRepo(t)
	path, err := review.WorktreePath(dir)
	require.NoError(t, err)
	check := func(cwd string, running bool) {
		t.Helper()
		out, terminal, err := executeBinary(t, binary, cwd, "status", "--is-reviewing", "--json")
		require.NoError(t, err)
		require.Empty(t, terminal)
		want := `{"version":1,"command":"status","is_reviewing":false}`
		if running {
			want = `{"version":1,"command":"status","is_reviewing":true}`
		}
		require.JSONEq(t, want, out)
	}
	check(dir, false)
	_, err = os.Stat(filepath.Dir(path))
	require.ErrorIs(t, err, os.ErrNotExist)
	view, err := (review.Repository{Dir: dir}).Snapshot(t.Context())
	require.NoError(t, err)
	require.NoError(t, (review.Store{Path: view.Path}).Update(func(state *review.State) error {
		state.Attempts = append(state.Attempts, review.Attempt{ID: "interrupted", Stack: view.Stacks[0], Status: "reviewing", StartedAt: "2026-01-01T00:00:00Z"})
		return nil
	}))
	check(dir, false)
	other := filepath.Join(t.TempDir(), "checkout")
	git(t, dir, "worktree", "add", "-b", "other", other, "HEAD")
	pause := func(cwd string) {
		t.Helper()
		cmd := exec.Command("git", "rebase", "--force-rebase", "--exec", "false", "HEAD~1")
		cmd.Dir = cwd
		output, err := cmd.CombinedOutput()
		require.Error(t, err, string(output))
		require.Empty(t, git(t, cwd, "branch", "--show-current"))
	}
	called := false
	reviewer := review.Reviewer{
		Service: review.Service{Repository: review.Repository{Dir: dir}},
		Choose: func(context.Context, *review.Decision) (string, error) {
			called = true
			check(dir, true)
			check(other, false)
			// A paused replay must still identify its branch, including in a
			// linked worktree, without treating another branch's lock as its own.
			pause(dir)
			pause(other)
			check(dir, true)
			check(other, false)
			git(t, dir, "rebase", "--abort")
			git(t, other, "rebase", "--abort")
			return "cancel", nil
		},
	}
	_, err = reviewer.Review(t.Context(), review.ReviewOptions{})
	require.NoError(t, err)
	require.True(t, called)
	pause(dir)
	pause(other)
	check(dir, false)
	check(other, false)
	for _, cwd := range []string{dir, other} {
		git(t, cwd, "rebase", "--abort")
	}
	// A detached checkout without rebase metadata has no branch to check.
	git(t, dir, "checkout", "--detach")
	out, terminal, err := executeBinary(t, binary, dir, "status", "--is-reviewing", "--json")
	require.Error(t, err)
	require.Empty(t, terminal)
	require.Contains(t, out, `"status":"error"`)
}

func TestReviewRunningOutsideRepository(t *testing.T) {
	out, terminal, err := execute(t, t.TempDir(), "status", "--is-reviewing", "--json")
	require.Error(t, err)
	require.Empty(t, terminal)
	require.Contains(t, out, `"status":"error"`)
}

func TestStatusIsReviewingTerminalAndConflicts(t *testing.T) {
	binary := buildGitReview(t)
	dir := featureRepo(t)
	out, terminal, err := executeBinary(t, binary, dir, "status", "--is-reviewing")
	require.NoError(t, err)
	require.Empty(t, terminal)
	require.Contains(t, out, "Is reviewing: false")
	out, terminal, err = executeBinary(t, binary, dir, "status", "--is-reviewing", "--json")
	require.NoError(t, err)
	require.Empty(t, terminal)
	require.Contains(t, out, `"is_reviewing"`)
	for _, args := range [][]string{
		{"status", "--is-reviewing", "--needs-review", "--json"},
		{"status", "--is-reviewing", "--verbose", "--json"},
	} {
		out, terminal, err := executeBinary(t, binary, dir, args...)
		require.Error(t, err)
		require.Empty(t, terminal)
		require.Contains(t, out, `"status":"error"`)
	}
}

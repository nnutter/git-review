package cli

import (
	"context"
	"os"
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
		out, terminal, err := executeBinary(t, binary, cwd, "running", "--json")
		require.NoError(t, err)
		require.Empty(t, terminal)
		want := `{"version":1,"running":false}`
		if running {
			want = `{"version":1,"running":true}`
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
	called := false
	reviewer := review.Reviewer{
		Service: review.Service{Repository: review.Repository{Dir: dir}},
		Choose: func(context.Context, *review.Decision) (string, error) {
			called = true
			check(dir, true)
			check(other, false)
			return "cancel", nil
		},
	}
	_, err = reviewer.Review(t.Context(), review.ReviewOptions{})
	require.NoError(t, err)
	require.True(t, called)
	check(dir, false)
	check(other, false)
}

func TestReviewRunningOutsideRepository(t *testing.T) {
	out, terminal, err := execute(t, t.TempDir(), "running", "--json")
	require.Error(t, err)
	require.Empty(t, terminal)
	require.Contains(t, out, `"status":"error"`)
}

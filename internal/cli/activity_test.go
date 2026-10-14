package cli

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nnutter/umpire/internal/review"
)

func TestReviewRunningReportsLiveLockNotPersistedAttempt(t *testing.T) {
	binary := buildUmpire(t)
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
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	lock, err := os.OpenFile(path+".umpire.lock", os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lock.Close()) })
	require.NoError(t, syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	check(dir, true)
	other := filepath.Join(t.TempDir(), "checkout")
	git(t, dir, "worktree", "add", "-b", "other", other, "HEAD")
	check(other, true)
	require.NoError(t, syscall.Flock(int(lock.Fd()), syscall.LOCK_UN))
	check(dir, false)
}

func TestReviewRunningOutsideRepository(t *testing.T) {
	out, terminal, err := execute(t, t.TempDir(), "running", "--json")
	require.Error(t, err)
	require.Empty(t, terminal)
	require.Contains(t, out, `"status":"error"`)
}

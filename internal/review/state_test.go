package review

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApprovalAndSupersession(t *testing.T) {
	r := testRepo(t)
	id := "I" + strings.Repeat("c", 40)
	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "One\n\nChange-Id: "+id)
	first := gitTest(t, r.Dir, "rev-parse", "HEAD")
	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "fixup! One")
	tip := gitTest(t, r.Dir, "rev-parse", "HEAD")
	service := Service{Repository: r}
	_, err := service.Approve(t.Context(), first, "")
	require.ErrorContains(t, err, "complete stacks")
	result, err := service.Approve(t.Context(), first[:8], tip[:8])
	require.NoError(t, err)
	require.Equal(t, "approved", result.Status)
	require.Equal(t, "user-command", result.Entries[0].Attempt.ApprovalSource)
	result, err = service.Status(t.Context(), true)
	require.NoError(t, err)
	require.Equal(t, "idle", result.Status)
	require.Empty(t, result.Entries)

	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "amend! One")
	result, err = service.Status(t.Context(), true)
	require.NoError(t, err)
	require.Equal(t, "needs_review", result.Entries[0].Status)
	require.Equal(t, "approved", result.Entries[0].Previous.Status)

	// Feedback on the previous version must not globally block its replacement.
	store := Store{Path: result.View.Path}
	require.NoError(t, store.Update(func(state *State) error { state.Attempts[0].Status = "feedback"; return nil }))
	gitTest(t, r.Dir, "reset", "--hard", first)
	gitTest(t, r.Dir, "commit", "--amend", "--allow-empty", "-m", "Rewritten\n\nChange-Id: "+id)
	result, err = service.Status(t.Context(), true)
	require.NoError(t, err)
	require.Equal(t, "feedback", result.Entries[0].Previous.Status)
	require.Equal(t, "needs_review", result.Entries[0].Status)
	_, err = service.Approve(t.Context(), "HEAD", "")
	require.NoError(t, err)
}

func TestApprovalBlocksCurrentFeedbackAndActiveAttempts(t *testing.T) {
	for _, status := range []string{"feedback", "reviewing"} {
		t.Run(status, func(t *testing.T) {
			r := testRepo(t)
			gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "One")
			view, err := r.Snapshot(t.Context())
			require.NoError(t, err)
			store := Store{Path: view.Path}
			require.NoError(t, store.Update(func(state *State) error {
				state.Attempts = append(state.Attempts, newAttempt(view.Stacks[0], status))
				return nil
			}))
			_, err = (Service{Repository: r}).Approve(t.Context(), "HEAD", "")
			require.Error(t, err)
			state, err := store.Load()
			require.NoError(t, err)
			require.Len(t, state.Attempts, 1)
		})
	}
}

func TestApprovalWithPendingAttemptsOutsideRange(t *testing.T) {
	for _, status := range []string{"feedback", "reviewing"} {
		t.Run(status, func(t *testing.T) {
			r := testRepo(t)
			for _, subject := range []string{"Before", "First", "Last", "After"} {
				gitTest(t, r.Dir, "commit", "--allow-empty", "-m", subject)
			}
			view, err := r.Snapshot(t.Context())
			require.NoError(t, err)
			store := Store{Path: view.Path}
			require.NoError(t, store.Update(func(state *State) error {
				state.Attempts = append(state.Attempts,
					newAttempt(view.Stacks[0], status),
					newAttempt(view.Stacks[3], status))
				return nil
			}))
			service := Service{Repository: r}
			_, err = service.Approve(t.Context(), view.Stacks[1].Key, view.Stacks[2].Tip)
			if status == "reviewing" {
				require.ErrorContains(t, err, "active review")
				state, err := store.Load()
				require.NoError(t, err)
				require.Len(t, state.Attempts, 2)
				return
			}
			require.NoError(t, err)
			result, err := service.Status(t.Context(), false)
			require.NoError(t, err)
			require.Len(t, result.Entries, 4)
			require.Equal(t, "needs_review", result.Entries[0].Status) // Newer stacks address earlier feedback.
			require.Equal(t, "approved", result.Entries[1].Status)
			require.Equal(t, "approved", result.Entries[2].Status)
			require.Equal(t, "feedback", result.Entries[3].Status)
		})
	}
}

func TestStateLockAndCorruption(t *testing.T) {
	r := testRepo(t)
	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "One")
	view, err := r.Snapshot(t.Context())
	require.NoError(t, err)
	store := Store{Path: view.Path}
	require.NoError(t, store.Update(func(state *State) error {
		state.Attempts = append(state.Attempts, newAttempt(view.Stacks[0], "approved"))
		return nil
	}))
	original, err := os.ReadFile(store.Path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(store.Path+".lock", nil, 0o600))
	err = store.Update(func(state *State) error { state.Attempts = nil; return nil })
	require.ErrorContains(t, err, "locked")
	after, err := os.ReadFile(store.Path)
	require.NoError(t, err)
	require.Equal(t, original, after)
	require.NoError(t, os.Remove(store.Path+".lock"))
	for _, raw := range []string{`{"version":2,"attempts":[]}`, `{"version":1,"attempts":null}`, `{"version":1,"attempts":[{}]}`, `{"version":1,"version":1,"attempts":[]}`} {
		require.NoError(t, os.WriteFile(store.Path, []byte(raw), 0o600))
		_, err = store.Load()
		require.Error(t, err)
	}
}

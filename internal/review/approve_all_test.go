package review

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApproveAllApprovesEveryStack(t *testing.T) {
	r := testRepo(t)
	for _, subject := range []string{"First", "Second", "Third"} {
		gitTest(t, r.Dir, "commit", "--allow-empty", "-m", subject)
	}
	service := Service{Repository: r}
	result, err := service.ApproveAll(t.Context())
	require.NoError(t, err)
	require.Equal(t, "approved", result.Status)
	require.Len(t, result.Entries, 3)
	for _, entry := range result.Entries {
		require.Equal(t, "user-command", entry.Attempt.ApprovalSource)
	}
	status, err := service.Status(t.Context(), true)
	require.NoError(t, err)
	require.Empty(t, status.Entries)
}

func TestApproveAllBlockedByFeedbackOnAnyStack(t *testing.T) {
	r := testRepo(t)
	for _, subject := range []string{"First", "Second", "Third"} {
		gitTest(t, r.Dir, "commit", "--allow-empty", "-m", subject)
	}
	view, err := r.Snapshot(t.Context())
	require.NoError(t, err)
	store := Store{Path: view.Path}
	require.NoError(t, store.Update(func(state *State) error {
		state.Attempts = append(state.Attempts, newAttempt(view.Stacks[2], "feedback"))
		return nil
	}))
	_, err = (Service{Repository: r}).ApproveAll(t.Context())
	require.ErrorContains(t, err, "pending feedback")
	state, err := store.Load()
	require.NoError(t, err)
	require.Len(t, state.Attempts, 1)
}

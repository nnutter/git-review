package review

import (
	"encoding/json/jsontext"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatusOmitsFeedbackFromEarlierStacks(t *testing.T) {
	r := testRepo(t)
	for _, name := range []string{"one", "two"} {
		require.NoError(t, os.WriteFile(filepath.Join(r.Dir, name), []byte(name+"\n"), 0o600))
		gitTest(t, r.Dir, "add", ".")
		gitTest(t, r.Dir, "commit", "-m", "Add "+name)
	}
	view, err := r.Snapshot(t.Context())
	require.NoError(t, err)
	require.Len(t, view.Stacks, 2)
	comments := jsontext.Value(`{"comments":[{"text":"Please fix this"}]}`)
	require.NoError(t, (Store{Path: view.Path}).Update(func(state *State) error {
		for _, stack := range view.Stacks {
			attempt := newAttempt(stack, "feedback")
			attempt.Review = comments
			state.Attempts = append(state.Attempts, attempt)
		}
		return nil
	}))

	service := Service{Repository: r}
	result, err := service.Status(t.Context(), false)
	require.NoError(t, err)
	require.Len(t, result.Entries, 2)
	// A newer stack means the model already responded to the earlier feedback.
	require.Equal(t, "needs_review", result.Entries[0].Status)
	require.Nil(t, result.Entries[0].Attempt)
	require.Equal(t, "feedback", result.Entries[1].Status)
	require.NotNil(t, result.Entries[1].Attempt)
	require.Equal(t, []StateCount{
		{State: "needs_review", Count: 1},
		{State: "reviewing"},
		{State: "feedback", Count: 1},
		{State: "incomplete"},
		{State: "approved"},
	}, result.Summary)

	// The feedback command keeps reading every saved note.
	notes, err := service.Feedback(t.Context())
	require.NoError(t, err)
	require.Len(t, notes.Feedback, 2)
}

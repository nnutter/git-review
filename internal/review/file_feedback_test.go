package review

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileFeedbackDiscoversNotesWithoutChangingSessions(t *testing.T) {
	r := testRepo(t)
	store := t.TempDir()
	path := filepath.Join(store, "sessions/0123456789abcdef.json")
	session := fileFixture(t, r.Dir, "working_tree", "file", store, false, "sessions/0123456789abcdef.json")
	session["session_notes"] = "Check the public API"
	writeJSON(t, path, session)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	reader := SavedReader{Stores: []string{store, store}}
	feedback, err := reader.FileFeedback(r.Dir)
	require.NoError(t, err)
	require.Len(t, feedback, 1, "duplicate index discovery must not repeat feedback")
	require.Equal(t, "feedback", feedback[0].Status)
	require.Empty(t, feedback[0].Review.Comments)
	require.Equal(t, "Check the public API", *feedback[0].Review.SessionNotes)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)

	// A different repository must not inherit this annotation, even if its
	// directory name starts with the same prefix.
	other := r.Dir + "-other"
	require.NoError(t, os.Mkdir(other, 0o700))
	t.Cleanup(func() { _ = os.Remove(other) })
	otherStore := t.TempDir()
	otherSession := fileFixture(t, other, "working_tree", "file", otherStore, false, "sessions/0123456789abcdef.json")
	otherSession["session_notes"] = "Unrelated feedback"
	writeJSON(t, filepath.Join(otherStore, "sessions/0123456789abcdef.json"), otherSession)
	feedback, err = (SavedReader{Stores: []string{otherStore}}).FileFeedback(r.Dir)
	require.NoError(t, err)
	require.Empty(t, feedback)

	delete(session, "session_notes")
	writeJSON(t, path, session)
	feedback, err = reader.FileFeedback(r.Dir)
	require.NoError(t, err)
	require.Empty(t, feedback, "incomplete reviews without notes are not feedback")

	// Corrupt notes must be reported, not mistaken for absent feedback.
	session["session_notes"] = 42
	writeJSON(t, path, session)
	_, err = reader.FileFeedback(r.Dir)
	require.ErrorContains(t, err, "session_notes")
}

package cli

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nnutter/git-review/internal/display"
	"github.com/nnutter/git-review/internal/review"
)

func TestStatusCountsCurrentCommitsInStateOrder(t *testing.T) {
	binary := buildGitReview(t)
	dir := featureRepo(t)
	git(t, dir, "commit", "--allow-empty", "-m", "fixup! Add feature")
	for _, subject := range []string{"Active", "Feedback", "Partial", "Approved"} {
		git(t, dir, "commit", "--allow-empty", "-m", subject)
	}
	git(t, dir, "commit", "--allow-empty", "-m", "fixup! Approved")
	view, err := (review.Repository{Dir: dir}).Snapshot(t.Context())
	require.NoError(t, err)
	store := review.Store{Path: view.Path}
	require.NoError(t, store.Update(func(state *review.State) error {
		// An older approval must not override the latest feedback.
		state.Attempts = append(state.Attempts, review.Attempt{ID: "old", Stack: view.Stacks[2], Status: "approved", StartedAt: "2026-01-01T00:00:00Z"})
		for i, status := range []string{"reviewing", "feedback", "incomplete", "approved"} {
			state.Attempts = append(state.Attempts, review.Attempt{ID: status, Stack: view.Stacks[i+1], Status: status, StartedAt: "2026-01-01T00:00:00Z", Review: jsontext.Value(`{"reviewed":1,"files":2,"sessionNotes":"Saved review details"}`)})
		}
		return nil
	}))
	before, err := os.ReadFile(view.Path)
	require.NoError(t, err)
	want := []review.StateCount{{State: "needs_review", Count: 2}, {State: "reviewing", Count: 1}, {State: "feedback", Count: 1}, {State: "incomplete", Count: 1}, {State: "approved", Count: 2}}
	for _, args := range [][]string{{"status", "--json"}, {"--json", "status"}} {
		out, terminal, err := executeBinary(t, binary, dir, args...)
		require.NoError(t, err)
		require.Empty(t, terminal)
		var result review.Result
		require.NoError(t, json.Unmarshal([]byte(out), &result))
		require.Equal(t, 1, result.Version)
		require.Equal(t, "status", result.Command)
		require.Equal(t, "pending", result.Status)
		require.Equal(t, view.Branch, result.View.Branch)
		require.Equal(t, view.Target, result.View.Target)
		require.Equal(t, view.Path, result.View.Path)
		require.Equal(t, want, result.Summary)
		require.Len(t, result.Entries, len(view.Stacks))
		for i, entry := range result.Entries {
			require.Equal(t, view.Stacks[i], entry.Stack)
			require.Equal(t, want[i].State, entry.Status)
		}
		require.Len(t, result.Active, 1)
		require.Equal(t, "reviewing", result.Active[0].ID)
	}
	out, terminal, err := executeBinary(t, binary, dir, "status")
	require.NoError(t, err)
	require.Empty(t, terminal)
	require.Contains(t, out, "COMMITS (inclusive)")
	require.NotContains(t, out, "Feature review:")
	require.NotContains(t, out, "Target:")
	require.NotContains(t, out, "State:")
	require.NotContains(t, out, "Saved review details")
	require.Contains(t, out, "Active attempt: reviewing")
	for i, stack := range view.Stacks {
		require.Contains(t, out, display.ShortStack(stack))
		require.Contains(t, out, stack.Subject)
		require.Contains(t, out, display.StatusLabel(want[i].State))
	}
	filtered, terminal, err := executeBinary(t, binary, dir, "status", "--needs-review")
	require.NoError(t, err)
	require.Empty(t, terminal)
	for _, stack := range view.Stacks[:4] {
		require.Contains(t, filtered, display.ShortStack(stack))
	}
	require.NotContains(t, filtered, display.ShortStack(view.Stacks[4]))
	filtered, _, err = executeBinary(t, binary, dir, "status", "--needs-review", "--json")
	require.NoError(t, err)
	var result review.Result
	require.NoError(t, json.Unmarshal([]byte(filtered), &result))
	require.Equal(t, "status", result.Command)
	require.Equal(t, want, result.Summary)
	require.Len(t, result.Entries, 4)
	for i, entry := range result.Entries {
		require.Equal(t, view.Stacks[i], entry.Stack)
		require.Equal(t, want[i].State, entry.Status)
	}
	require.Len(t, result.View.Stacks, 5)
	require.Len(t, result.Active, 1)
	after, err := os.ReadFile(view.Path)
	require.NoError(t, err)
	require.Equal(t, before, after)

	// A changed stack loses approval, and removed stacks contribute nothing.
	git(t, dir, "commit", "--allow-empty", "-m", "fixup! Approved")
	out, _, err = executeBinary(t, binary, dir, "status", "--json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.Equal(t, []review.StateCount{{State: "needs_review", Count: 5}, {State: "reviewing", Count: 1}, {State: "feedback", Count: 1}, {State: "incomplete", Count: 1}, {State: "approved", Count: 0}}, result.Summary)
	assertStatusVerbosity(t, binary, dir, out)
	git(t, dir, "reset", "--hard", "main")
	out, _, err = executeBinary(t, binary, dir, "status", "--json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.Equal(t, []review.StateCount{{State: "needs_review"}, {State: "reviewing"}, {State: "feedback"}, {State: "incomplete"}, {State: "approved"}}, result.Summary)
	require.Empty(t, result.Entries)
	require.Equal(t, "pending", result.Status) // The historical active attempt still needs recovery.
}

func assertStatusVerbosity(t *testing.T, binary, dir, out string) {
	t.Helper()
	for _, flag := range []string{"", "-v", "--verbose"} {
		args := []string{"status"}
		if flag != "" {
			args = append(args, flag)
		}
		text, terminal, err := executeBinary(t, binary, dir, args...)
		require.NoError(t, err)
		require.Empty(t, terminal)
		require.Contains(t, text, "Active attempt: reviewing")
		if flag == "" {
			require.NotContains(t, text, "Saved review details")
			require.NotContains(t, text, "Previous version (historical):")
		} else {
			require.Contains(t, text, "Previous version (historical): Review approved:")
			require.Contains(t, text, "Review feedback:")
			require.Contains(t, text, "Saved review details")
			require.Less(t, strings.LastIndex(text, "Saved review details"), strings.Index(text, "COMMITS (inclusive)"))
		}
		jsonOut, _, err := executeBinary(t, binary, dir, append(args, "--json")...)
		require.NoError(t, err)
		require.JSONEq(t, out, jsonOut)
	}
}

func TestStatusWithoutReviewHistory(t *testing.T) {
	dir := featureRepo(t)
	view, err := (review.Repository{Dir: dir}).Snapshot(t.Context())
	require.NoError(t, err)
	out, terminal, err := execute(t, dir, "status", "--json")
	require.NoError(t, err)
	require.Empty(t, terminal)
	var result review.Result
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.Equal(t, []review.StateCount{{State: "needs_review", Count: 1}, {State: "reviewing"}, {State: "feedback"}, {State: "incomplete"}, {State: "approved"}}, result.Summary)
	require.Len(t, result.Entries, 1)
	require.Equal(t, "needs_review", result.Entries[0].Status)
	_, err = os.Stat(view.Path)
	require.ErrorIs(t, err, os.ErrNotExist)

	_, _, err = execute(t, dir, "approve", "HEAD")
	require.NoError(t, err)
	out, _, err = execute(t, dir, "status", "--json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.Equal(t, "idle", result.Status)
	require.Len(t, result.Entries, 1)
	require.Equal(t, "approved", result.Entries[0].Status)
	out, _, err = execute(t, dir, "status")
	require.NoError(t, err)
	require.Contains(t, out, "Approved")
	require.Contains(t, out, "Add feature")
	filtered, _, err := execute(t, dir, "status", "--needs-review")
	require.NoError(t, err)
	require.Equal(t, "No reviews pending.\n", filtered)
}

func TestStatusOutsideGitRepository(t *testing.T) {
	binary := buildGitReview(t)
	dir := t.TempDir()
	const message = "git-review must be used from inside a Git repository"
	for _, args := range [][]string{{"status"}, {"status", "--json"}} {
		t.Run(args[len(args)-1], func(t *testing.T) {
			out, terminal, err := executeBinary(t, binary, dir, args...)
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit)
			require.Equal(t, 1, exit.ExitCode())
			if len(args) == 2 {
				require.Empty(t, terminal)
				var failed display.ErrorResult
				require.NoError(t, json.Unmarshal([]byte(out), &failed))
				require.Equal(t, "status", failed.Command)
				require.Equal(t, "error", failed.Status)
				require.Equal(t, message, failed.Error)
			} else {
				require.Empty(t, out)
				require.Contains(t, terminal, "Git-Review must be used from inside a Git repository.")
				require.NotContains(t, terminal, "git symbolic-ref")
				require.NotContains(t, terminal, "detached HEAD")
				require.NotContains(t, terminal, "exit status")
			}
		})
	}
}

func TestStatusJSONErrors(t *testing.T) {
	dir := featureRepo(t)
	for _, args := range [][]string{{"status", "extra", "--json"}, {"status", "--unknown", "--json"}} {
		out, terminal, err := execute(t, dir, args...)
		require.Error(t, err)
		require.Empty(t, terminal)
		var failed display.ErrorResult
		require.NoError(t, json.Unmarshal([]byte(out), &failed))
		require.Equal(t, "status", failed.Command)
		require.Equal(t, "error", failed.Status)
	}
	git(t, dir, "branch", "--unset-upstream")
	out, terminal, err := execute(t, dir, "status", "--json")
	require.Error(t, err)
	require.Empty(t, terminal)
	var failed display.ErrorResult
	require.NoError(t, json.Unmarshal([]byte(out), &failed))
	require.Equal(t, "status", failed.Command)
	require.Equal(t, "error", failed.Status)
}

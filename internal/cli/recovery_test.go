package cli

import (
	"encoding/json/jsontext"
	"testing"

	"github.com/nnutter/git-review/internal/review"
	"github.com/stretchr/testify/require"
)

func TestRecoveryDecisionExplainsSavedWork(t *testing.T) {
	for _, tc := range []struct {
		name        string
		saved       jsontext.Value
		want        string
		replacement string
	}{
		{"saved progress", jsontext.Value(`{"reviewed":1,"files":2,"comments":[{"content":"Fix this"}],"sessionNotes":"Check this"}`), "Saved progress: 1/2 files reviewed · Comments: 1 · session notes saved", "changed"},
		{"missing progress", nil, "Saved progress: unavailable", "same"},
		{"missing counters", jsontext.Value(`{"comments":[]}`), "Saved progress: unavailable", "same"},
		{"no replacement", nil, "Saved progress: unavailable", "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := review.Stack{Base: "base", Tip: "bbbbbbbbbbbb", Commits: []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"}, Subject: "Fix recovery\x1b[31m\nhandling"}
			current := review.Stack{Base: "base", Tip: "cccccccccccc", Commits: []string{"cccccccccccc"}, Subject: "Revised recovery"}
			result := review.Result{Command: "run", Status: "decision_required", Decision: &review.Decision{
				AttemptID:   "internal-id",
				Attempt:     review.Attempt{Stack: saved, Status: "incomplete", StartedAt: "2026-10-16T12:00:00Z", Review: tc.saved},
				Replacement: &current,
			}}
			switch tc.replacement {
			case "same":
				result.Decision.Replacement = &saved
			case "none":
				result.Decision.Replacement = nil
			}
			text, err := renderResult(result, false)
			require.NoError(t, err)
			require.Contains(t, text, "Saved commits (inclusive): aaaaaaaa..bbbbbbbb — Fix recovery handling")
			require.Contains(t, text, "Started: 2026-10-16T12:00:00Z · Status: Incomplete")
			require.Contains(t, text, tc.want)
			if tc.replacement == "changed" {
				require.Contains(t, text, "New review commits (inclusive): cccccccc — Revised recovery")
			} else {
				require.NotContains(t, text, "New review commits")
			}
			if tc.replacement == "none" {
				require.Contains(t, text, "No current replacement scope. Select one with git-review run <commit>.")
			}
			require.Contains(t, text, "Replacement keeps the prior attempt in history. Tuicr may reuse its saved session.")
			require.NotContains(t, text, "internal-id")
		})
	}
}

package review

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReviewSelectsWithinInclusiveBounds(t *testing.T) {
	for _, bounds := range []string{"start", "end", "both"} {
		t.Run(bounds, func(t *testing.T) {
			reviewer, _, store := testReviewer(t)
			r := reviewer.Service.Repository
			gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Second")
			gitTest(t, r.Dir, "commit", "--allow-empty", "--fixup=HEAD")
			secondTip := gitTest(t, r.Dir, "rev-parse", "HEAD")
			gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Third")
			view, err := r.Snapshot(t.Context())
			require.NoError(t, err)
			options := ReviewOptions{}
			if bounds != "end" {
				options.Start = secondTip
			}
			if bounds != "start" {
				options.End = view.Stacks[1].Commits[0]
			}
			if bounds == "end" {
				require.NoError(t, (Store{Path: view.Path}).Update(func(state *State) error {
					state.Attempts = append(state.Attempts, newAttempt(view.Stacks[0], "approved"))
					return nil
				}))
			}
			path, err := WorktreePath(r.Dir)
			require.NoError(t, err)
			scope := view.Stacks[1]
			scope.Repo = path
			savedFixture(t, scope, store, true)
			log := filepath.Join(t.TempDir(), "launch")
			t.Setenv("TEST_LAUNCH_LOG", log)
			fakeTuicr(t, `printf '%s\n' "$@" >> "$TEST_LAUNCH_LOG"`)
			result, err := reviewer.Review(t.Context(), options)
			require.NoError(t, err)
			if bounds == "start" {
				require.Equal(t, "cancelled", result.Status)
				require.Equal(t, view.Stacks[2].Tip, gitTest(t, path, "rev-parse", "HEAD"))
			} else {
				require.Equal(t, "range_complete", result.Status)
				require.Equal(t, secondTip, gitTest(t, path, "rev-parse", "HEAD"))
			}
			state, err := (Store{Path: view.Path}).Load()
			require.NoError(t, err)
			attempt := state.latest(view.Stacks[1])
			require.NotNil(t, attempt)
			require.Equal(t, "approved", attempt.Status)
			if bounds != "start" {
				result, err = reviewer.Review(t.Context(), options)
				require.NoError(t, err)
				require.Equal(t, "range_complete", result.Status)
				launch, err := os.ReadFile(log)
				require.NoError(t, err)
				require.Equal(t, "--no-update-check\n--stdout\n-r\n"+scope.Base+".."+scope.Tip+"\n", string(launch))
			}
		})
	}
}

func TestBoundedReviewContinuesUntilApprovalOrUserInput(t *testing.T) {
	for _, outcome := range []string{"approved", "feedback", "incomplete", "cancelled", "decision_required", "error"} {
		t.Run(outcome, func(t *testing.T) {
			reviewer, _, firstStore := testReviewer(t)
			r := reviewer.Service.Repository
			gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Second")
			gitTest(t, r.Dir, "commit", "--allow-empty", "--fixup=HEAD")
			gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Third")
			gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Fourth")
			view, err := r.Snapshot(t.Context())
			require.NoError(t, err)
			path, err := WorktreePath(r.Dir)
			require.NoError(t, err)
			first, second := view.Stacks[1], view.Stacks[2]
			first.Repo, second.Repo = path, path
			savedFixture(t, first, firstStore, true)
			secondStore := t.TempDir()
			reviewer.Reader.Stores = append(reviewer.Reader.Stores, secondStore)
			if outcome != "cancelled" {
				session := savedFixture(t, second, secondStore, true)
				session["id"] = "second-session"
				if outcome == "feedback" {
					session["session_notes"] = "Please revise"
				}
				if outcome == "incomplete" {
					session["files"].(map[string]any)["CommitMessage"].(map[string]any)["reviewed"] = false
				}
				writeJSON(t, filepath.Join(secondStore, "sessions/0123456789abcdef.json"), session)
			}
			stateStore := Store{Path: view.Path}
			prior := newAttempt(view.Stacks[1], "incomplete")
			require.NoError(t, stateStore.Update(func(state *State) error {
				state.Attempts = append(state.Attempts, prior)
				if outcome == "decision_required" {
					state.Attempts = append(state.Attempts, newAttempt(view.Stacks[2], "incomplete"))
				}
				return nil
			}))
			log := filepath.Join(t.TempDir(), "launch")
			t.Setenv("TEST_LAUNCH_LOG", log)
			t.Setenv("TEST_FAIL_TIP", "")
			if outcome == "error" {
				t.Setenv("TEST_FAIL_TIP", second.Tip)
			}
			fakeTuicr(t, `printf '%s\n' "$4" >> "$TEST_LAUNCH_LOG"
if [ "$(git rev-parse HEAD)" = "$TEST_FAIL_TIP" ]; then exit 1; fi`)
			result, err := reviewer.Review(t.Context(), ReviewOptions{Start: first.Tip, End: second.Tip, Choice: "recover", Session: "fixture-session"})
			if outcome == "error" {
				require.ErrorContains(t, err, "tuicr interrupted")
			} else {
				require.NoError(t, err)
				want := outcome
				if outcome == "approved" {
					want = "range_complete"
				}
				require.Equal(t, want, result.Status)
			}
			launch, err := os.ReadFile(log)
			require.NoError(t, err)
			wantLaunch := first.Base + ".." + first.Tip + "\n"
			if outcome != "decision_required" {
				wantLaunch += second.Base + ".." + second.Tip + "\n"
			}
			require.Equal(t, wantLaunch, string(launch))
			state, err := stateStore.Load()
			require.NoError(t, err)
			attempt := state.latest(view.Stacks[1])
			require.NotNil(t, attempt)
			require.Equal(t, prior.ID, attempt.ID)
			require.Equal(t, "approved", attempt.Status)
			require.Nil(t, state.latest(view.Stacks[0]))
			require.Nil(t, state.latest(view.Stacks[3]))
		})
	}
}

func TestBoundedReviewRetainsFeedbackAndContinuesToLaterStacks(t *testing.T) {
	reviewer, _, _ := testReviewer(t)
	r := reviewer.Service.Repository
	gitTest(t, r.Dir, "commit", "--allow-empty", "--fixup=HEAD")
	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Second")
	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Third")
	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Outside range")
	view, err := r.Snapshot(t.Context())
	require.NoError(t, err)
	path, err := WorktreePath(r.Dir)
	require.NoError(t, err)
	wantLaunch := ""
	for i, stack := range view.Stacks[:3] {
		stack.Repo = path
		store := t.TempDir()
		reviewer.Reader.Stores = append(reviewer.Reader.Stores, store)
		session := savedFixture(t, stack, store, true)
		if i < 2 {
			session["session_notes"] = "Please revise"
			writeJSON(t, filepath.Join(store, "sessions/0123456789abcdef.json"), session)
		}
		wantLaunch += stack.Base + ".." + stack.Tip + "\n"
	}
	log := filepath.Join(t.TempDir(), "launch")
	t.Setenv("TEST_LAUNCH_LOG", log)
	fakeTuicr(t, `printf '%s\n' "$4" >> "$TEST_LAUNCH_LOG"`)
	options := ReviewOptions{Start: view.Stacks[0].Tip, End: view.Stacks[2].Tip}
	result, err := reviewer.Review(t.Context(), options)
	require.NoError(t, err)
	require.Equal(t, "feedback", result.Status)
	launch, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Equal(t, wantLaunch, string(launch))
	state, err := (Store{Path: view.Path}).Load()
	require.NoError(t, err)
	require.Len(t, state.Attempts, 3)
	for i, status := range []string{"feedback", "feedback", "approved"} {
		require.Equal(t, view.Stacks[i].Commits, state.Attempts[i].Stack.Commits)
		require.Equal(t, status, state.Attempts[i].Status)
		if status == "feedback" {
			require.Contains(t, string(state.Attempts[i].Review), "Please revise")
		}
	}
	// Feedback remains unresolved and needs an explicit choice on a later invocation.
	result, err = reviewer.Review(t.Context(), options)
	require.NoError(t, err)
	require.Equal(t, "decision_required", result.Status)
	require.Equal(t, state.Attempts[0].ID, result.Decision.AttemptID)
	launch, err = os.ReadFile(log)
	require.NoError(t, err)
	require.Equal(t, wantLaunch, string(launch))
}

func TestReviewRejectsInvalidBoundsWithoutStateChanges(t *testing.T) {
	reviewer, _, _ := testReviewer(t)
	r := reviewer.Service.Repository
	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Second")
	view, err := r.Snapshot(t.Context())
	require.NoError(t, err)
	for _, tc := range []struct {
		options ReviewOptions
		message string
	}{
		{ReviewOptions{Start: "HEAD", End: "HEAD~1"}, "oldest-first"},
		{ReviewOptions{Start: "main"}, "outside"},
		{ReviewOptions{End: "main"}, "outside"},
		{ReviewOptions{Start: "missing-ref"}, "rev-parse"},
		{ReviewOptions{Stack: "HEAD", Start: "HEAD"}, "explicit stack"},
	} {
		_, err := reviewer.Review(t.Context(), tc.options)
		require.ErrorContains(t, err, tc.message)
		_, err = os.Stat(view.Path)
		require.True(t, os.IsNotExist(err))
	}
}

func TestBoundedReviewRetainsDeferredAndActiveAttempts(t *testing.T) {
	reviewer, view, store := testReviewer(t)
	r := reviewer.Service.Repository
	first := view.Stacks[0]
	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Second")
	view, err := r.Snapshot(t.Context())
	require.NoError(t, err)
	deferred := newAttempt(view.Stacks[1], "incomplete")
	deferred.Deferred = true
	active := newAttempt(first, "reviewing")
	stateStore := Store{Path: view.Path}
	require.NoError(t, stateStore.Update(func(state *State) error {
		state.Attempts = append(state.Attempts, deferred)
		return nil
	}))
	options := ReviewOptions{Start: "HEAD", End: "HEAD"}
	result, err := reviewer.Review(t.Context(), options)
	require.NoError(t, err)
	require.Equal(t, "no_waiting", result.Status)
	require.NoError(t, stateStore.Update(func(state *State) error {
		state.Attempts = append(state.Attempts, active)
		return nil
	}))
	result, err = reviewer.Review(t.Context(), options)
	require.NoError(t, err)
	require.Equal(t, "decision_required", result.Status)
	require.Equal(t, active.ID, result.Decision.AttemptID)
	path, err := WorktreePath(r.Dir)
	require.NoError(t, err)
	scope := first
	scope.Repo = path
	savedFixture(t, scope, store, true)
	fakeTuicr(t, "exit 0")
	options.Choice = "recover"
	result, err = reviewer.Review(t.Context(), options)
	require.NoError(t, err)
	require.Equal(t, "no_waiting", result.Status)
	state, err := stateStore.Load()
	require.NoError(t, err)
	attempt := state.latest(first)
	require.NotNil(t, attempt)
	require.Equal(t, active.ID, attempt.ID)
	require.Equal(t, "approved", attempt.Status)
	require.Equal(t, first.Tip, gitTest(t, path, "rev-parse", "HEAD"))
}

package review

import (
	"encoding/json/jsontext"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAppendedFeedbackWithUnchangedPatchRequiresReview(t *testing.T) {
	for _, tc := range []struct {
		subject string
		want    string
	}{
		{subject: "amend! Feature", want: "needs_review"},
		{subject: "reword! Feature", want: "needs_review"},
		// A fixup! commit carries no message, so patch identity alone decides it.
		{subject: "fixup! Feature", want: "approved"},
	} {
		t.Run(tc.subject, func(t *testing.T) {
			r := testRepo(t)
			file := filepath.Join(r.Dir, "file")
			require.NoError(t, os.WriteFile(file, []byte("one\n"), 0o600))
			gitTest(t, r.Dir, "add", ".")
			gitTest(t, r.Dir, "commit", "-m", "Feature")
			service := Service{Repository: r}
			_, err := service.Approve(t.Context(), "HEAD", "")
			require.NoError(t, err)
			before, err := r.Snapshot(t.Context())
			require.NoError(t, err)
			require.NotEmpty(t, before.Stacks[0].PatchHash)
			gitTest(t, r.Dir, "commit", "--allow-empty", "-m", tc.subject)
			after, err := r.Snapshot(t.Context())
			require.NoError(t, err)
			require.Len(t, after.Stacks, 1)
			require.Equal(t, before.Stacks[0].PatchHash, after.Stacks[0].PatchHash)
			if tc.want == "approved" {
				require.Equal(t, before.Stacks[0].AmendCount, after.Stacks[0].AmendCount)
				result, err := service.Status(t.Context(), true)
				require.NoError(t, err)
				require.Equal(t, "idle", result.Status)
				require.Empty(t, result.Entries)
				return
			}
			require.Equal(t, before.Stacks[0].AmendCount+1, after.Stacks[0].AmendCount)
			result, err := service.Status(t.Context(), true)
			require.NoError(t, err)
			require.Len(t, result.Entries, 1)
			require.Equal(t, "needs_review", result.Entries[0].Status)
			require.Equal(t, "approved", result.Entries[0].Previous.Status)
		})
	}
}

func TestReviewStatusAfterStackRewrite(t *testing.T) {
	for _, tc := range []struct {
		name, status, rewrite, want string
		legacy                      bool
		v1                          bool
	}{
		{name: "approved reword", status: "approved", rewrite: "reword", want: "approved"},
		{name: "approved rebase", status: "approved", rewrite: "rebase", want: "approved"},
		{name: "approved squash", status: "approved", rewrite: "squash", want: "approved"},
		{name: "feedback squash", status: "feedback", rewrite: "squash", want: "feedback"},
		{name: "incomplete reword", status: "incomplete", rewrite: "reword", want: "needs_review"},
		{name: "active reword", status: "reviewing", rewrite: "reword", want: "needs_review"},
		{name: "changed content", status: "approved", rewrite: "content", want: "needs_review"},
		{name: "legacy reword", status: "approved", rewrite: "reword", want: "needs_review", legacy: true},
		{name: "v1 reword", status: "approved", rewrite: "reword", want: "needs_review", v1: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRepo(t)
			file := filepath.Join(r.Dir, "file")
			require.NoError(t, os.WriteFile(file, []byte("one\n"), 0o600))
			gitTest(t, r.Dir, "add", ".")
			gitTest(t, r.Dir, "commit", "-m", "Feature")
			require.NoError(t, os.WriteFile(file, []byte("two\n"), 0o600))
			gitTest(t, r.Dir, "add", ".")
			gitTest(t, r.Dir, "commit", "-m", "fixup! Feature")
			view, err := r.Snapshot(t.Context())
			require.NoError(t, err)
			attempt := newAttempt(view.Stacks[0], tc.status)
			if tc.legacy {
				attempt.Stack.PatchHash = ""
			}
			if tc.v1 {
				attempt.Stack.PatchHash = "v1:" + strings.Repeat("a", 64)
			}
			if tc.status == "feedback" {
				attempt.Review = jsontext.Value(`{"comments":[{"text":"Please fix this"}]}`)
			}
			store := Store{Path: view.Path}
			require.NoError(t, store.Update(func(state *State) error {
				state.Attempts = append(state.Attempts, attempt)
				return nil
			}))
			service := Service{Repository: r}
			statusResult, err := service.Status(t.Context(), false)
			require.NoError(t, err)
			for _, count := range statusResult.Summary {
				if count.State == tc.status {
					require.Equal(t, 2, count.Count)
				}
			}
			before, err := os.ReadFile(store.Path)
			require.NoError(t, err)

			rewriteTestStack(t, r, view, tc.rewrite)
			result, err := service.Status(t.Context(), true)
			require.NoError(t, err)
			require.NotEqual(t, view.Stacks[0].Key, result.View.Stacks[0].Key)
			if tc.want == "approved" {
				require.Equal(t, "idle", result.Status)
				require.Empty(t, result.Entries)
				// Automatic review must not launch tuicr for preserved approval.
				reviewed, err := (Reviewer{Service: service}).Review(t.Context(), ReviewOptions{})
				require.NoError(t, err)
				require.Equal(t, "range_complete", reviewed.Status)
			} else {
				require.Len(t, result.Entries, 1)
				require.Equal(t, tc.want, result.Entries[0].Status)
				if tc.want == "feedback" {
					require.Equal(t, attempt.Stack, result.Entries[0].Attempt.Stack)
					notes, err := service.Feedback(t.Context())
					require.NoError(t, err)
					require.Len(t, notes.Feedback, 1)
					require.False(t, notes.Feedback[0].Historical)
					require.Equal(t, attempt.ID, notes.Feedback[0].Attempt.ID)
					require.Equal(t, attempt.Stack, notes.Feedback[0].Attempt.Stack)
					require.JSONEq(t, string(attempt.Review), string(notes.Feedback[0].Attempt.Review))
					_, err = service.Approve(t.Context(), "HEAD", "")
					require.ErrorContains(t, err, "pending feedback")
				}
			}
			statusResult, err = service.Status(t.Context(), false)
			require.NoError(t, err)
			assertStateCounts(t, statusResult.Summary, tc.want, len(result.View.Stacks[0].Commits))
			after, err := os.ReadFile(store.Path)
			require.NoError(t, err)
			require.Equal(t, before, after, "status matching must not rewrite captured scopes")
		})
	}
}

func assertStateCounts(t *testing.T, counts []StateCount, status string, commits int) {
	t.Helper()
	for _, count := range counts {
		want := 0
		if count.State == status {
			want = commits
		}
		require.Equal(t, want, count.Count, "state %s", count.State)
	}
}

// A rebase can change blob identities and hunk offsets without changing the
// reviewed patch. Python indentation must still distinguish rewritten content.
func TestApprovalAfterTouchedFileRebase(t *testing.T) {
	for _, indentChanged := range []bool{false, true} {
		name := "shifted hunk"
		if indentChanged {
			name = "changed Python indentation"
		}
		t.Run(name, func(t *testing.T) {
			r := testRepo(t)
			file := filepath.Join(r.Dir, "script.py")
			prefix := strings.Repeat("# unchanged\n", 10)
			base := prefix + "if True:\n    print('one')\n"
			require.NoError(t, os.WriteFile(file, []byte(base), 0o600))
			gitTest(t, r.Dir, "add", ".")
			gitTest(t, r.Dir, "commit", "-m", "Add script")
			gitTest(t, r.Dir, "checkout", "main")
			gitTest(t, r.Dir, "merge", "--ff-only", "feature")
			gitTest(t, r.Dir, "checkout", "feature")
			changed := prefix + "if True:\n    print('two')\n"
			require.NoError(t, os.WriteFile(file, []byte(changed), 0o600))
			gitTest(t, r.Dir, "add", ".")
			gitTest(t, r.Dir, "commit", "-m", "Change output")
			service := Service{Repository: r}
			approved, err := service.Approve(t.Context(), "HEAD", "")
			require.NoError(t, err)

			gitTest(t, r.Dir, "checkout", "main")
			require.NoError(t, os.WriteFile(file, []byte("# header\n"+base), 0o600))
			gitTest(t, r.Dir, "add", ".")
			gitTest(t, r.Dir, "commit", "-m", "Add header")
			gitTest(t, r.Dir, "checkout", "feature")
			gitTest(t, r.Dir, "rebase", "main")
			if indentChanged {
				changed = strings.Replace(changed, "    print", "        print", 1)
				require.NoError(t, os.WriteFile(file, []byte("# header\n"+changed), 0o600))
				gitTest(t, r.Dir, "add", ".")
				gitTest(t, r.Dir, "commit", "--amend", "--no-edit")
			}
			result, err := service.Status(t.Context(), true)
			require.NoError(t, err)
			require.NotEqual(t, approved.View.Head, result.View.Head)
			if indentChanged {
				require.Len(t, result.Entries, 1)
				require.Equal(t, "needs_review", result.Entries[0].Status)
			} else {
				require.Empty(t, result.Entries)
				require.Equal(t, "idle", result.Status)
			}
		})
	}
}

func rewriteTestStack(t *testing.T, r Repository, view Snapshot, kind string) {
	t.Helper()
	switch kind {
	case "reword":
		gitTest(t, r.Dir, "commit", "--amend", "-m", "amend! Feature")
	case "squash":
		gitTest(t, r.Dir, "reset", "--soft", view.Base)
		gitTest(t, r.Dir, "commit", "-m", "Squashed feature")
	case "rebase":
		gitTest(t, r.Dir, "checkout", "main")
		require.NoError(t, os.WriteFile(filepath.Join(r.Dir, "other"), []byte("unrelated\n"), 0o600))
		gitTest(t, r.Dir, "add", ".")
		gitTest(t, r.Dir, "commit", "-m", "Unrelated upstream change")
		gitTest(t, r.Dir, "checkout", "feature")
		gitTest(t, r.Dir, "rebase", "main")
	case "content":
		require.NoError(t, os.WriteFile(filepath.Join(r.Dir, "file"), []byte("two \n"), 0o600))
		gitTest(t, r.Dir, "add", ".")
		gitTest(t, r.Dir, "commit", "--amend", "-m", "fixup! Feature")
	default:
		require.FailNow(t, "unknown rewrite", "kind: %s", kind)
	}
}

func TestStackRewriteDoesNotRestoreSupersededApproval(t *testing.T) {
	for _, status := range []string{"incomplete", "addressed"} {
		t.Run(status, func(t *testing.T) {
			r := testRepo(t)
			require.NoError(t, os.WriteFile(filepath.Join(r.Dir, "file"), []byte("one\n"), 0o600))
			gitTest(t, r.Dir, "add", ".")
			gitTest(t, r.Dir, "commit", "-m", "Feature")
			service := Service{Repository: r}
			approved, err := service.Approve(t.Context(), "HEAD", "")
			require.NoError(t, err)
			stack := approved.View.Stacks[0]
			require.NoError(t, (Store{Path: approved.View.Path}).Update(func(state *State) error {
				newer := newAttempt(stack, status)
				if status == "addressed" {
					newer.Replacement = &stack
				}
				state.Attempts = append(state.Attempts, newer)
				return nil
			}))
			gitTest(t, r.Dir, "commit", "--amend", "-m", "Reworded")
			result, err := service.Status(t.Context(), true)
			require.NoError(t, err)
			require.Len(t, result.Entries, 1)
			require.Equal(t, "needs_review", result.Entries[0].Status)
		})
	}
}

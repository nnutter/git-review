package review

import (
	"context"
	"encoding/json/v2"
	"fmt"
)

// Entry presents the status of one current stack and optional prior lineage.
type Entry struct {
	Stack    Stack    `json:"stack"`
	Status   string   `json:"status"`
	Attempt  *Attempt `json:"attempt,omitzero"`
	Previous *Attempt `json:"previous,omitzero"`
}

// Result is the shared input to JSON encoding and terminal rendering.
type Result struct {
	Version      int              `json:"version"`
	Command      string           `json:"command"`
	Status       string           `json:"status"`
	View         Snapshot         `json:"view"`
	Entries      []Entry          `json:"entries"`
	Active       []Attempt        `json:"active"`
	Decision     *Decision        `json:"decision,omitzero"`
	Feedback     []FeedbackRecord `json:"feedback,omitempty"`
	FileFeedback []FilesResult    `json:"fileFeedback,omitempty"`
	Summary      []StateCount     `json:"summary,omitempty"`
}

// FeedbackRecord retains captured scope and marks obsolete or addressed notes.
type FeedbackRecord struct {
	Attempt    Attempt `json:"attempt"`
	Historical bool    `json:"historical"`
}

// Decision requires an explicit choice; it is not review approval.
type Decision struct {
	AttemptID string   `json:"attemptId"`
	StackKey  string   `json:"stackKey"`
	Choices   []string `json:"choices"`
	Attempt   Attempt  `json:"attempt"`
	// Replacement is the current scope a new attempt would review, if available.
	Replacement *Stack `json:"replacement,omitzero"`
}

// StateCount counts current feature commits in one review state.
type StateCount struct {
	State string `json:"state"`
	Count int    `json:"count"`
}

func countStates(entries []Entry) []StateCount {
	counts := []StateCount{
		{State: "needs_review"},
		{State: "reviewing"},
		{State: "feedback"},
		{State: "incomplete"},
		{State: "approved"},
	}
	for _, entry := range entries {
		for i := range counts {
			if counts[i].State == entry.Status {
				counts[i].Count += len(entry.Stack.Commits)
				break
			}
		}
	}
	return counts
}

func appendCommitFeedback(result *Result, state State) error {
	for _, attempt := range state.Attempts {
		if len(attempt.Review) == 0 {
			continue
		}
		var saved SavedReview
		if err := json.Unmarshal(attempt.Review, &saved); err != nil {
			return fmt.Errorf("invalid stored review %s: %w", attempt.ID, err)
		}
		if saved.Status() != "feedback" {
			continue
		}
		historical := !currentAttempt(result.View, state, attempt)
		result.Feedback = append(result.Feedback, FeedbackRecord{Attempt: attempt, Historical: historical})
		if !historical {
			result.Status = "pending"
		}
	}
	return nil
}

// Service coordinates the current branch with its durable review history.
type Service struct{ Repository Repository }

// Approve records explicit approval of inclusive endpoints and complete stacks.
func (s Service) Approve(ctx context.Context, first, last string) (Result, error) {
	view, err := s.Repository.Snapshot(ctx)
	if err != nil {
		return Result{}, err
	}
	selected, err := s.selectRange(ctx, view, first, last)
	if err != nil {
		return Result{}, err
	}
	return s.recordApproval(ctx, view, selected)
}

// ApproveAll records explicit approval of every stack in the feature range.
func (s Service) ApproveAll(ctx context.Context) (Result, error) {
	view, err := s.Repository.Snapshot(ctx)
	if err != nil {
		return Result{}, err
	}
	return s.recordApproval(ctx, view, view.Stacks)
}

// Feedback reads saved commit and file feedback without changing either store.
func (s Service) Feedback(ctx context.Context) (Result, error) {
	view, repo, err := s.Repository.checkoutView(ctx)
	if err != nil {
		return Result{}, err
	}
	state, err := (Store{Path: view.Path}).Load()
	if err != nil {
		return Result{}, err
	}
	view, err = s.feedbackView(ctx, view, state)
	if err != nil {
		return Result{}, err
	}
	reader, err := DefaultSavedReader()
	if err != nil {
		return Result{}, err
	}
	files, err := reader.FileFeedback(repo)
	if err != nil {
		return Result{}, err
	}
	result := Result{Version: 1, Command: "feedback", Status: "idle", View: view, Entries: []Entry{}, Active: activeAttempts(state), FileFeedback: files}
	if len(files) > 0 {
		result.Status = "pending"
	}
	if err := appendCommitFeedback(&result, state); err != nil {
		return Result{}, err
	}
	return result, nil
}

// ReviewRunning reports whether the current or rebasing branch owns the live review lock.
// It does not create the lock file or infer activity from persisted attempts.
func (s Service) ReviewRunning(ctx context.Context) (bool, error) {
	path, err := WorktreePath(s.Repository.Dir)
	if err != nil {
		return false, err
	}
	branch, err := s.Repository.reviewBranch(ctx)
	if err != nil {
		return false, err
	}
	return reviewRunning(ctx, path, branch)
}

// Status returns current stacks and ordered commit counts without changing review state.
// needsReview omits approved entries but does not filter the summary.
func (s Service) Status(ctx context.Context, needsReview bool) (Result, error) {
	view, state, err := s.snapshotState(ctx)
	if err != nil {
		return Result{}, err
	}
	entries := entriesFor(view, state)
	withoutAddressedFeedback(entries)
	result := resultFromEntries("status", view, state, entries)
	result.Summary = countStates(entries)
	if !needsReview {
		result.Entries = entries
	}
	return result, nil
}

// File annotation does not require an upstream or linear commit history.
// Preserve stack validation when there are commit-review attempts to classify.
func (s Service) feedbackView(ctx context.Context, view Snapshot, state State) (Snapshot, error) {
	if len(state.Attempts) > 0 {
		return s.Repository.Snapshot(ctx)
	}
	head, err := s.Repository.Resolve(ctx, "HEAD")
	if err != nil {
		return Snapshot{}, err
	}
	target, err := s.Repository.git(ctx, "for-each-ref", "--format=%(upstream)", "refs/heads/"+view.Branch)
	if err != nil {
		return Snapshot{}, err
	}
	view.Head = head
	view.Target = target
	return view, nil
}

// recordApproval appends user-command approvals for the selected complete stacks.
func (s Service) recordApproval(ctx context.Context, view Snapshot, selected []Stack) (Result, error) {
	store := Store{Path: view.Path}
	err := store.Update(func(state *State) error {
		if err := approvalAllowed(selected, *state); err != nil {
			return err
		}
		if err := s.unchanged(ctx, view); err != nil {
			return err
		}
		for _, stack := range selected {
			a := newAttempt(stack, "approved")
			a.ApprovalSource = "user-command"
			state.Attempts = append(state.Attempts, a)
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	state, err := store.Load()
	if err != nil {
		return Result{}, err
	}
	result := makeResult("approve", view, state)
	result.Status = "approved"
	result.Entries = nil
	for _, stack := range selected {
		result.Entries = append(result.Entries, entryFor(state, stack))
	}
	return result, nil
}

func (s Service) selectRange(ctx context.Context, view Snapshot, first, last string) ([]Stack, error) {
	startSHA, err := s.Repository.Resolve(ctx, first)
	if err != nil {
		return nil, err
	}
	endSHA := startSHA
	if last != "" {
		endSHA, err = s.Repository.Resolve(ctx, last)
		if err != nil {
			return nil, err
		}
	}
	start, end := -1, -1
	for i, stack := range view.Stacks {
		if stack.Commits[0] == startSHA {
			start = i
		}
		if stack.Tip == endSHA {
			end = i
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("approval must start at an original commit in the feature range")
	}
	if end < start {
		return nil, fmt.Errorf("approval requires complete stacks in oldest-first order; include all attached fixups")
	}
	return view.Stacks[start : end+1], nil
}

func (s Service) snapshotState(ctx context.Context) (Snapshot, State, error) {
	view, err := s.Repository.Snapshot(ctx)
	if err != nil {
		return Snapshot{}, State{}, err
	}
	state, err := (Store{Path: view.Path}).Load()
	if err != nil {
		return Snapshot{}, State{}, err
	}
	return view, state, nil
}

func (s Service) unchanged(ctx context.Context, view Snapshot) error {
	fresh, err := s.Repository.Snapshot(ctx)
	if err != nil {
		return err
	}
	if fresh.Head != view.Head {
		return fmt.Errorf("git history changed; retry")
	}
	if fresh.Path != view.Path {
		return fmt.Errorf("git branch changed; retry")
	}
	if fresh.Base != view.Base {
		return fmt.Errorf("git review base changed; retry")
	}
	if fresh.Target != view.Target {
		return fmt.Errorf("git upstream changed; retry")
	}
	return nil
}

func activeAttempts(state State) []Attempt {
	active := []Attempt{}
	for _, a := range state.Attempts {
		if a.Status == "reviewing" {
			active = append(active, a)
		}
	}
	return active
}

func approvalAllowed(selected []Stack, state State) error {
	if len(activeAttempts(state)) > 0 {
		return fmt.Errorf("recover or replace the active review before approval")
	}
	for _, stack := range selected {
		a := state.latest(stack)
		if a == nil {
			continue
		}
		if a.Status == "feedback" {
			return fmt.Errorf("current stack %s has pending feedback; change the stack before approval", stack.Key)
		}
	}
	return nil
}

func currentAttempt(view Snapshot, state State, attempt Attempt) bool {
	for _, stack := range view.Stacks {
		if !attempt.matches(stack) {
			continue
		}
		latest := state.latest(stack)
		if latest == nil {
			return false
		}
		return latest.ID == attempt.ID
	}
	return false
}

func entryFor(state State, stack Stack) Entry {
	entry := Entry{Stack: stack, Status: "needs_review", Attempt: state.latest(stack), Previous: state.previous(stack)}
	if entry.Attempt != nil {
		entry.Status = entry.Attempt.Status
	}
	return entry
}

// withoutAddressedFeedback reports feedback on every stack except the newest as
// needs_review without its attempt. A newer stack shows the model responded.
func withoutAddressedFeedback(entries []Entry) {
	for i := range len(entries) - 1 {
		if entries[i].Status != "feedback" {
			continue
		}
		entries[i].Status = "needs_review"
		entries[i].Attempt = nil
	}
}

func entriesFor(view Snapshot, state State) []Entry {
	entries := make([]Entry, 0, len(view.Stacks))
	for _, stack := range view.Stacks {
		entries = append(entries, entryFor(state, stack))
	}
	return entries
}

func makeResult(command string, view Snapshot, state State) Result {
	return resultFromEntries(command, view, state, entriesFor(view, state))
}

func resultFromEntries(command string, view Snapshot, state State, entries []Entry) Result {
	result := Result{Version: 1, Command: command, Status: "pending", View: view, Entries: []Entry{}, Active: activeAttempts(state)}
	for _, entry := range entries {
		if entry.Status != "approved" {
			result.Entries = append(result.Entries, entry)
		}
	}
	if len(result.Entries) == 0 {
		if len(result.Active) == 0 {
			result.Status = "idle"
		}
	}
	return result
}

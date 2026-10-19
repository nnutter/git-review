package display

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/nnutter/git-review/internal/review"
)

func Render(output io.Writer, result review.Result, verbose bool) error {
	var text string
	var err error
	if result.Command == "feedback" {
		text, err = renderFeedbackResult(result)
	} else {
		text, err = RenderResult(result, verbose)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(colorprofile.NewWriter(output, os.Environ()), text)
	return err
}

func RenderResult(result review.Result, verbose bool) (string, error) {
	var out strings.Builder
	if showStatusDetails(result, verbose) {
		if err := renderDetails(&out, result.Entries); err != nil {
			return "", err
		}
	}
	if len(result.Entries) > 0 {
		fmt.Fprintln(&out, renderStackTable(result.Entries))
	}
	for _, attempt := range result.Active {
		fmt.Fprintf(&out, "Active attempt: %s  %s\n", safeText(attempt.ID), ShortStack(attempt.Stack))
	}
	if result.Command != "status" {
		if err := renderDetails(&out, result.Entries); err != nil {
			return "", err
		}
	}
	if message := resultMessage(result); message != "" {
		fmt.Fprintln(&out, message)
	}
	if result.Decision != nil {
		fmt.Fprintln(&out, RecoveryDescription(result.Decision))
		fmt.Fprintln(&out, "Choose git-review commits --recover, git-review commits --replace, or leave the attempt unchanged.")
	}
	return strings.TrimSpace(out.String()), nil
}

func renderStackTable(entries []review.Entry) string {
	rows := make([][]string, 0, len(entries))
	for _, entry := range entries {
		label := StatusLabel(entry.Status)
		if entry.Previous != nil {
			if entry.Status == "needs_review" {
				label += " (changed)"
			}
		}
		rows = append(rows, []string{label, ShortStack(entry.Stack), safeText(entry.Stack.Subject)})
	}
	t := table.New().Headers("STATUS", "COMMITS (inclusive)", "SUBJECT").Rows(rows...).Border(lipgloss.RoundedBorder()).StyleFunc(func(row, _ int) lipgloss.Style {
		style := lipgloss.NewStyle().Padding(0, 1)
		if row == table.HeaderRow {
			return style.Bold(true).Foreground(lipgloss.Color("12"))
		}
		return style
	})
	return t.Render()
}

func RecoveryDescription(decision *review.Decision) string {
	var out strings.Builder
	attempt := decision.Attempt
	fmt.Fprintf(&out, "Saved commits (inclusive): %s — %s\n", ShortStack(attempt.Stack), safeText(attempt.Stack.Subject))
	started := safeText(attempt.StartedAt)
	if started == "" {
		started = "unavailable"
	}
	fmt.Fprintf(&out, "Started: %s · Status: %s\n", started, StatusLabel(attempt.Status))
	var saved savedProgress
	if !savedProgressAvailable(attempt.Review, &saved) {
		fmt.Fprintln(&out, "Saved progress: unavailable")
	} else {
		fmt.Fprintf(&out, "Saved progress: %d/%d files reviewed · Comments: %d", *saved.Reviewed, *saved.Files, len(saved.Comments))
		if hasSessionNotes(saved.SessionNotes) {
			fmt.Fprint(&out, " · session notes saved")
		}
		fmt.Fprintln(&out)
	}
	if attempt.Error != "" {
		fmt.Fprintln(&out, "Last error: "+safeText(attempt.Error))
	}
	if current := decision.Replacement; current != nil {
		if scopeChanged(*current, attempt.Stack) {
			fmt.Fprintf(&out, "New review commits (inclusive): %s — %s\n", ShortStack(*current), safeText(current.Subject))
		}
	} else {
		fmt.Fprintln(&out, "No current replacement scope. Select one with git-review commits <commit>.")
	}
	fmt.Fprint(&out, "Replacement keeps the prior attempt in history. Tuicr may reuse its saved session.")
	return out.String()
}

type savedProgress struct {
	Reviewed     *int    `json:"reviewed"`
	Files        *int    `json:"files"`
	Comments     []any   `json:"comments"`
	SessionNotes *string `json:"sessionNotes"`
}

func savedProgressAvailable(data []byte, saved *savedProgress) bool {
	if len(data) == 0 {
		return false
	}
	if err := json.Unmarshal(data, saved); err != nil {
		return false
	}
	if saved.Reviewed == nil {
		return false
	}
	return saved.Files != nil
}

func showStatusDetails(result review.Result, verbose bool) bool {
	if !verbose {
		return false
	}
	return result.Command == "status"
}

func hasSessionNotes(notes *string) bool {
	if notes == nil {
		return false
	}
	return *notes != ""
}

func scopeChanged(current, captured review.Stack) bool {
	if current.Base != captured.Base {
		return true
	}
	return current.Tip != captured.Tip
}

func renderDetails(out *strings.Builder, entries []review.Entry) error {
	for _, entry := range entries {
		if entry.Attempt != nil {
			if err := renderAttempt(out, *entry.Attempt, ""); err != nil {
				return err
			}
		}
		if entry.Previous != nil {
			if err := renderAttempt(out, *entry.Previous, "Previous version (historical): "); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderFeedbackResult(result review.Result) (string, error) {
	var out strings.Builder
	fmt.Fprintln(&out, "Saved feedback: "+safeText(result.View.Branch))
	fmt.Fprintln(&out, "Target: "+safeText(result.View.Target))
	fmt.Fprintln(&out, "State: "+safeText(result.View.Path))
	for _, attempt := range result.Active {
		fmt.Fprintf(&out, "Active attempt: %s  %s\n", safeText(attempt.ID), ShortStack(attempt.Stack))
	}
	if err := renderFeedback(&out, result.Feedback); err != nil {
		return "", err
	}
	if result.Status == "idle" {
		fmt.Fprintln(&out, "No current saved feedback.")
	} else {
		fmt.Fprintln(&out, "Current saved feedback is available.")
	}
	return strings.TrimSpace(out.String()), nil
}

func renderFeedback(out *strings.Builder, records []review.FeedbackRecord) error {
	for _, record := range records {
		label := "Current feedback"
		if record.Historical {
			label = "Historical feedback"
		}
		fmt.Fprintf(out, "%s: %s  %s  %s\n", label, safeText(record.Attempt.ID), ShortStack(record.Attempt.Stack), safeText(record.Attempt.Stack.Subject))
		if record.Attempt.Response != "" {
			fmt.Fprintln(out, "  Response: "+safeText(record.Attempt.Response))
		}
		if err := renderAttempt(out, record.Attempt, ""); err != nil {
			return err
		}
	}
	return nil
}

func renderAttempt(out *strings.Builder, attempt review.Attempt, prefix string) error {
	if attempt.Error != "" {
		fmt.Fprintln(out, prefix+safeText(attempt.Error))
	}
	if len(attempt.Review) == 0 {
		return nil
	}
	var saved review.SavedReview
	if err := json.Unmarshal(attempt.Review, &saved); err != nil {
		return fmt.Errorf("invalid stored review %s: %w", attempt.ID, err)
	}
	fmt.Fprintf(out, "%sReview %s: %d/%d files reviewed\n", prefix, safeText(attempt.ID), saved.Reviewed, saved.Files)
	for _, comment := range saved.Comments {
		content, _ := comment["content"].(string)
		path, _ := comment["path"].(string)
		line, _ := comment["stored_line"].(string)
		location, _ := comment["location"].(string)
		label := location
		if path != "" {
			label = path
		}
		if line != "" {
			label += ":" + line
		}
		fmt.Fprintf(out, "  %s: %s\n", safeText(label), safeText(content))
	}
	if saved.SessionNotes != nil {
		if *saved.SessionNotes != "" {
			fmt.Fprintln(out, "  Session notes: "+safeText(*saved.SessionNotes))
		}
	}
	return nil
}

func resultMessage(result review.Result) string {
	switch result.Status {
	case "idle":
		return "No reviews pending."
	case "range_complete":
		return "No reviews pending in the selected range."
	case "no_waiting":
		return "No stacks waiting for review. Deferred reviews remain unapproved."
	case "approved":
		return "Approved. Approval applies only to the recorded commits."
	case "feedback":
		return "Feedback requires another review after the stack changes."
	case "incomplete":
		return "Review is incomplete, not approved."
	case "decision_required":
		return "A previous attempt requires an explicit recovery or replacement choice."
	case "cancelled":
		return "Review cancelled. The attempt remains unchanged."
	default:
		return ""
	}
}

func safeText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, ansi.Strip(value))
}

func ShortStack(stack review.Stack) string {
	first := stack.Commits[0]
	if len(first) > 8 {
		first = first[:8]
	}
	tip := stack.Tip
	if len(tip) > 8 {
		tip = tip[:8]
	}
	if len(stack.Commits) > 1 {
		return first + ".." + tip
	}
	return first
}

func StatusLabel(status string) string {
	switch status {
	case "needs_review":
		return "Needs review"
	case "approved":
		return "Approved"
	case "feedback":
		return "Feedback"
	case "reviewing":
		return "Reviewing"
	case "incomplete":
		return "Incomplete"
	default:
		return safeText(status)
	}
}

func RenderFiles(output io.Writer, result review.FilesResult) error {
	text, err := renderFilesResult(result)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(colorprofile.NewWriter(output, os.Environ()), text)
	return err
}

func renderFilesResult(result review.FilesResult) (string, error) {
	var out strings.Builder
	scope := result.Repo
	if result.AllFiles {
		fmt.Fprintf(&out, "Files review: all tracked files in %s\n", safeText(scope))
	} else if result.Path != "" {
		fmt.Fprintf(&out, "Files review: %s in %s\n", safeText(result.Path), safeText(scope))
	} else {
		fmt.Fprintf(&out, "Files review: %s\n", safeText(scope))
	}
	if result.Session != "" {
		fmt.Fprintf(&out, "Session: %s\n", safeText(result.Session))
	}
	if result.Review != nil {
		fmt.Fprintf(&out, "Review %s: %d/%d files reviewed\n", safeText(result.Review.ID), result.Review.Reviewed, result.Review.Files)
		for _, comment := range result.Review.Comments {
			content, _ := comment["content"].(string)
			path, _ := comment["path"].(string)
			line, _ := comment["stored_line"].(string)
			location, _ := comment["location"].(string)
			label := location
			if path != "" {
				label = path
			}
			if line != "" {
				label += ":" + line
			}
			fmt.Fprintf(&out, "  %s: %s\n", safeText(label), safeText(content))
		}
		if hasSessionNotes(result.Review.SessionNotes) {
			fmt.Fprintln(&out, "  Session notes: "+safeText(*result.Review.SessionNotes))
		}
	}
	if message := filesResultMessage(result); message != "" {
		fmt.Fprintln(&out, message)
	}
	return strings.TrimSpace(out.String()), nil
}

func filesResultMessage(result review.FilesResult) string {
	switch result.Status {
	case "approved":
		return "Approved. Address any follow-up as new commits."
	case "feedback":
		return "Feedback requires new commits, logically organized. Do not use fixup commits."
	case "incomplete":
		return "Review is incomplete, not approved."
	case "cancelled":
		return "Review cancelled. No file feedback was saved."
	default:
		return ""
	}
}

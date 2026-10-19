package commits

import (
	"context"
	"fmt"
	"io"
	"os"

	"charm.land/huh/v2"
	"github.com/charmbracelet/x/term"

	"github.com/nnutter/git-review/internal/display"
	"github.com/nnutter/git-review/internal/review"
)

func canPrompt(input io.Reader, output io.Writer) bool {
	in, ok := input.(*os.File)
	if !ok {
		return false
	}
	out, ok := output.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(in.Fd()) && term.IsTerminal(out.Fd())
}

func promptChoice(ctx context.Context, input io.Reader, output io.Writer, decision *review.Decision) (string, error) {
	if decision == nil {
		return "", fmt.Errorf("missing recovery decision")
	}
	choice := "cancel"
	selectField := huh.NewSelect[string]().Title("Resume a saved review?").Description(display.RecoveryDescription(decision)).Options(
		huh.NewOption("Recover — reopen tuicr with saved progress and comments", "recover"),
		huh.NewOption("Replace — create a new attempt (keep prior history)", "replace"),
		huh.NewOption("Cancel — keep the saved review unchanged", "cancel"),
	).Value(&choice)
	err := huh.NewForm(huh.NewGroup(selectField)).WithInput(input).WithOutput(output).RunWithContext(ctx)
	return choice, err
}

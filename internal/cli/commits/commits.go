// Package commits reviews feature-branch commit stacks with tuicr.
package commits

import (
	"context"
	"errors"
	"io"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/nnutter/git-review/internal/display"
	"github.com/nnutter/git-review/internal/review"
)

// Command houses flag values and positional arguments for the commits review.
type Command struct {
	Service  review.Service
	Input    io.Reader
	Output   io.Writer
	Terminal io.Writer
	Stack    string
	Start    string
	End      string
	Session  string
	Recover  bool
	Replace  bool
}

// New builds the commits sub-command. Callers install it onto the parent
// with Install.
func New(svc review.Service, in io.Reader, out, term io.Writer) *cobra.Command {
	c := &Command{Service: svc, Input: in, Output: out, Terminal: term}
	cmd := &cobra.Command{Use: "commits [<commit_stack>]", Aliases: []string{"run", "challenge"}, Short: "Review eligible stacks, or select one stack", Long: "Run tuicr in a stable detached worktree. Without a stack argument, review each\neligible stack in oldest-first order. A commit reference selects one stack. After\napproval or feedback, continue through the selected stacks. With --start or --end,\nlimit selection to the bounded range. Each eligible stack opens once per\ninvocation. Incomplete reviews, cancellation, required decisions, and errors stop\nthe command. Existing interrupted or feedback attempts require an explicit\nrecovery or replacement choice.", Args: cobra.MaximumNArgs(1), RunE: c.RunE}
	cmd.Flags().StringVar(&c.Start, "start", "", "Review from the stack containing this commit (inclusive)")
	cmd.Flags().StringVar(&c.End, "end", "", "Review through the stack containing this commit (inclusive)")
	cmd.Flags().BoolVar(&c.Recover, "recover", false, "Reopen the captured attempt in tuicr, preserving saved progress")
	cmd.Flags().BoolVar(&c.Replace, "replace", false, "Launch a new attempt, retaining previous review history")
	cmd.Flags().StringVar(&c.Session, "session", "", "Select an exact saved session ID, slug, or indexed session path")
	cmd.MarkFlagsMutuallyExclusive("recover", "replace")
	return cmd
}

// Install adds the commits sub-command to parent.
func Install(parent *cobra.Command, svc review.Service, in io.Reader, out, term io.Writer) {
	parent.AddCommand(New(svc, in, out, term))
}

// RunE runs the commits review workflow.
func (c *Command) RunE(cmd *cobra.Command, args []string) error {
	options := review.ReviewOptions{Session: c.Session, Start: c.Start, End: c.End}
	if len(args) > 0 {
		options.Stack = args[0]
		c.Stack = args[0]
	}
	if c.Recover {
		options.Choice = "recover"
	}
	if c.Replace {
		options.Choice = "replace"
	}
	return c.runReview(cmd.Context(), options, cmd)
}

func (c *Command) runReview(ctx context.Context, options review.ReviewOptions, cmd *cobra.Command) error {
	asJSON := false
	if flag, err := cmd.Root().PersistentFlags().GetBool("json"); err == nil {
		asJSON = flag
	}
	reader, err := review.DefaultSavedReader()
	if err != nil {
		return err
	}
	runner := review.Reviewer{Service: c.Service, Reader: reader, Input: c.Input, Terminal: c.Terminal}
	if !asJSON && canPrompt(c.Input, c.Terminal) {
		runner.Choose = func(ctx context.Context, decision *review.Decision) (string, error) {
			choice, err := promptChoice(ctx, c.Input, c.Terminal, decision)
			if errors.Is(err, huh.ErrUserAborted) {
				return "cancel", nil
			}
			return choice, err
		}
	}
	result, err := runner.Review(ctx, options)
	if err != nil {
		return err
	}
	return display.Write(c.Output, asJSON, result, false)
}

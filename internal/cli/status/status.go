// Package status shows feature stacks and live review activity.
package status

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/nnutter/git-review/internal/display"
	"github.com/nnutter/git-review/internal/review"
)

// Command houses flag values for the status review.
type Command struct {
	Service     review.Service
	Output      io.Writer
	NeedsReview bool
	IsReviewing bool
	Verbose     bool
}

// New builds the status sub-command. Callers install it onto the parent
// with Install.
func New(svc review.Service, out io.Writer) *cobra.Command {
	c := &Command{Service: svc, Output: out}
	cmd := &cobra.Command{Use: "status", Short: "Show all feature stacks and their review states", Args: cobra.NoArgs, RunE: c.RunE}
	cmd.Flags().BoolVarP(&c.Verbose, "verbose", "v", false, "Show saved review details before the table")
	cmd.Flags().BoolVar(&c.NeedsReview, "needs-review", false, "Show unresolved stacks and active review attempts")
	cmd.Flags().BoolVar(&c.IsReviewing, "is-reviewing", false, "Check whether a review is running for the current branch")
	cmd.MarkFlagsMutuallyExclusive("needs-review", "is-reviewing")
	cmd.MarkFlagsMutuallyExclusive("verbose", "is-reviewing")
	return cmd
}

// Install adds the status sub-command to parent.
func Install(parent *cobra.Command, svc review.Service, out io.Writer) {
	parent.AddCommand(New(svc, out))
}

// RunE runs the status workflow.
func (c *Command) RunE(cmd *cobra.Command, _ []string) error {
	asJSON := false
	if flag, err := cmd.Root().PersistentFlags().GetBool("json"); err == nil {
		asJSON = flag
	}
	if c.IsReviewing {
		reviewing, err := c.Service.ReviewRunning(cmd.Context())
		if err != nil {
			return err
		}
		if asJSON {
			return display.WriteJSON(c.Output, struct {
				Version     int    `json:"version"`
				Command     string `json:"command"`
				IsReviewing bool   `json:"is_reviewing"`
			}{Version: 1, Command: "status", IsReviewing: reviewing})
		}
		_, err = fmt.Fprintf(c.Output, "Is reviewing: %t\n", reviewing)
		return err
	}
	result, err := c.Service.Status(cmd.Context(), c.NeedsReview)
	if err != nil {
		return err
	}
	return display.Write(c.Output, asJSON, result, c.Verbose)
}

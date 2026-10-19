// Package approve records explicit user approval of complete stacks.
package approve

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/nnutter/git-review/internal/display"
	"github.com/nnutter/git-review/internal/review"
)

// Command houses positional arguments for the approval review.
type Command struct {
	Service review.Service
	Output  io.Writer
	First   string
	Last    string
}

// New builds the approve sub-command. Callers install it onto the parent
// with Install.
func New(svc review.Service, out io.Writer) *cobra.Command {
	c := &Command{Service: svc, Output: out}
	cmd := &cobra.Command{Use: "approve <start_commit> [<end_commit>]", Aliases: []string{"confirm"}, Short: "Record explicit user approval of complete stacks", Long: "Record user approval without launching tuicr. Endpoints are inclusive and\nmust include complete stacks, including all attached feedback commits.", Args: cobra.RangeArgs(1, 2), RunE: c.RunE}
	return cmd
}

// Install adds the approve sub-command to parent.
func Install(parent *cobra.Command, svc review.Service, out io.Writer) {
	parent.AddCommand(New(svc, out))
}

// RunE runs the approval workflow.
func (c *Command) RunE(cmd *cobra.Command, args []string) error {
	asJSON := false
	if flag, err := cmd.Root().PersistentFlags().GetBool("json"); err == nil {
		asJSON = flag
	}
	c.First = args[0]
	if len(args) == 2 {
		c.Last = args[1]
	}
	result, err := c.Service.Approve(cmd.Context(), c.First, c.Last)
	if err != nil {
		return err
	}
	return display.Write(c.Output, asJSON, result, false)
}

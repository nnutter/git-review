// Package approve records explicit user approval of complete stacks.
package approve

import (
	"errors"
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
	All     bool
}

// New builds the approve sub-command. Callers install it onto the parent
// with Install.
func New(svc review.Service, out io.Writer) *cobra.Command {
	c := &Command{Service: svc, Output: out}
	cmd := &cobra.Command{Use: "approve [<start_commit>] [<end_commit>]", Aliases: []string{"confirm"}, Short: "Record explicit user approval of complete stacks", Long: "Record user approval without launching tuicr. Endpoints are inclusive and\nmust include complete stacks, including all attached feedback commits.\nUse --all to approve every stack in the feature range.", Args: cobra.RangeArgs(0, 2), RunE: c.RunE}
	cmd.Flags().BoolVar(&c.All, "all", false, "Approve every stack in the feature range")
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
	var result review.Result
	var err error
	if c.All {
		if len(args) > 0 {
			return errors.New("--all cannot be combined with commit endpoints")
		}
		result, err = c.Service.ApproveAll(cmd.Context())
	} else {
		if len(args) == 0 {
			return errors.New("approve requires <start_commit> or --all")
		}
		c.First = args[0]
		if len(args) == 2 {
			c.Last = args[1]
		}
		result, err = c.Service.Approve(cmd.Context(), c.First, c.Last)
	}
	if err != nil {
		return err
	}
	return display.Write(c.Output, asJSON, result, false)
}

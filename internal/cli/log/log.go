// Package log shows commits after the upstream in oldest-first order.
package log

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/nnutter/git-review/internal/review"
)

// Command houses flag values for the log review.
type Command struct {
	Service        review.Service
	Input          io.Reader
	Output         io.Writer
	Terminal       io.Writer
	Oneline        bool
	Patch          bool
	IgnoreAllSpace bool
	Stat           bool
	Difft          bool
	Hunk           bool
}

// New builds the log sub-command. Callers install it onto the parent
// with Install.
func New(svc review.Service, in io.Reader, out, term io.Writer) *cobra.Command {
	c := &Command{Service: svc, Input: in, Output: out, Terminal: term}
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show commits after the upstream in oldest-first order",
		Long:  "Run git log @{u}..HEAD --reverse with optional one-line summaries, patches, diff statistics, and whitespace filtering.\nUse --difft for Difftastic or --hunk for hunk pager. Both imply --patch.\nOutput uses Git's format and pager, not git-review's review state.",
		Args:  cobra.NoArgs,
		RunE:  c.RunE,
	}
	cmd.Flags().BoolVar(&c.Oneline, "oneline", false, "Show each commit as an abbreviated hash and subject")
	cmd.Flags().BoolVarP(&c.Patch, "patch", "p", false, "Show the patch for each commit")
	cmd.Flags().BoolVarP(&c.IgnoreAllSpace, "ignore-all-space", "w", false, "Ignore whitespace changes in patches")
	cmd.Flags().BoolVar(&c.Stat, "stat", false, "Show diff statistics for each commit")
	cmd.Flags().BoolVar(&c.Difft, "difft", false, "Show patches with difft (requires difft on PATH)")
	cmd.Flags().BoolVar(&c.Hunk, "hunk", false, "Show patches with hunk pager (requires hunk on PATH)")
	return cmd
}

// Install adds the log sub-command to parent.
func Install(parent *cobra.Command, svc review.Service, in io.Reader, out, term io.Writer) {
	parent.AddCommand(New(svc, in, out, term))
}

// RunE runs the log workflow.
func (c *Command) RunE(cmd *cobra.Command, _ []string) error {
	asJSON := false
	if flag, err := cmd.Root().PersistentFlags().GetBool("json"); err == nil {
		asJSON = flag
	}
	if asJSON {
		return fmt.Errorf("log does not support --json")
	}
	git := exec.CommandContext(cmd.Context(), "git", c.gitArgs()...)
	git.Dir = c.Service.Repository.Dir
	git.Stdin = c.Input
	git.Stdout = c.Output
	git.Stderr = c.Terminal
	if err := git.Run(); err != nil {
		return fmt.Errorf("git log: %w", err)
	}
	return nil
}

func (c *Command) gitArgs() []string {
	var args []string
	if c.Hunk {
		args = append(args, "-c", "core.pager=hunk pager")
	}
	if c.Difft {
		args = append(args, "-c", "diff.external=difft")
	}
	args = append(args, "log", "@{u}..HEAD", "--reverse")
	if c.Oneline {
		args = append(args, "--oneline")
	}
	if c.Difft {
		args = append(args, "--ext-diff")
	}
	if wantsPatch(c.Patch, c.Difft, c.Hunk) {
		args = append(args, "-p")
	}
	if c.IgnoreAllSpace {
		args = append(args, "-w")
	}
	if c.Stat {
		args = append(args, "--stat")
	}
	return args
}

func wantsPatch(patch, difft, hunk bool) bool {
	if patch {
		return true
	}
	if difft {
		return true
	}
	return hunk
}

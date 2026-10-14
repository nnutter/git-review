package cli

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
)

func (a *application) logCommand() *cobra.Command {
	var patch, ignoreAllSpace, stat, difft bool
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show commits after the upstream in oldest-first order",
		Long:  "Run git log @{u}..HEAD --reverse with optional patches, diff statistics, and whitespace filtering.\nOutput uses Git's format and pager, not git-review's review state.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.json {
				return fmt.Errorf("log does not support --json")
			}
			var args []string
			if difft {
				args = append(args, "-c", "diff.external=difft")
			}
			args = append(args, "log", "@{u}..HEAD", "--reverse")
			if difft {
				args = append(args, "--ext-diff")
			}
			if patch || difft {
				args = append(args, "-p")
			}
			if ignoreAllSpace {
				args = append(args, "-w")
			}
			if stat {
				args = append(args, "--stat")
			}
			git := exec.CommandContext(cmd.Context(), "git", args...)
			git.Dir = a.service.Repository.Dir
			git.Stdin = a.input
			git.Stdout = a.output
			git.Stderr = a.terminal
			if err := git.Run(); err != nil {
				return fmt.Errorf("git log: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&patch, "patch", "p", false, "Show the patch for each commit")
	cmd.Flags().BoolVarP(&ignoreAllSpace, "ignore-all-space", "w", false, "Ignore whitespace changes in patches")
	cmd.Flags().BoolVar(&stat, "stat", false, "Show diff statistics for each commit")
	cmd.Flags().BoolVar(&difft, "difft", false, "Show patches with difft (requires difft on PATH)")
	return cmd
}

// Package cache exposes disposable patch cache maintenance commands.
package cache

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/nnutter/git-review/internal/display"
	"github.com/nnutter/git-review/internal/review"
)

// Install adds cache maintenance commands to parent.
func Install(parent *cobra.Command, repository review.Repository, out io.Writer) {
	cmd := &cobra.Command{Use: "cache", Short: "Maintain the disposable patch fingerprint cache"}
	warm := &cobra.Command{
		Use: "warm [<commit>]", Short: "Warm the current feature stack containing a commit (default HEAD)",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := ""
			if len(args) > 0 {
				ref = args[0]
			}
			if err := repository.WarmCache(cmd.Context(), ref); err != nil {
				return err
			}
			return writeResult(cmd, out)
		},
	}
	prune := &cobra.Command{
		Use: "prune", Short: "Prune old patch fingerprints and enforce cache limits",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := repository.PruneCache(cmd.Context()); err != nil {
				return err
			}
			return writeResult(cmd, out)
		},
	}
	cmd.AddCommand(warm, prune)
	parent.AddCommand(cmd)
}

func writeResult(cmd *cobra.Command, out io.Writer) error {
	asJSON, _ := cmd.Root().PersistentFlags().GetBool("json")
	if !asJSON {
		return nil
	}
	return display.WriteJSON(out, struct {
		Version int    `json:"version"`
		Command string `json:"command"`
		Status  string `json:"status"`
	}{Version: 1, Command: cmd.Name(), Status: "ok"})
}

// Package files reviews working-tree files with tuicr.
package files

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/nnutter/git-review/internal/display"
	"github.com/nnutter/git-review/internal/review"
)

// Command houses flag values and positional arguments for the files review.
type Command struct {
	Service   review.Service
	Input     io.Reader
	Output    io.Writer
	Terminal  io.Writer
	Path      string
	All       bool
	Untracked bool
	Session   string
}

// New builds the files sub-command. Callers install it onto the parent
// with Install.
func New(svc review.Service, in io.Reader, out, term io.Writer) *cobra.Command {
	c := &Command{Service: svc, Input: in, Output: out, Terminal: term}
	cmd := &cobra.Command{Use: "files [<path>]", Short: "Review branch changes or annotate files with tuicr", Long: `Without a path or scope flag, review the committed branch diff from the
merge-base with upstream to HEAD. This requires a configured Git upstream.
With --all, annotate every tracked file. With a path, annotate that file or
directory. With --untracked, annotate files in the current directory,
including untracked files. These explicit scopes do not require an upstream.
Address file feedback as new commits, logically organized, not fixup commits.`, Args: cobra.MaximumNArgs(1), RunE: c.RunE}
	cmd.Flags().BoolVar(&c.All, "all", false, "Review every tracked file in the repository")
	cmd.Flags().BoolVar(&c.Untracked, "untracked", false, "Review untracked files in the current directory (defaults path to .)")
	cmd.Flags().StringVar(&c.Session, "session", "", "Select an exact saved session ID, slug, or indexed session path")
	return cmd
}

// Install adds the files sub-command to parent.
func Install(parent *cobra.Command, svc review.Service, in io.Reader, out, term io.Writer) {
	parent.AddCommand(New(svc, in, out, term))
}

// RunE runs the files review workflow.
func (c *Command) RunE(cmd *cobra.Command, args []string) error {
	options := review.FilesOptions{Session: c.Session, All: c.All, Untracked: c.Untracked}
	if len(args) > 0 {
		options.Path = args[0]
		c.Path = args[0]
	}
	asJSON := false
	if flag, err := cmd.Root().PersistentFlags().GetBool("json"); err == nil {
		asJSON = flag
	}
	reader, err := review.DefaultSavedReader()
	if err != nil {
		return err
	}
	reviewer := review.FileReviewer{Service: c.Service, Reader: reader, Input: c.Input, Terminal: c.Terminal}
	result, err := reviewer.Review(cmd.Context(), options)
	if err != nil {
		return err
	}
	return display.WriteFiles(c.Output, asJSON, result)
}

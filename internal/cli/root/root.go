// Package root assembles the git-review command tree.
package root

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"

	charmlog "charm.land/log/v2"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"github.com/nnutter/git-review/internal/cli/approve"
	"github.com/nnutter/git-review/internal/cli/cache"
	"github.com/nnutter/git-review/internal/cli/commitmsg"
	"github.com/nnutter/git-review/internal/cli/commits"
	"github.com/nnutter/git-review/internal/cli/feedback"
	"github.com/nnutter/git-review/internal/cli/files"
	"github.com/nnutter/git-review/internal/cli/log"
	"github.com/nnutter/git-review/internal/cli/status"
	"github.com/nnutter/git-review/internal/display"
	"github.com/nnutter/git-review/internal/review"
)

// Command houses setup state for the root command.
type Command struct {
	Service     review.Service
	Input       io.Reader
	Output      io.Writer
	Terminal    io.Writer
	JSON        bool
	Debug       bool
	outputError error
}

// New builds the root command and installs every sub-command onto it.
func New(svc review.Service, in io.Reader, out, term io.Writer, jsonDef bool) *cobra.Command {
	c := &Command{Service: svc, Input: in, Output: out, Terminal: term, JSON: jsonDef}
	return c.build()
}

// Install adds the assembled tree to parent. It exists so every command
// package shares the same constructor shape; the binary uses Execute.
func Install(parent *cobra.Command, svc review.Service, in io.Reader, out, term io.Writer, jsonDef bool) {
	parent.AddCommand(New(svc, in, out, term, jsonDef))
}

// Execute runs a command with explicit process streams and checkout location.
func Execute(ctx context.Context, args []string, dir string, input io.Reader, output, terminal io.Writer, version string) error {
	svc := review.Service{Repository: review.Repository{Dir: dir}}
	c := &Command{Service: svc, Input: input, Output: output, Terminal: terminal, JSON: display.WantsJSON(args)}
	root := c.build()
	root.SetArgs(args)
	root.SetIn(input)
	root.SetOut(output)
	root.SetErr(terminal)
	err := fang.Execute(ctx, root, fang.WithVersion(version), fang.WithoutManpage(), fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM), fang.WithErrorHandler(func(w io.Writer, styles fang.Styles, err error) {
		if c.JSON {
			failed, _, _ := root.Find(args)
			if failed == nil {
				failed = root
			}
			c.outputError = display.WriteJSON(output, display.ErrorResult{Version: 1, Command: failed.Name(), Status: "error", Error: err.Error()})
			return
		}
		fang.DefaultErrorHandler(w, styles, err)
	}))
	return errors.Join(err, c.outputError)
}

func (c *Command) build() *cobra.Command {
	writer := c.Terminal
	if writer == nil {
		writer = io.Discard
	}
	logger := charmlog.New(writer)
	c.Service.Repository = review.NewRepository(c.Service.Repository.Dir, logger)
	root := &cobra.Command{Use: "git-review", Short: "Review feature-branch commit stacks with tuicr", Long: "git-review tracks review of exact commit stacks against the configured Git upstream.\nApproval never transfers to rewritten commits or added fixups."}
	root.PersistentPreRun = func(_ *cobra.Command, _ []string) {
		if c.Debug {
			logger.SetLevel(charmlog.DebugLevel)
		}
	}
	root.PersistentFlags().BoolVar(&c.Debug, "debug", false, "Write diagnostic logs to stderr")
	root.PersistentFlags().BoolVar(&c.JSON, "json", c.JSON, "Emit a versioned JSON result without interactive prompts")
	root.AddCommand(commitmsg.New(c.Service.Repository.Dir))
	approve.Install(root, c.Service, c.Output)
	cache.Install(root, c.Service.Repository, c.Output)
	commits.Install(root, c.Service, c.Input, c.Output, c.Terminal)
	feedback.Install(root, c.Service, c.Output)
	files.Install(root, c.Service, c.Input, c.Output, c.Terminal)
	log.Install(root, c.Service, c.Input, c.Output, c.Terminal)
	status.Install(root, c.Service, c.Output)
	return root
}

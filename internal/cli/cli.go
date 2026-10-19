// Package cli exposes git-review's review workflow through Cobra and Fang.
package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"github.com/nnutter/git-review/internal/cli/approve"
	"github.com/nnutter/git-review/internal/cli/commits"
	"github.com/nnutter/git-review/internal/cli/feedback"
	"github.com/nnutter/git-review/internal/cli/files"
	"github.com/nnutter/git-review/internal/cli/log"
	"github.com/nnutter/git-review/internal/cli/status"
	"github.com/nnutter/git-review/internal/display"
	"github.com/nnutter/git-review/internal/review"
)

// Execute runs a command with explicit process streams and checkout location.
func Execute(ctx context.Context, args []string, dir string, input io.Reader, output, terminal io.Writer, version string) error {
	app := application{json: display.WantsJSON(args), service: review.Service{Repository: review.Repository{Dir: dir}}, input: input, output: output, terminal: terminal}
	root := app.command()
	root.SetArgs(args)
	root.SetIn(input)
	root.SetOut(output)
	root.SetErr(terminal)
	err := fang.Execute(ctx, root, fang.WithVersion(version), fang.WithoutManpage(), fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM), fang.WithErrorHandler(func(w io.Writer, styles fang.Styles, err error) {
		if app.json {
			failed, _, _ := root.Find(args)
			if failed == nil {
				failed = root
			}
			app.outputError = display.WriteJSON(output, display.ErrorResult{Version: 1, Command: failed.Name(), Status: "error", Error: err.Error()})
			return
		}
		fang.DefaultErrorHandler(w, styles, err)
	}))
	return errors.Join(err, app.outputError)
}

type application struct {
	json        bool
	service     review.Service
	input       io.Reader
	output      io.Writer
	terminal    io.Writer
	outputError error
}

func (a *application) command() *cobra.Command {
	root := &cobra.Command{Use: "git-review", Short: "Review feature-branch commit stacks with tuicr", Long: "git-review tracks review of exact commit stacks against the configured Git upstream.\nApproval never transfers to rewritten commits or added fixups."}
	root.PersistentFlags().BoolVar(&a.json, "json", a.json, "Emit a versioned JSON result without interactive prompts")
	commits.Install(root, a.service, a.input, a.output, a.terminal)
	feedback.Install(root, a.service, a.output)
	approve.Install(root, a.service, a.output)
	files.Install(root, a.service, a.input, a.output, a.terminal)
	log.Install(root, a.service, a.input, a.output, a.terminal)
	status.Install(root, a.service, a.output)
	return root
}

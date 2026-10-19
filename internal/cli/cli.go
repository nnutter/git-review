// Package cli exposes git-review's review workflow through Cobra and Fang.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"charm.land/huh/v2"
	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

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
	verbose     bool
	service     review.Service
	input       io.Reader
	output      io.Writer
	terminal    io.Writer
	outputError error
}

func (a *application) command() *cobra.Command {
	root := &cobra.Command{Use: "git-review", Short: "Review feature-branch commit stacks with tuicr", Long: "git-review tracks review of exact commit stacks against the configured Git upstream.\nApproval never transfers to rewritten commits or added fixups."}
	root.PersistentFlags().BoolVar(&a.json, "json", a.json, "Emit a versioned JSON result without interactive prompts")
	feedback := &cobra.Command{Use: "feedback", Short: "Read current and historical saved feedback without launching tuicr", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, err := a.service.Feedback(cmd.Context())
		if err != nil {
			return err
		}
		return a.write(result)
	}}
	approve := &cobra.Command{Use: "approve <start_commit> [<end_commit>]", Aliases: []string{"confirm"}, Short: "Record explicit user approval of complete stacks", Long: "Record user approval without launching tuicr. Endpoints are inclusive and\nmust include complete stacks, including all attached feedback commits.", Args: cobra.RangeArgs(1, 2), RunE: func(cmd *cobra.Command, args []string) error {
		last := ""
		if len(args) == 2 {
			last = args[1]
		}
		result, err := a.service.Approve(cmd.Context(), args[0], last)
		if err != nil {
			return err
		}
		return a.write(result)
	}}
	var onlyNeedsReview, isReviewing bool
	status := &cobra.Command{Use: "status", Short: "Show all feature stacks and their review states", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if isReviewing {
			reviewing, err := a.service.ReviewRunning(cmd.Context())
			if err != nil {
				return err
			}
			if a.json {
				return display.WriteJSON(a.output, struct {
					Version     int    `json:"version"`
					Command     string `json:"command"`
					IsReviewing bool   `json:"is_reviewing"`
				}{Version: 1, Command: "status", IsReviewing: reviewing})
			}
			_, err = fmt.Fprintf(a.output, "Is reviewing: %t\n", reviewing)
			return err
		}
		result, err := a.service.Status(cmd.Context(), onlyNeedsReview)
		if err != nil {
			return err
		}
		return a.write(result)
	}}
	status.Flags().BoolVarP(&a.verbose, "verbose", "v", false, "Show saved review details before the table")
	status.Flags().BoolVar(&onlyNeedsReview, "needs-review", false, "Show unresolved stacks and active review attempts")
	status.Flags().BoolVar(&isReviewing, "is-reviewing", false, "Check whether a review is running for the current branch")
	status.MarkFlagsMutuallyExclusive("needs-review", "is-reviewing")
	status.MarkFlagsMutuallyExclusive("verbose", "is-reviewing")
	root.AddCommand(feedback, approve, status, a.runCommand(), a.filesCommand(), a.logCommand())
	return root
}

func (a *application) filesCommand() *cobra.Command {
	var untracked bool
	var session string
	cmd := &cobra.Command{Use: "files [<path>]", Short: "Review working-tree files with tuicr", Long: "Review working-tree files with tuicr. Without a path, review every tracked\nfile with tuicr tui --all-files. With a path, review that file or directory\nwith tuicr tui --file. With --untracked and no path, review untracked files\nin the current directory with tuicr tui --file . Address file feedback as new commits,\nlogically organized, not fixup commits.", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		options := review.FilesOptions{Session: session, Untracked: untracked}
		if len(args) > 0 {
			options.Path = args[0]
		}
		return a.runFiles(cmd.Context(), options)
	}}
	cmd.Flags().BoolVar(&untracked, "untracked", false, "Review untracked files in the current directory (defaults path to .)")
	cmd.Flags().StringVar(&session, "session", "", "Select an exact saved session ID, slug, or indexed session path")
	return cmd
}

func (a *application) logCommand() *cobra.Command {
	var patch, ignoreAllSpace, stat, difft, hunk bool
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show commits after the upstream in oldest-first order",
		Long:  "Run git log @{u}..HEAD --reverse with optional patches, diff statistics, and whitespace filtering.\nUse --difft for Difftastic or --hunk for hunk pager. Both imply --patch.\nOutput uses Git's format and pager, not git-review's review state.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.json {
				return fmt.Errorf("log does not support --json")
			}
			var args []string
			if hunk {
				args = append(args, "-c", "core.pager=hunk pager")
			}
			if difft {
				args = append(args, "-c", "diff.external=difft")
			}
			args = append(args, "log", "@{u}..HEAD", "--reverse")
			if difft {
				args = append(args, "--ext-diff")
			}
			if wantsPatch(patch, difft, hunk) {
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
	cmd.Flags().BoolVar(&hunk, "hunk", false, "Show patches with hunk pager (requires hunk on PATH)")
	return cmd
}

func (a *application) runCommand() *cobra.Command {
	var recoverReview, replaceReview bool
	var session, start, end string
	cmd := &cobra.Command{Use: "commits [<commit_stack>]", Aliases: []string{"run", "challenge"}, Short: "Review eligible stacks, or select one stack", Long: "Run tuicr in a stable detached worktree. Without a stack argument, review each\neligible stack in oldest-first order. A commit reference selects one stack. After\napproval or feedback, continue through the selected stacks. With --start or --end,\nlimit selection to the bounded range. Each eligible stack opens once per\ninvocation. Incomplete reviews, cancellation, required decisions, and errors stop\nthe command. Existing interrupted or feedback attempts require an explicit\nrecovery or replacement choice.", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		options := review.ReviewOptions{Session: session, Start: start, End: end}
		if len(args) > 0 {
			options.Stack = args[0]
		}
		if recoverReview {
			options.Choice = "recover"
		}
		if replaceReview {
			options.Choice = "replace"
		}
		return a.runReview(cmd.Context(), options)
	}}
	cmd.Flags().StringVar(&start, "start", "", "Review from the stack containing this commit (inclusive)")
	cmd.Flags().StringVar(&end, "end", "", "Review through the stack containing this commit (inclusive)")
	cmd.Flags().BoolVar(&recoverReview, "recover", false, "Reopen the captured attempt in tuicr, preserving saved progress")
	cmd.Flags().BoolVar(&replaceReview, "replace", false, "Launch a new attempt, retaining previous review history")
	cmd.Flags().StringVar(&session, "session", "", "Select an exact saved session ID, slug, or indexed session path")
	cmd.MarkFlagsMutuallyExclusive("recover", "replace")
	return cmd
}

func (a *application) runFiles(ctx context.Context, options review.FilesOptions) error {
	reader, err := review.DefaultSavedReader()
	if err != nil {
		return err
	}
	reviewer := review.FileReviewer{Service: a.service, Reader: reader, Input: a.input, Terminal: a.terminal}
	result, err := reviewer.Review(ctx, options)
	if err != nil {
		return err
	}
	return a.writeFiles(result)
}

func (a *application) runReview(ctx context.Context, options review.ReviewOptions) error {
	reader, err := review.DefaultSavedReader()
	if err != nil {
		return err
	}
	runner := review.Reviewer{Service: a.service, Reader: reader, Input: a.input, Terminal: a.terminal}
	if !a.json && canPrompt(a.input, a.terminal) {
		runner.Choose = func(ctx context.Context, decision *review.Decision) (string, error) {
			choice, err := promptChoice(ctx, a.input, a.terminal, decision)
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
	return a.write(result)
}

func (a *application) write(result review.Result) error {
	if a.json {
		return display.WriteJSON(a.output, result)
	}
	return display.Render(a.output, result, a.verbose)
}

func (a *application) writeFiles(result review.FilesResult) error {
	if a.json {
		return display.WriteJSON(a.output, result)
	}
	return display.RenderFiles(a.output, result)
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

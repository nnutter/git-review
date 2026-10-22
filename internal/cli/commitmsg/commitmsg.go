// Package commitmsg provides a Gerrit-compatible commit-msg hook.
package commitmsg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// New builds the commit-msg subcommand. Successful hooks produce no output.
func New(dir string) *cobra.Command {
	return &cobra.Command{
		Use:   "commit-msg <message-file>",
		Short: "Add a Gerrit-compatible Change-Id to a commit message",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return (hook{ctx: cmd.Context(), dir: dir}).run(args[0])
		},
	}
}

type hook struct {
	ctx context.Context
	dir string
}

func (h hook) changeID(message []byte) (string, error) {
	ref, err := h.git(nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		ref, err = h.git(nil, "hash-object", "-t", "tree", "--stdin")
		if err != nil {
			return "", err
		}
	}
	ident, err := h.git(nil, "var", "GIT_COMMITTER_IDENT")
	if err != nil {
		return "", err
	}
	seed := append(ident, ref...)
	seed = append(seed, message...)
	hash, err := h.git(seed, "hash-object", "--stdin")
	if err != nil {
		return "", err
	}
	return "I" + strings.TrimSpace(string(hash)), nil
}

func (h hook) config(args ...string) (string, error) {
	out, err := h.git(nil, append([]string{"config"}, args...)...)
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		if exit.ExitCode() == 1 {
			return "", nil // An absent configuration key is not an error.
		}
	}
	return strings.TrimSuffix(string(out), "\n"), err
}

func (h hook) git(input []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(h.ctx, "git", args...)
	cmd.Dir = h.dir
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func (h hook) run(path string) error {
	if !filepath.IsAbs(path) {
		path = filepath.Join(h.dir, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular message file: %s", path)
	}
	create, err := h.config("--bool", "--get", "gerrit.createChangeId")
	if err != nil {
		return err
	}
	if create == "false" {
		return nil
	}
	message, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	id, err := h.changeID(message)
	if err != nil {
		return err
	}
	return h.updateMessage(path, message, id, info.Mode())
}

func (h hook) updateMessage(path string, message []byte, id string, mode os.FileMode) error {
	clean, err := h.git(message, "stripspace", "--strip-comments")
	if err != nil {
		return err
	}
	if len(clean) == 0 {
		return fmt.Errorf("file is empty: %s", path)
	}
	url, err := h.config("--get", "gerrit.reviewUrl")
	if err != nil {
		return err
	}
	args := []string{"-c", "trailer.ifexists=doNothing", "interpret-trailers", "--trailer", "Change-Id: " + id}
	if url != "" {
		trailers, err := h.git(message, "interpret-trailers", "--parse")
		if err != nil {
			return err
		}
		if reviewLink.Match(trailers) {
			// The reference hook retains stripspace's output on this path.
			return replaceMessage(path, clean, mode)
		}
		args = []string{"interpret-trailers", "--trailer", "Link: " + strings.TrimSuffix(url, "/") + "/id/" + id}
	}
	updated, err := h.git(message, args...)
	if err != nil {
		return err
	}
	return replaceMessage(path, updated, mode)
}

func replaceMessage(path string, message []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".commit-msg-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }() // Best-effort cleanup.
	_, writeErr := file.Write(message)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	if err := os.Chmod(file.Name(), mode.Perm()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

var reviewLink = regexp.MustCompile(`(?m)^Link:.*/id/I[0-9a-f]{40}$`)

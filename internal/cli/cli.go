// Package cli exposes git-review's review workflow through Cobra and Fang.
package cli

import (
	"context"
	"io"

	"github.com/nnutter/git-review/internal/cli/root"
)

// Execute runs a command with explicit process streams and checkout location.
// It delegates to the assembled root tree so existing callers keep working.
func Execute(ctx context.Context, args []string, dir string, input io.Reader, output, terminal io.Writer, version string) error {
	return root.Execute(ctx, args, dir, input, output, terminal, version)
}

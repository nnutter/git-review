// Command git-review is the git-review command line tool.
package main

import (
	"context"
	"os"

	"github.com/nnutter/git-review/internal/cli"
)

func main() {
	if err := cli.Execute(context.Background(), os.Args[1:], ".", os.Stdin, os.Stdout, os.Stderr, version); err != nil {
		os.Exit(1)
	}
}

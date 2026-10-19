// Package display presents CLI helpers used by every git-review subcommand.
package display

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"strings"
)

// ErrorResult is the versioned JSON object for command failures.
type ErrorResult struct {
	Version int    `json:"version"`
	Command string `json:"command"`
	Status  string `json:"status"`
	Error   string `json:"error"`
}

// Determine error output even if Cobra fails before it parses persistent flags.
func WantsJSON(args []string) bool {
	enabled := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		switch arg {
		case "--json", "--json=true":
			enabled = true
		case "--json=false":
			enabled = false
		}
	}
	return enabled
}

func WriteJSON(output io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, strings.TrimSpace(string(data)))
	return err
}

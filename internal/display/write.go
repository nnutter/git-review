package display

import (
	"io"

	"github.com/nnutter/git-review/internal/review"
)

// Write emits a stack result as JSON or terminal text.
func Write(w io.Writer, asJSON bool, result review.Result, verbose bool) error {
	if asJSON {
		return WriteJSON(w, result)
	}
	return Render(w, result, verbose)
}

// WriteFiles emits a file result as JSON or terminal text.
func WriteFiles(w io.Writer, asJSON bool, result review.FilesResult) error {
	if asJSON {
		return WriteJSON(w, result)
	}
	return RenderFiles(w, result)
}

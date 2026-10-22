package cli

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nnutter/git-review/internal/review"
)

func TestApproveAllRecordsEveryStack(t *testing.T) {
	dir := featureRepo(t)
	git(t, dir, "commit", "--allow-empty", "-m", "Second\n\nChange-Id: I"+strings.Repeat("b", 40))
	out, _, err := execute(t, dir, "approve", "--all", "--json")
	require.NoError(t, err)
	var result review.Result
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.Equal(t, "approved", result.Status)
	require.Len(t, result.Entries, 2)
	out, _, err = execute(t, dir, "status", "--needs-review", "--json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.Empty(t, result.Entries)
}

func TestApproveArgumentsRequireExactlyOneSelection(t *testing.T) {
	dir := featureRepo(t)
	_, _, err := execute(t, dir, "approve")
	require.ErrorContains(t, err, "requires <start_commit> or --all")
	_, _, err = execute(t, dir, "approve", "--all", "HEAD")
	require.ErrorContains(t, err, "cannot be combined")
	_, _, err = execute(t, dir, "approve", "--all", "HEAD", "HEAD")
	require.ErrorContains(t, err, "cannot be combined")
	status, _, err := execute(t, dir, "status", "--needs-review", "--json")
	require.NoError(t, err)
	var result review.Result
	require.NoError(t, json.Unmarshal([]byte(status), &result))
	require.Len(t, result.Entries, 1)
	require.Equal(t, "needs_review", result.Entries[0].Status)
}

package testenv

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsolationPreservesFunctionalTestOptIn(t *testing.T) {
	t.Setenv("GIT_REVIEW_REAL_TUICR", "1")
	t.Setenv("GIT_DIR", "/not-the-test-repository")
	require.NoError(t, isolateGit())
	require.Equal(t, "1", os.Getenv("GIT_REVIEW_REAL_TUICR"))
	require.Empty(t, os.Getenv("GIT_DIR"))
}

package review

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckoutViewPathsWithoutGit(t *testing.T) {
	for _, layout := range []string{"checkout", "linked", "separate git dir"} {
		t.Run(layout, func(t *testing.T) {
			r := testRepo(t)
			switch layout {
			case "linked":
				path := filepath.Join(t.TempDir(), "linked")
				gitTest(t, r.Dir, "worktree", "add", "-b", "linked", path, "feature")
				r.Dir = path
			case "separate git dir":
				gitTest(t, r.Dir, "init", "--separate-git-dir", filepath.Join(t.TempDir(), "metadata"))
			}
			root := gitTest(t, r.Dir, "rev-parse", "--show-toplevel")
			common := gitTest(t, r.Dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
			branch := gitTest(t, r.Dir, "symbolic-ref", "--short", "HEAD")
			want := filepath.Join(common, "tuicr-reviews", fmt.Sprintf("%x.json", sha256.Sum256([]byte(branch))))
			r.Dir = filepath.Join(r.Dir, "nested")
			require.NoError(t, os.Mkdir(r.Dir, 0o755))
			t.Setenv("PATH", t.TempDir())
			view, repo, err := r.checkoutView(t.Context())
			require.NoError(t, err)
			require.Equal(t, root, repo)
			require.Equal(t, branch, view.Branch)
			require.Equal(t, want, view.Path)
		})
	}
}

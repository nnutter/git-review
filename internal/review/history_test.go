package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// These layouts exercise filesystem discovery and packed-object access rather
// than merely asserting that the dependency advertises worktree support.
func TestSnapshotRepositoryLayouts(t *testing.T) {
	for _, layout := range []string{"checkout", "linked", "relative-linked", "sha256"} {
		t.Run(layout, func(t *testing.T) {
			if layout == "sha256" {
				t.Setenv("GIT_DEFAULT_HASH", "sha256")
			}
			r := testRepo(t)
			base := gitTest(t, r.Dir, "rev-parse", "HEAD")
			id := "I" + strings.Repeat("c", 40)
			gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "First line\nsecond line\n\nChange-Id: "+id)
			head := gitTest(t, r.Dir, "rev-parse", "HEAD")
			subject := gitTest(t, r.Dir, "show", "-s", "--format=%s", "HEAD")
			gitTest(t, r.Dir, "update-ref", "refs/remotes/origin/main", base)
			gitTest(t, r.Dir, "config", "remote.origin.url", ".")
			gitTest(t, r.Dir, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
			if strings.Contains(layout, "linked") {
				args := []string{"worktree", "add", "-b", "linked"}
				if layout == "relative-linked" {
					cmd := exec.CommandContext(t.Context(), "git", "worktree", "add", "-h")
					cmd.Dir = r.Dir
					out, _ := cmd.CombinedOutput()
					if !strings.Contains(string(out), "relative-paths") {
						t.Skip("Git does not support relative worktree links")
					}
					args = append(args, "--relative-paths")
				}
				path := filepath.Join(t.TempDir(), "linked")
				gitTest(t, r.Dir, append(args, path, "feature")...)
				r.Dir = path
			}
			gitTest(t, r.Dir, "branch", "--set-upstream-to=origin/main")
			gitTest(t, r.Dir, "gc", "--quiet")
			repo, err := filepath.EvalSymlinks(r.Dir)
			require.NoError(t, err)
			subdir := filepath.Join(repo, "nested")
			require.NoError(t, os.Mkdir(subdir, 0o700))
			r.Dir = subdir
			view, err := r.Snapshot(t.Context())
			require.NoError(t, err)
			require.Equal(t, "refs/remotes/origin/main", view.Target)
			require.Equal(t, base, view.Base)
			require.Equal(t, head, view.Head)
			require.Len(t, view.Stacks, 1)
			require.Equal(t, repo, view.Stacks[0].Repo)
			require.Equal(t, []string{head}, view.Stacks[0].Commits)
			require.Equal(t, subject, view.Stacks[0].Subject)
			require.Equal(t, id, view.Stacks[0].ChangeID)
		})
	}
}

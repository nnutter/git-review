package review

import (
	"os"
	"path/filepath"
	"testing"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/stretchr/testify/require"
)

func TestResolveCommitReferences(t *testing.T) {
	r := testRepo(t)
	base := gitTest(t, r.Dir, "rev-parse", "HEAD")
	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Feature")
	head := gitTest(t, r.Dir, "rev-parse", "HEAD")
	gitTest(t, r.Dir, "tag", "-a", "release", "-m", "Release", base)

	for _, ref := range []string{"HEAD@{1}", "@{upstream}"} {
		t.Run(ref, func(t *testing.T) {
			got, err := r.Resolve(t.Context(), ref)
			require.NoError(t, err)
			require.Equal(t, base, got)
		})
	}

	subdir := filepath.Join(r.Dir, "nested")
	require.NoError(t, os.Mkdir(subdir, 0o755))
	for _, tc := range []struct{ ref, want string }{
		{"HEAD", head},
		{"feature", head},
		{head, head},
		{head[:12], head},
		{"HEAD~1", base},
		{"HEAD^", base},
		{"release", base},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			// Ordinary references must resolve without a Git executable, even
			// when the caller starts below the checkout root.
			t.Setenv("PATH", t.TempDir())
			got, err := (Repository{Dir: subdir}).Resolve(t.Context(), tc.ref)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	for _, ref := range []string{"missing-ref", "--help"} {
		_, err := r.Resolve(t.Context(), ref)
		require.Error(t, err)
	}
	blob := gitTest(t, r.Dir, "hash-object", "-w", "--stdin")
	_, err := r.Resolve(t.Context(), blob)
	require.Error(t, err, "a blob is not a commit")
}

// This reproducer checks whether the pinned resolver silently ignores a reflog
// selector. It is evidence for the Resolve migration, not desired behavior.
func TestGoGitResolveRevisionIgnoresReflogSelector(t *testing.T) {
	r := testRepo(t)
	previous := gitTest(t, r.Dir, "rev-parse", "HEAD")
	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Second commit")
	head := gitTest(t, r.Dir, "rev-parse", "HEAD")
	require.NotEqual(t, previous, head)

	want := gitTest(t, r.Dir, "rev-parse", "--verify", "--end-of-options", "HEAD@{1}^{commit}")
	require.Equal(t, want, previous)

	repo, err := git.PlainOpen(r.Dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, repo.Close()) })

	got, err := repo.ResolveRevision(plumbing.Revision("HEAD@{1}"))
	require.NoError(t, err)
	require.NotNil(t, got)
	t.Logf("Git HEAD@{1}: %s; go-git HEAD@{1}: %s; HEAD: %s", want, got.String(), head)
	require.Equal(t, head, got.String(), "reproducer: go-git returns HEAD despite the reflog selector")
	require.NotEqual(t, want, got.String(), "reproducer: go-git disagrees with Git")

	// Resolve currently appends this suffix. With that suffix, go-git rejects
	// the expression instead of silently resolving it to HEAD.
	_, err = repo.ResolveRevision(plumbing.Revision("HEAD@{1}^{commit}"))
	require.Error(t, err)
	t.Logf("go-git HEAD@{1}^{commit}: %v", err)
}

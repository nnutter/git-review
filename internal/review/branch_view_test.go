package review

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBranchViewUpstream(t *testing.T) {
	for _, mode := range []string{"local", "remote", "custom fetch", "missing ref", "no upstream", "unmapped"} {
		t.Run(mode, func(t *testing.T) {
			r := testRepo(t)
			switch mode {
			case "remote", "custom fetch", "missing ref", "unmapped":
				gitTest(t, r.Dir, "remote", "add", "origin", ".")
				tracking := "refs/remotes/origin/main"
				if mode == "custom fetch" {
					gitTest(t, r.Dir, "config", "remote.origin.fetch", "+refs/heads/*:refs/tracking/*")
					tracking = "refs/tracking/main"
				}
				if mode != "missing ref" {
					gitTest(t, r.Dir, "update-ref", tracking, "main")
				}
				gitTest(t, r.Dir, "config", "branch.feature.remote", "origin")
				if mode == "unmapped" {
					gitTest(t, r.Dir, "config", "remote.origin.fetch", "+refs/heads/other:refs/remotes/origin/other")
				}
			case "no upstream":
				gitTest(t, r.Dir, "branch", "--unset-upstream")
			}

			if mode == "missing ref" || mode == "no upstream" || mode == "unmapped" {
				_, _, err := r.branchView(t.Context())
				require.ErrorContains(t, err, "requires a configured Git upstream")
				return
			}
			want := gitTest(t, r.Dir, "rev-parse", "--symbolic-full-name", "@{upstream}")
			view, root, err := r.branchView(t.Context())
			require.NoError(t, err)
			require.Equal(t, want, view.Target)
			require.Equal(t, r.Dir, root)
		})
	}
}

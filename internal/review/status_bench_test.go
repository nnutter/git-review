package review

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Build history outside the measured operation. Fast-import keeps the fixture
// cost small enough to include large ranges in routine benchmark runs.
func benchmarkRepository(b *testing.B, commits int, fixups bool) Repository {
	b.Helper()
	dir, err := filepath.EvalSymlinks(b.TempDir())
	require.NoError(b, err)
	run := func(input string, args ...string) {
		b.Helper()
		cmd := exec.CommandContext(b.Context(), "git", args...)
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		require.NoError(b, err, "git %v: %s", args, out)
	}
	run("", "init", "-b", "main")
	var stream strings.Builder
	stream.WriteString("commit refs/heads/main\ncommitter Benchmark <bench@example.com> 1000000000 +0000\ndata 4\nBase\n\n")
	for i := range commits {
		subject := fmt.Sprintf("Feature %d", i)
		if fixups {
			if i%2 == 1 {
				subject = fmt.Sprintf("fixup! Feature %d", i-1)
			}
		}
		message := fmt.Sprintf("%s\n\nChange-Id: I%040x\n", subject, i)
		fmt.Fprintf(&stream, "commit refs/heads/feature\ncommitter Benchmark <bench@example.com> 1000000000 +0000\ndata %d\n%s", len(message), message)
		if i == 0 {
			stream.WriteString("from refs/heads/main\n")
		}
		fmt.Fprintf(&stream, "M 100644 inline file-%d\ndata 8\ncontent\n\n", i)
	}
	run(stream.String(), "fast-import", "--quiet")
	run("", "symbolic-ref", "HEAD", "refs/heads/feature")
	run("", "branch", "--set-upstream-to=main")
	return Repository{Dir: dir}
}

func benchmarkCacheModes(b *testing.B, r Repository, operation func() error) {
	b.Helper()
	for _, mode := range []string{"cold", "warm"} {
		b.Run(mode, func(b *testing.B) {
			view, err := r.Snapshot(b.Context())
			require.NoError(b, err)
			b.ReportAllocs()
			for b.Loop() {
				if mode == "cold" {
					b.StopTimer()
					require.NoError(b, os.RemoveAll(patchCachePath(view)))
					b.StartTimer()
				}
				require.NoError(b, operation())
			}
		})
	}
}

func BenchmarkSnapshot(b *testing.B) {
	for _, commits := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("commits=%d", commits), func(b *testing.B) {
			r := benchmarkRepository(b, commits, false)
			benchmarkCacheModes(b, r, func() error {
				_, err := r.Snapshot(b.Context())
				return err
			})
		})
	}
}

func BenchmarkStatus(b *testing.B) {
	for _, tc := range []struct {
		name    string
		commits int
		history bool
		fixups  bool
	}{
		{name: "commits=10", commits: 10},
		{name: "commits=100", commits: 100},
		{name: "commits=1000", commits: 1000},
		{name: "history", commits: 100, history: true},
		{name: "fixups", commits: 100, history: true, fixups: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			r := benchmarkRepository(b, tc.commits, tc.fixups)
			if tc.history {
				view, err := r.Snapshot(b.Context())
				require.NoError(b, err)
				err = (Store{Path: view.Path}).Update(func(state *State) error {
					for range 3 {
						for _, stack := range view.Stacks {
							state.Attempts = append(state.Attempts, newAttempt(stack, "approved"))
						}
					}
					return nil
				})
				require.NoError(b, err)
			}
			svc := Service{Repository: r}
			benchmarkCacheModes(b, r, func() error {
				_, err := svc.Status(b.Context(), false)
				return err
			})
		})
	}
}

package review

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

type historyCommit struct {
	sha, parent, subject, changeID string
}

// Read immutable objects through go-git. Keep Git's trailer parser in one batch:
// trailer configuration and folded values must retain their existing meaning.
func readHistory(ctx context.Context, r Repository, repo, base, head string) (commits []historyCommit, err error) {
	if base == head {
		return []historyCommit{}, nil
	}
	objects, err := git.PlainOpen(repo)
	if err != nil {
		return nil, fmt.Errorf("open commit objects: %w", err)
	}
	defer func() { err = errors.Join(err, objects.Close()) }()
	commits, err = walkHistory(ctx, objects, base, head)
	if err != nil {
		return nil, err
	}
	ids, err := readChangeIDTrailers(ctx, r, base, head)
	if err != nil {
		return nil, err
	}
	for i := range commits {
		id, ok := ids[commits[i].sha]
		if !ok {
			return nil, fmt.Errorf("missing Git trailer record for %s", commits[i].sha)
		}
		commits[i].changeID = id
	}
	return commits, nil
}

func walkHistory(ctx context.Context, objects *git.Repository, base, head string) ([]historyCommit, error) {
	var commits []historyCommit
	for sha := head; sha != base; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		commit, err := objects.CommitObject(plumbing.NewHash(sha))
		if err != nil {
			return nil, fmt.Errorf("read commit %s: %w", sha, err)
		}
		if len(commit.ParentHashes) != 1 {
			return nil, fmt.Errorf("review requires linear feature history; resolve merges first")
		}
		parent := commit.ParentHashes[0].String()
		commits = append(commits, historyCommit{sha: sha, parent: parent, subject: commitSubject(commit.Message)})
		sha = parent
	}
	slices.Reverse(commits)
	return commits, nil
}

func readChangeIDTrailers(ctx context.Context, r Repository, base, head string) (map[string]string, error) {
	trailers, err := r.gitOutput(ctx, "log", "-z", "--format=%H%x00%(trailers:key=Change-Id,valueonly)", base+".."+head)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(trailers, "\x00")
	if len(fields)%2 != 1 {
		return nil, fmt.Errorf("invalid Git trailer records")
	}
	if fields[len(fields)-1] != "" {
		return nil, fmt.Errorf("invalid Git trailer records")
	}
	ids := make(map[string]string, len(fields)/2)
	for i := 0; i < len(fields)-1; i += 2 {
		ids[fields[i]] = strings.TrimSpace(fields[i+1])
	}
	return ids, nil
}

func commitSubject(message string) string {
	var lines []string
	for line := range strings.SplitSeq(strings.TrimSpace(message), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, " ")
}

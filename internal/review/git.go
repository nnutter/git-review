// Package review implements feature-branch commit review independently of a UI.
package review

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"charm.land/log/v2"
)

// Stack identifies an original commit and its adjacent feedback commits.
// Field names retain the Pi state format. ChangeID is optional lineage metadata.
// AmendCount counts attached amend! and reword! commits, whose messages carry
// reviewable content. Attached fixup! commits carry no message, so patch
// identity alone decides their review status.
type Stack struct {
	Repo       string   `json:"repo"`
	Commits    []string `json:"commits"`
	Base       string   `json:"base"`
	Tip        string   `json:"tip"`
	Subject    string   `json:"subject"`
	Key        string   `json:"key"`
	ChangeID   string   `json:"changeId,omitempty"`
	PatchHash  string   `json:"patchHash,omitempty"`
	AmendCount int      `json:"amendCount,omitzero"`
}

func (s Stack) validate() error {
	if !filepath.IsAbs(s.Repo) {
		return fmt.Errorf("stack requires an absolute repository path")
	}
	if err := validateCommits(s.Commits); err != nil {
		return err
	}
	if !fullSHA.MatchString(s.Base) {
		return fmt.Errorf("stack requires a full parent SHA")
	}
	if s.Tip != s.Commits[len(s.Commits)-1] {
		return fmt.Errorf("stack tip does not match commits")
	}
	expected := s.Commits[0]
	if len(s.Commits) > 1 {
		expected += ".." + s.Tip
	}
	if s.Key != expected {
		return fmt.Errorf("stack key does not match commits")
	}
	if s.Subject == "" {
		return fmt.Errorf("stack requires a subject")
	}
	return s.validateMetadata()
}

func (s Stack) validateMetadata() error {
	if s.PatchHash != "" && !patchHashPattern.MatchString(s.PatchHash) {
		return fmt.Errorf("invalid stack patch hash")
	}
	if s.ChangeID != "" {
		if !changeIDPattern.MatchString(s.ChangeID) {
			return fmt.Errorf("invalid stack Change-Id")
		}
	}
	if s.AmendCount < 0 {
		return fmt.Errorf("invalid stack amend count")
	}
	if s.AmendCount > len(s.Commits) {
		return fmt.Errorf("invalid stack amend count")
	}
	return nil
}

func validateCommits(commits []string) error {
	if len(commits) == 0 {
		return fmt.Errorf("stack requires commits")
	}
	seen := map[string]bool{}
	for _, sha := range commits {
		if !fullSHA.MatchString(sha) {
			return fmt.Errorf("stack requires full commit SHAs")
		}
		if seen[sha] {
			return fmt.Errorf("duplicate stack commit")
		}
		seen[sha] = true
	}
	return nil
}

// Snapshot is an immutable view of a feature branch's review range.
type Snapshot struct {
	Branch string  `json:"branch"`
	Target string  `json:"target"`
	Base   string  `json:"base"`
	Head   string  `json:"head"`
	Stacks []Stack `json:"stacks"`
	Path   string  `json:"statePath"`
}

// NewRepository creates command-scoped diagnostics for a source checkout.
func NewRepository(dir string, logger *log.Logger) Repository {
	return Repository{Dir: dir, Logger: logger, missingIDWarning: new(sync.Once)}
}

// Repository executes Git in the source checkout.
type Repository struct {
	Dir              string
	Logger           *log.Logger
	missingIDWarning *sync.Once
}

var quietLogger = log.New(io.Discard)

// Resolve accepts a commit reference but never interprets it as an option.
func (r Repository) Resolve(ctx context.Context, ref string) (string, error) {
	return r.git(ctx, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
}

// Snapshot requires an upstream and a single, linear feature-branch range.
func (r Repository) Snapshot(ctx context.Context) (Snapshot, error) {
	started := time.Now()
	defer func() { r.logger().Debug("Repository snapshot completed", "elapsed", time.Since(started)) }()
	view, repo, err := r.stackView(ctx)
	if err != nil {
		return view, err
	}
	cache := newPatchCache(ctx, r, view, repo)
	for i := range view.Stacks {
		stack := &view.Stacks[i]
		stack.PatchHash, err = cache.hash(ctx, r, stack.Base, stack.Tip)
		if err != nil {
			return view, err
		}
	}
	return view, nil
}

func (r Repository) branchView(ctx context.Context) (Snapshot, string, error) {
	view, repo, err := r.checkoutView(ctx)
	if err != nil {
		return view, "", err
	}
	target, err := r.git(ctx, "rev-parse", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return view, "", fmt.Errorf("branch %s requires a configured Git upstream: %w", view.Branch, err)
	}
	view.Target = target
	return view, repo, nil
}

func changeID(sha, id string, ids map[string]bool) (string, error) {
	if id == "" {
		return "", nil
	}
	if !changeIDPattern.MatchString(id) {
		return "", fmt.Errorf("commit %s requires at most one valid Change-Id trailer", sha)
	}
	if ids[id] {
		return "", fmt.Errorf("duplicate Change-Id %s on current original commits", id)
	}
	ids[id] = true
	return id, nil
}

func (r Repository) checkoutView(ctx context.Context) (Snapshot, string, error) {
	var view Snapshot
	repo, err := r.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return view, "", err
	}
	branch, err := r.git(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return view, "", fmt.Errorf("review requires a branch, not detached HEAD: %w", err)
	}
	common, err := r.git(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return view, "", err
	}
	view.Branch = branch
	view.Path = filepath.Join(common, "tuicr-reviews", fmt.Sprintf("%x.json", sha256.Sum256([]byte(branch))))
	return view, repo, nil
}

func (r Repository) git(ctx context.Context, args ...string) (string, error) {
	out, err := r.gitOutput(ctx, args...)
	return strings.TrimSpace(out), err
}

func (r Repository) gitInput(ctx context.Context, input string, args ...string) (string, error) {
	started := time.Now()
	defer func() { r.logger().Debug("Git command completed", "args", args, "elapsed", time.Since(started)) }()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	cmd.Stdin = strings.NewReader(input)
	// Keep Git diagnostics stable so repository discovery errors can be identified.
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if strings.HasPrefix(stderr.String(), "fatal: not a git repository") {
			return "", fmt.Errorf("git-review must be used from inside a Git repository")
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

func (r Repository) gitOutput(ctx context.Context, args ...string) (string, error) {
	return r.gitInput(ctx, "", args...)
}

func (r Repository) logger() *log.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return quietLogger
}

// patchHash fingerprints the aggregate diff, not commit metadata. Empty diffs
// have no content identity, so unrelated empty commits cannot share approval.
func (r Repository) patchHash(ctx context.Context, base, tip string) (string, error) {
	patch, err := r.gitOutput(ctx, "-c", "core.quotePath=true", "-c", "diff.suppressBlankEmpty=false", "diff",
		"--patch", "--binary", "--full-index", "--no-renames", "--no-ext-diff",
		"--no-textconv", "--no-color", "--no-relative", "--ignore-submodules=none",
		"--src-prefix=a/", "--dst-prefix=b/", "--output-indicator-new=+",
		"--output-indicator-old=-", "--output-indicator-context= ",
		"--unified=3", "--inter-hunk-context=0", "--diff-algorithm=myers",
		"--no-indent-heuristic", "--submodule=short", "-O/dev/null", base, tip, "--")
	if err != nil {
		return "", err
	}
	if patch == "" {
		return "", nil
	}
	// Verbatim patch IDs preserve whitespace and context, but ignore text blob IDs
	// and hunk line numbers so an unchanged patch can survive relocation.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "patch-id", "--verbatim")
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.Stdin = strings.NewReader(patch)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git patch-id --verbatim: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 || !fullSHA.MatchString(fields[0]) {
		return "", fmt.Errorf("git patch-id --verbatim returned an invalid patch ID")
	}
	return "v2:" + fields[0], nil
}

// reviewBranch identifies the branch whose review lock applies during a replay.
// Other review commands still require an attached HEAD through branchView.
func (r Repository) reviewBranch(ctx context.Context) (string, error) {
	branch, err := r.git(ctx, "symbolic-ref", "--quiet", "HEAD")
	if err == nil {
		return branch, nil
	}
	if !detachedHEADExit(err) {
		return "", err
	}
	for _, backend := range []string{"rebase-merge", "rebase-apply"} {
		path, pathErr := r.git(ctx, "rev-parse", "--path-format=absolute", "--git-path", backend+"/head-name")
		if pathErr != nil {
			return "", pathErr
		}
		data, readErr := os.ReadFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return "", readErr
		}
		branch = strings.TrimSpace(string(data))
		if !strings.HasPrefix(branch, "refs/heads/") {
			return "", fmt.Errorf("rebase does not identify a local branch")
		}
		if _, err := r.git(ctx, "check-ref-format", branch); err != nil {
			return "", err
		}
		return branch, nil
	}
	return "", err
}

func (r Repository) stackView(ctx context.Context) (Snapshot, string, error) {
	view, repo, err := r.branchView(ctx)
	if err != nil {
		return view, repo, err
	}
	view.Head, err = r.Resolve(ctx, "HEAD")
	if err != nil {
		return view, repo, err
	}
	targetSHA, err := r.Resolve(ctx, view.Target)
	if err != nil {
		return view, repo, err
	}
	view.Base, err = r.git(ctx, "merge-base", "--all", view.Head, targetSHA)
	if err != nil {
		return view, repo, err
	}
	if strings.Contains(view.Base, "\n") {
		return view, repo, fmt.Errorf("review requires one merge-base with upstream")
	}
	history, err := readHistory(ctx, r, repo, view.Base, view.Head)
	if err != nil {
		return view, repo, err
	}
	view.Stacks, err = r.stacks(repo, view.Base, history)
	if err != nil {
		return view, repo, err
	}
	return view, repo, nil
}

func (r Repository) stacks(repo, base string, history []historyCommit) ([]Stack, error) {
	stacks := []Stack{}
	if len(history) == 0 {
		return stacks, nil
	}
	parent := base
	ids := map[string]bool{}
	targets := map[string][]int{}
	for _, commit := range history {
		if commit.parent != parent {
			return nil, fmt.Errorf("review requires linear feature history; resolve merges first")
		}
		sha, subject := commit.sha, commit.subject
		if subject == "" {
			return nil, fmt.Errorf("commit %s has an empty subject", sha)
		}
		if match := feedbackSubject.FindStringSubmatch(subject); match != nil {
			if !tryAppendFeedback(stacks, targets, sha, match[1], match[2]) {
				id, err := changeID(sha, commit.changeID, ids)
				if err != nil {
					return nil, err
				}
				stacks = append(stacks, Stack{Repo: repo, Commits: []string{sha}, Base: parent, Tip: sha, Subject: subject, Key: sha, ChangeID: id})
			}
		} else {
			id, err := changeID(sha, commit.changeID, ids)
			if err != nil {
				return nil, err
			}
			stacks = append(stacks, Stack{Repo: repo, Commits: []string{sha}, Base: parent, Tip: sha, Subject: subject, Key: sha, ChangeID: id})
		}
		targets[subject] = append(targets[subject], len(stacks)-1)
		parent = sha
	}
	warnMissingChangeIDs(r, stacks)
	return stacks, nil
}

func warnMissingChangeIDs(r Repository, stacks []Stack) {
	if r.missingIDWarning == nil {
		return
	}
	missing := 0
	for _, stack := range stacks {
		if stack.ChangeID == "" {
			missing++
		}
	}
	if missing == 0 {
		return
	}
	r.missingIDWarning.Do(func() {
		r.logger().Warn("Original commits lack Change-Id trailers; add them to improve lineage tracking after rewrites", "commits", missing)
	})
}

func detachedHEADExit(err error) bool {
	exit, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		return false
	}
	return exit.ExitCode() == 1
}

func changesCommitMessage(kind string) bool {
	switch kind {
	case "amend!", "reword!":
		return true
	default:
		return false
	}
}

func tryAppendFeedback(stacks []Stack, targets map[string][]int, sha, kind, subject string) bool {
	matches := targets[subject]
	if len(matches) != 1 {
		return false
	}
	if matches[0] != len(stacks)-1 {
		return false
	}
	last := &stacks[len(stacks)-1]
	last.Commits = append(last.Commits, sha)
	last.Tip = sha
	last.Key = last.Commits[0] + ".." + sha
	if changesCommitMessage(kind) {
		last.AmendCount++
	}
	return true
}

var (
	patchHashPattern = regexp.MustCompile(`^(?:v1:[a-f0-9]{64}|v2:(?:[a-f0-9]{40}|[a-f0-9]{64}))$`)
	feedbackSubject  = regexp.MustCompile(`^(fixup!|reword!|amend!) (.+)$`)
	changeIDPattern  = regexp.MustCompile(`^I[0-9a-fA-F]{40}$`)
)

package review

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FilesOptions selects a working-tree file review.
type FilesOptions struct {
	Path      string
	All       bool
	Untracked bool
	Session   string
}

// FileReview contains validated progress for a file review session.
type FileReview struct {
	ID           string           `json:"id"`
	Version      string           `json:"version"`
	Reviewed     int              `json:"reviewed"`
	Files        int              `json:"files"`
	Complete     bool             `json:"complete"`
	Comments     []map[string]any `json:"comments"`
	SessionNotes *string          `json:"sessionNotes"`
}

// Status never equates a missing or partially reviewed session with approval.
func (r *FileReview) Status() string {
	if r == nil {
		return "incomplete"
	}
	if len(r.Comments) > 0 {
		return "feedback"
	}
	if r.SessionNotes != nil {
		if *r.SessionNotes != "" {
			return "feedback"
		}
	}
	if r.Complete {
		return "approved"
	}
	return "incomplete"
}

// FilesResult is the shared input to JSON encoding and terminal rendering
// for file reviews. Command is always "files" to distinguish it from
// "commits" stack reviews.
type FilesResult struct {
	Version   int         `json:"version"`
	Command   string      `json:"command"`
	Status    string      `json:"status"`
	Repo      string      `json:"repo"`
	Path      string      `json:"path,omitempty"`
	AllFiles  bool        `json:"allFiles,omitzero"`
	Untracked bool        `json:"untracked,omitzero"`
	Review    *FileReview `json:"review,omitempty"`
	Session   string      `json:"session,omitempty"`
}

// FileReviewer runs tuicr for working-tree files, keeping its terminal output
// separate from results.
type FileReviewer struct {
	Service  Service
	Reader   SavedReader
	Input    io.Reader
	Terminal io.Writer
}

// Review launches tuicr for working-tree files and reads its saved session.
func (r FileReviewer) Review(ctx context.Context, options FilesOptions) (FilesResult, error) {
	var result FilesResult
	if err := validateFileScope(options); err != nil {
		return result, err
	}
	repo, err := r.Service.Repository.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return result, err
	}
	lock, err := r.lockReview(ctx)
	if err != nil {
		return result, err
	}
	defer func() {
		// Close releases the repository-wide lock so status --is-reviewing
		// reports false after either commits or files reviews exit.
		_ = lock.Close()
	}()

	stopMaintenance := startCacheMaintenance(ctx, r.Service.Repository, lock)
	defer stopMaintenance()
	if reviewBranchFiles(options) {
		return r.reviewBranchDiff(ctx, repo, options.Session, lock)
	}
	allFiles := options.All
	filePath := options.Path
	if reviewUntrackedDirectory(options) {
		filePath = "."
	}
	diffSource := "working_tree"
	args := []string{"tui", "--no-update-check", "--stdout"}
	if allFiles {
		args = append(args, "--all-files")
		diffSource = "pristine"
	} else {
		args = append(args, "--file", filePath)
	}
	if err := r.launch(ctx, args, lock); err != nil {
		return result, err
	}
	saved, sessionPath, err := r.readReview(filePath, diffSource, options.Session)
	if err != nil {
		return result, err
	}
	result = FilesResult{
		Version:   1,
		Command:   "files",
		Repo:      repo,
		Path:      options.Path,
		AllFiles:  allFiles,
		Untracked: options.Untracked,
		Session:   sessionPath,
	}
	if reviewUntrackedDirectory(options) {
		result.Path = "."
	}
	return fileReviewResult(result, saved)
}

func (r FileReviewer) launch(ctx context.Context, args []string, lock *os.File) error {
	cmd := exec.CommandContext(ctx, "tuicr", args...)
	cmd.Dir = r.Service.Repository.Dir
	cmd.Stdin = r.Input
	// --stdout bypasses tuicr's clipboard confirmation. The TUI uses /dev/tty;
	// git-review reads persisted feedback instead of the exported markdown.
	cmd.Stdout = io.Discard
	cmd.Stderr = r.Terminal
	// Keep the worktree locked if git-review dies while tuicr is still running.
	cmd.ExtraFiles = []*os.File{lock}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tuicr interrupted or failed; use git-review files to retry: %w", err)
	}
	return nil
}

func (r FileReviewer) lockReview(ctx context.Context) (*os.File, error) {
	branch, err := r.Service.Repository.reviewBranch(ctx)
	if err != nil {
		return nil, err
	}
	path, err := WorktreePath(r.Service.Repository.Dir)
	if err != nil {
		return nil, err
	}
	lock, err := lockWorktree(path)
	if err != nil {
		return nil, err
	}
	if err := lock.Truncate(0); err != nil {
		_ = lock.Close()
		return nil, err
	}
	if _, err := lock.WriteString(branch); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return lock, nil
}

func (r FileReviewer) readReview(path, diffSource, selector string) (*SavedReview, string, error) {
	root, err := fileSessionRoot(r.Service.Repository.Dir, path)
	if err != nil {
		return nil, "", err
	}
	return r.Reader.ReadFile(root, diffSource, selector)
}

// reviewBranchDiff uses Git's upstream merge-base without imposing the linear
// history and stack-placement rules required by commit reviews.
func (r FileReviewer) reviewBranchDiff(ctx context.Context, repo, selector string, lock *os.File) (FilesResult, error) {
	result := FilesResult{Version: 1, Command: "files", Repo: repo}
	repository := r.Service.Repository
	view, _, err := repository.branchView(ctx)
	if err != nil {
		return result, err
	}
	head, err := repository.Resolve(ctx, "HEAD")
	if err != nil {
		return result, err
	}
	base, err := repository.git(ctx, "merge-base", "--all", view.Target, head)
	if err != nil {
		return result, err
	}
	if strings.Contains(base, "\n") {
		return result, fmt.Errorf("review requires one merge-base with upstream")
	}
	history, err := repository.git(ctx, "rev-list", "--reverse", base+".."+head)
	if err != nil {
		return result, err
	}
	if history == "" {
		return fileReviewResult(result, nil)
	}
	// Run at the root so invocation from a subdirectory keeps the same scope
	// and saved-session identity.
	r.Service.Repository.Dir = repo
	if err := r.launch(ctx, []string{"tui", "--no-update-check", "--stdout", "--revisions", base + ".." + head}, lock); err != nil {
		return result, err
	}
	scope := Stack{Repo: repo, Base: base, Tip: head, Commits: strings.Split(history, "\n")}
	saved, err := r.Reader.Read(scope, selector)
	if err != nil {
		return result, err
	}
	if saved != nil {
		result.Session = saved.Path
	}
	return fileReviewResult(result, saved)
}

// Tuicr keys --file sessions by the canonical parent of a file or by the
// selected directory. --all-files uses the canonical current directory.
func fileSessionRoot(dir, path string) (string, error) {
	if path == "" {
		path = "."
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		path = filepath.Dir(path)
	}
	return path, nil
}

func fileReviewResult(result FilesResult, saved *SavedReview) (FilesResult, error) {
	if saved == nil || untouchedFileReview(saved) {
		result.Status = "cancelled"
		return result, nil
	}
	if err := makeFileCommentsRepoRelative(saved, result.Repo); err != nil {
		return result, err
	}
	review := &FileReview{
		ID:           saved.ID,
		Version:      saved.Version,
		Reviewed:     saved.Reviewed,
		Files:        saved.Files,
		Complete:     saved.Complete,
		Comments:     saved.Comments,
		SessionNotes: saved.SessionNotes,
	}
	result.Status = review.Status()
	result.Review = review
	return result, nil
}

func makeFileCommentsRepoRelative(saved *SavedReview, repo string) error {
	for _, comment := range saved.Comments {
		path, _ := comment["path"].(string)
		if path == "" {
			continue
		}
		relative, err := filepath.Rel(repo, filepath.Join(saved.Scope.Repo, filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		comment["path"] = filepath.ToSlash(relative)
	}
	return nil
}

func validateFileScope(options FilesOptions) error {
	if conflictingAllFileScope(options) {
		return fmt.Errorf("--all cannot be combined with --untracked or an explicit path")
	}
	if conflictingFileScope(options) {
		return fmt.Errorf("--untracked cannot be combined with an explicit path")
	}
	return nil
}

func conflictingAllFileScope(options FilesOptions) bool {
	if !options.All {
		return false
	}
	if options.Untracked {
		return true
	}
	return options.Path != ""
}

func reviewBranchFiles(options FilesOptions) bool {
	if options.All {
		return false
	}
	if options.Untracked {
		return false
	}
	return options.Path == ""
}

func conflictingFileScope(options FilesOptions) bool {
	if !options.Untracked {
		return false
	}
	return options.Path != ""
}

func reviewUntrackedDirectory(options FilesOptions) bool {
	if !options.Untracked {
		return false
	}
	return options.Path == ""
}

func untouchedFileReview(saved *SavedReview) bool {
	if saved.Status() != "incomplete" {
		return false
	}
	return saved.Reviewed == 0
}

func readFileStore(store, repo, diffSource, selector string) (*SavedReview, string, error) {
	entries, err := readIndex(store)
	if err != nil {
		return nil, "", err
	}
	if entries == nil {
		return nil, "", nil
	}
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		return nil, "", err
	}
	candidates := map[string]*SavedReview{}
	for slug, rows := range entries {
		for _, entry := range rows {
			saved, sessionPath, err := readFileEntry(store, repo, slug, entry, diffSource, selector)
			if err != nil {
				return nil, "", err
			}
			if saved != nil {
				candidates[sessionPath] = saved
			}
		}
	}
	if len(candidates) > 1 {
		return nil, "", fmt.Errorf("ambiguous saved reviews; select an exact session with --session")
	}
	for sessionPath, saved := range candidates {
		return saved, sessionPath, nil
	}
	return nil, "", nil
}

func readFileEntry(store, repo, slug string, entry object, diffSource, selector string) (*SavedReview, string, error) {
	kind, err := decodeRequired[object](entry, "kind")
	if err != nil {
		return nil, "", err
	}
	entryType, err := decodeRequired[string](kind, "type")
	if err != nil {
		return nil, "", err
	}
	if entryType != "local" {
		return nil, "", nil
	}
	canonical, err := decodeRequired[string](entry, "canonical_repo_path")
	if err != nil {
		return nil, "", err
	}
	if filepath.Clean(canonical) != repo {
		return nil, "", nil
	}
	relative, err := decodeRequired[string](entry, "path")
	if err != nil {
		return nil, "", err
	}
	if !sessionPathPattern.MatchString(relative) {
		return nil, "", fmt.Errorf("unsafe session path %q", relative)
	}
	path, err := safeSessionPath(store, relative)
	if err != nil {
		return nil, "", err
	}
	return readFileCandidate(path, repo, diffSource, selector, slug, relative)
}

func readFileCandidate(path, repo, diffSource, selector, slug, relative string) (*SavedReview, string, error) {
	session, err := readObject(path)
	if err != nil {
		return nil, "", err
	}
	saved, err := matchFileSession(session, repo, diffSource, selector, slug, relative)
	if err != nil {
		return nil, "", err
	}
	if saved != nil {
		saved.Slug = slug
		saved.Path = path
		return saved, path, nil
	}
	return nil, "", nil
}

func matchFileSession(session object, repo, diffSource, selector, slug, relative string) (*SavedReview, error) {
	source, err := decodeRequired[string](session, "diff_source")
	if err != nil {
		return nil, err
	}
	if source != diffSource {
		return nil, nil
	}
	selected, err := matchesSelector(session, selector, slug, relative)
	if err != nil {
		return nil, err
	}
	if !selected {
		return nil, nil
	}
	if err := validateSessionRepo(session, repo); err != nil {
		return nil, err
	}
	return decodeFileSession(session, repo)
}

func decodeFileSessionIdentity(session object, repo string) (*SavedReview, error) {
	version, err := decodeRequired[string](session, "version")
	if err != nil {
		return nil, err
	}
	if version != "1.3" {
		return nil, fmt.Errorf("unsupported saved-session version %q", version)
	}
	if raw, ok := session["pr_session_key"]; ok {
		if string(raw) != "null" {
			return nil, fmt.Errorf("expected local file session")
		}
	}
	id, err := decodeRequired[string](session, "id")
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, fmt.Errorf("missing session ID")
	}
	return &SavedReview{Scope: Stack{Repo: repo}, ID: id, Version: version}, nil
}

func decodeFileSession(session object, repo string) (*SavedReview, error) {
	saved, err := decodeFileSessionIdentity(session, repo)
	if err != nil {
		return nil, err
	}
	files, err := decodeRequired[object](session, "files")
	if err != nil {
		return nil, err
	}
	collector := commentCollector{comments: []map[string]any{}, ids: map[string]string{}}
	if raw, ok := session["review_comments"]; ok {
		if err := collector.add(raw, "review", "", ""); err != nil {
			return nil, err
		}
	}
	reviewed, err := collector.collectFiles(files)
	if err != nil {
		return nil, err
	}
	notes, err := sessionNotes(session)
	if err != nil {
		return nil, err
	}
	saved.Reviewed = reviewed
	saved.Files = len(files)
	saved.Complete = len(files) > 0 && allReviewed(reviewed, len(files))
	saved.Comments = collector.comments
	saved.SessionNotes = notes
	return saved, nil
}

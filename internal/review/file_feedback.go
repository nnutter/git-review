package review

import (
	"fmt"
	"path/filepath"
	"strings"
)

func readFileFeedbackEntry(store, repo string, entry object) (*FilesResult, error) {
	canonical, relativeScope, err := localFileFeedbackScope(repo, entry)
	if err != nil {
		return nil, err
	}
	if canonical == "" {
		return nil, nil
	}
	session, path, err := readFileFeedbackSession(store, entry)
	if err != nil {
		return nil, err
	}
	source, err := fileFeedbackSource(session, relativeScope)
	if err != nil {
		return nil, err
	}
	if source == "" {
		return nil, nil
	}
	if err := validateSessionRepo(session, filepath.Clean(canonical)); err != nil {
		return nil, err
	}
	saved, err := decodeFileFeedbackSession(session, canonical, source)
	if err != nil {
		return nil, err
	}
	if saved.Status() != "feedback" {
		return nil, nil
	}
	result := FilesResult{Version: 1, Command: "files", Repo: repo, Session: path, AllFiles: source == "pristine"}
	if relativeScope != "." {
		result.Path = filepath.ToSlash(relativeScope)
	}
	result, err = fileReviewResult(result, saved)
	return &result, err
}

func localFileFeedbackScope(repo string, entry object) (string, string, error) {
	kind, err := decodeRequired[object](entry, "kind")
	if err != nil {
		return "", "", err
	}
	entryType, err := decodeRequired[string](kind, "type")
	if err != nil {
		return "", "", err
	}
	if entryType != "local" {
		return "", "", nil
	}
	canonical, err := decodeRequired[string](entry, "canonical_repo_path")
	if err != nil {
		return "", "", err
	}
	relativeScope, err := filepath.Rel(repo, canonical)
	if err != nil {
		return "", "", err
	}
	if relativeScope == ".." || strings.HasPrefix(relativeScope, ".."+string(filepath.Separator)) {
		return "", "", nil
	}
	return canonical, relativeScope, nil
}

func readFileFeedbackSession(store string, entry object) (object, string, error) {
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
	session, err := readObject(path)
	return session, path, err
}

func fileFeedbackSource(session object, relativeScope string) (string, error) {
	if raw, ok := session["pr_session_key"]; ok {
		if string(raw) != "null" {
			return "", nil
		}
	}
	source, err := decodeRequired[string](session, "diff_source")
	if err != nil {
		return "", err
	}
	switch source {
	case "pristine", "working_tree":
		return source, nil
	case "commit_range":
		// Branch diffs launched by files always use the feature repository root.
		if relativeScope == "." {
			return source, nil
		}
	}
	return "", nil
}

func decodeFileFeedbackSession(session object, canonical, source string) (*SavedReview, error) {
	if source != "commit_range" {
		return decodeFileSession(session, canonical)
	}
	commits, err := decodeRequired[[]string](session, "commit_range")
	if err != nil {
		return nil, err
	}
	if err := validateCommits(commits); err != nil {
		return nil, err
	}
	return decodeSession(session, Stack{Repo: canonical, Commits: commits})
}

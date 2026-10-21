package review

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func fileFixture(t *testing.T, repo, diffSource, base string, store string, reviewed bool, sessionFile string) map[string]any {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(store, "sessions"), 0o700))
	session := map[string]any{
		"version": "1.3", "id": "file-session", "repo_path": repo,
		"base_commit": base, "diff_source": diffSource, "commit_range": nil,
		"files": map[string]any{
			"file.txt": map[string]any{"path": "file.txt", "reviewed": reviewed, "file_comments": []any{}, "line_comments": map[string]any{}},
		},
	}
	index := map[string]any{"version": "2.0", "entries": map[string]any{"fixture": []any{map[string]any{
		"kind": map[string]any{"type": "local"}, "canonical_repo_path": repo, "path": sessionFile,
	}}}}
	writeJSON(t, filepath.Join(store, "index.json"), index)
	writeJSON(t, filepath.Join(store, sessionFile), session)
	return session
}

func TestReadFilePristineAndWorkingTree(t *testing.T) {
	r := testRepo(t)
	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Feature")
	repo, err := r.git(t.Context(), "rev-parse", "--show-toplevel")
	require.NoError(t, err)
	for _, tc := range []struct {
		name       string
		diffSource string
		base       string
	}{
		{"pristine", "pristine", "pristine:abc:def"},
		{"working_tree", "working_tree", "file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := t.TempDir()
			fileFixture(t, repo, tc.diffSource, tc.base, store, true, "sessions/0123456789abcdef.json")
			saved, sessionPath, err := (SavedReader{Stores: []string{store}}).ReadFile(repo, tc.diffSource, "")
			require.NoError(t, err)
			require.NotNil(t, saved)
			require.Equal(t, "file-session", saved.ID)
			require.Equal(t, "approved", saved.Status())
			require.NotEmpty(t, sessionPath)
			// Wrong diff source finds nothing.
			missing, _, err := (SavedReader{Stores: []string{store}}).ReadFile(repo, "commit_range", "")
			require.NoError(t, err)
			require.Nil(t, missing)
		})
	}
}

func TestReadFileOutcomes(t *testing.T) {
	r := testRepo(t)
	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Feature")
	repo, err := r.git(t.Context(), "rev-parse", "--show-toplevel")
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		status string
	}{
		{"approved", func(map[string]any) {}, "approved"},
		{"feedback comment", func(s map[string]any) {
			s["review_comments"] = []any{map[string]any{"id": "c1", "content": "Fix this", "comment_type": "none"}}
		}, "feedback"},
		{"feedback notes", func(s map[string]any) { s["session_notes"] = "Revise" }, "feedback"},
		{"incomplete", func(s map[string]any) {
			s["files"].(map[string]any)["file.txt"].(map[string]any)["reviewed"] = false
		}, "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := t.TempDir()
			session := fileFixture(t, repo, "pristine", "pristine:abc:def", store, true, "sessions/0123456789abcdef.json")
			tc.mutate(session)
			writeJSON(t, filepath.Join(store, "sessions/0123456789abcdef.json"), session)
			saved, _, err := (SavedReader{Stores: []string{store}}).ReadFile(repo, "pristine", "")
			require.NoError(t, err)
			require.NotNil(t, saved)
			require.Equal(t, tc.status, saved.Status())
		})
	}
}

func TestReadFileAmbiguousRequiresSession(t *testing.T) {
	r := testRepo(t)
	gitTest(t, r.Dir, "commit", "--allow-empty", "-m", "Feature")
	repo, err := r.git(t.Context(), "rev-parse", "--show-toplevel")
	require.NoError(t, err)
	store := t.TempDir()
	fileFixture(t, repo, "working_tree", "file", store, true, "sessions/0123456789abcdef.json")
	session := map[string]any{
		"version": "1.3", "id": "second-session", "repo_path": repo,
		"base_commit": "file", "diff_source": "working_tree", "commit_range": nil,
		"files": map[string]any{
			"other.txt": map[string]any{"path": "other.txt", "reviewed": true, "file_comments": []any{}, "line_comments": map[string]any{}},
		},
	}
	writeJSON(t, filepath.Join(store, "sessions/fedcba9876543210.json"), session)
	writeJSON(t, filepath.Join(store, "index.json"), map[string]any{"version": "2.0", "entries": map[string]any{"fixture": []any{
		map[string]any{"kind": map[string]any{"type": "local"}, "canonical_repo_path": repo, "path": "sessions/0123456789abcdef.json"},
		map[string]any{"kind": map[string]any{"type": "local"}, "canonical_repo_path": repo, "path": "sessions/fedcba9876543210.json"},
	}}})
	_, _, err = (SavedReader{Stores: []string{store}}).ReadFile(repo, "working_tree", "")
	require.ErrorContains(t, err, "ambiguous")
	saved, _, err := (SavedReader{Stores: []string{store}}).ReadFile(repo, "working_tree", "second-session")
	require.NoError(t, err)
	require.NotNil(t, saved)
	require.Equal(t, "second-session", saved.ID)
}

func TestFilesReviewArgMappingAndLock(t *testing.T) {
	reviewer, _, _ := testReviewer(t)
	r := reviewer.Service.Repository
	require.NoError(t, os.WriteFile(filepath.Join(r.Dir, "file.txt"), []byte("File to review\n"), 0o600))
	repo, err := r.git(t.Context(), "rev-parse", "--show-toplevel")
	require.NoError(t, err)
	// Untracked with explicit path is an error without launching tuicr.
	_, err = FileReviewer{Service: reviewer.Service, Reader: reviewer.Reader, Input: strings.NewReader(""), Terminal: io.Discard}.Review(t.Context(), FilesOptions{Path: "file.txt", Untracked: true})
	require.ErrorContains(t, err, "--untracked")
	for _, tc := range []struct {
		name       string
		options    FilesOptions
		wantArgs   string
		diffSource string
		base       string
	}{
		{"all files", FilesOptions{All: true}, "--all-files", "pristine", "pristine:abc:def"},
		{"single file", FilesOptions{Path: "file.txt"}, "--file\nfile.txt", "working_tree", "file"},
		{"untracked defaults to dot", FilesOptions{Untracked: true}, "--file\n.", "working_tree", "file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := filepath.Join(t.TempDir(), "launch")
			t.Setenv("TEST_LAUNCH_LOG", log)
			fakeTuicr(t, `printf '%s\n' "$@" > "$TEST_LAUNCH_LOG"`)
			fileStore := t.TempDir()
			fileFixture(t, repo, tc.diffSource, tc.base, fileStore, true, "sessions/0123456789abcdef.json")
			fr := FileReviewer{Service: reviewer.Service, Reader: SavedReader{Stores: []string{fileStore}}, Input: strings.NewReader(""), Terminal: io.Discard}
			result, err := fr.Review(t.Context(), tc.options)
			require.NoError(t, err)
			require.Equal(t, "files", result.Command)
			require.Equal(t, "approved", result.Status)
			require.NotNil(t, result.Review)
			launch, err := os.ReadFile(log)
			require.NoError(t, err)
			require.Contains(t, string(launch), "tui\n--no-update-check\n--stdout")
			require.Contains(t, string(launch), tc.wantArgs)
		})
	}
}

func TestFilesReviewCancelledWithoutSession(t *testing.T) {
	reviewer, _, _ := testReviewer(t)
	fakeTuicr(t, "exit 0")
	store := t.TempDir()
	fr := FileReviewer{Service: reviewer.Service, Reader: SavedReader{Stores: []string{store}}, Input: strings.NewReader(""), Terminal: io.Discard}
	result, err := fr.Review(t.Context(), FilesOptions{})
	require.NoError(t, err)
	require.Equal(t, "files", result.Command)
	require.Equal(t, "cancelled", result.Status)
	require.Nil(t, result.Review)
}

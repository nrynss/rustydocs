package git

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/testutil"
)

// TestGetFileLastModified_SymlinkedPath is a regression test: when the file path
// reaches the repo through a symlink, the path relative to the (symlink-resolved)
// git root must still resolve, so a tracked file is not reported as having no
// history.
func TestGetFileLastModified_SymlinkedPath(t *testing.T) {
	repo := testutil.NewRepo(t) // repo.Dir is already symlink-resolved
	when := time.Now().AddDate(0, 0, -10)
	repo.Commit(when, "init", map[string]string{"docs/a.md": "# A\n\nbody\n"})

	// Reach the same file through a symlink to the repo root.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo.Dir, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	viaLink := filepath.Join(link, "docs", "a.md")

	info, err := GetFileLastModified(viaLink)
	if err != nil {
		t.Fatalf("GetFileLastModified through symlink: %v", err)
	}
	if info == nil {
		t.Fatal("tracked file reported as having no history when reached via a symlink")
	}
	if info.LastAuthor != "Test User" {
		t.Errorf("LastAuthor = %q, want %q", info.LastAuthor, "Test User")
	}
}

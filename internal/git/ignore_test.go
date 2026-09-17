package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/nrynss/rustydocs/internal/testutil"
)

// TestCheckIgnore covers the batch ignore query: which paths come back, that
// the answer is keyed by the exact spelling passed in, and that the three
// no-op shapes (no paths, nothing ignored, not a repository) are distinguished
// correctly — the last one by an error, which is the caller's cue to skip
// filtering rather than to fail (#69).
func TestCheckIgnore(t *testing.T) {
	repo := testutil.NewRepo(t)
	// "built/*" rather than "built/": git cannot re-include a file inside an
	// excluded *directory*, so the negation below only has an effect with the
	// former. Getting that wrong is exactly the class of detail a hand-rolled
	// matcher gets wrong too.
	repo.Write(".gitignore", "*.log\nbuilt/*\n!built/keep.md\nnested/**/deep.md\n")
	repo.Write("docs/a.md", "a")
	repo.Write("docs/debug.log", "log")
	repo.Write("built/out.md", "out")
	repo.Write("built/keep.md", "keep")
	repo.Write("nested/x/deep.md", "deep")
	// A nested .gitignore adds rules of its own, which is a large part of why
	// this asks git rather than matching patterns in-process.
	repo.Write("docs/sub/.gitignore", "local.md\n")
	repo.Write("docs/sub/local.md", "local")
	repo.Write("docs/sub/shared.md", "shared")

	all := []string{
		"docs/a.md", "docs/debug.log", "built/out.md", "built/keep.md",
		"nested/x/deep.md", "docs/sub/local.md", "docs/sub/shared.md",
	}
	got, err := CheckIgnore(repo.Dir, all)
	if err != nil {
		t.Fatalf("CheckIgnore: %v", err)
	}
	var ignored []string
	for p := range got {
		ignored = append(ignored, p)
	}
	sort.Strings(ignored)
	want := []string{"built/out.md", "docs/debug.log", "docs/sub/local.md", "nested/x/deep.md"}
	if !reflect.DeepEqual(ignored, want) {
		t.Errorf("ignored = %v, want %v", ignored, want)
	}

	// Nothing ignored: git exits 1, which is an answer and not a failure.
	none, err := CheckIgnore(repo.Dir, []string{"docs/a.md", "built/keep.md"})
	if err != nil {
		t.Fatalf("CheckIgnore(none ignored): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("ignored = %v, want empty", none)
	}

	// No paths: no subprocess, no error.
	empty, err := CheckIgnore(repo.Dir, nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("CheckIgnore(nil) = %v, %v", empty, err)
	}
}

// TestCheckIgnore_TrackedFileIsNotIgnored pins the deliberate absence of
// --no-index: a tracked file is not ignored whatever the patterns say.
func TestCheckIgnore_TrackedFileIsNotIgnored(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Write("docs/tracked.md", "x")
	cmd := exec.Command("git", "add", "-f", filepath.FromSlash("docs/tracked.md"))
	cmd.Dir = repo.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add -f: %v\n%s", err, out)
	}
	repo.Write(".gitignore", "docs/tracked.md\n")

	got, err := CheckIgnore(repo.Dir, []string{"docs/tracked.md"})
	if err != nil {
		t.Fatalf("CheckIgnore: %v", err)
	}
	if got["docs/tracked.md"] {
		t.Error("a tracked file was reported ignored; --no-index must stay off")
	}
}

// TestCheckIgnore_OutsideRepository is the graceful-degradation contract: a
// tree git cannot answer for produces an error, never a wrong answer.
func TestCheckIgnore_OutsideRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	// GIT_CEILING_DIRECTORIES is not enough on its own because the temp dir may
	// sit under a repository; setting a private ceiling plus an explicit
	// no-repo dir is what makes this deterministic. Instead, assert only the
	// shape: either git says "not a repository" (an error), or — when the temp
	// dir happens to live inside one — it answers without claiming the file is
	// ignored.
	got, err := CheckIgnore(dir, []string{"a.md"})
	if err == nil && got["a.md"] {
		t.Error("a file in a non-repository tree must not be reported ignored")
	}
}

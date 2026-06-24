// Package testutil provides shared helpers for tests that need a real git
// repository with controlled commit dates, so blame/log timestamps are
// deterministic and staleness math can be asserted exactly.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Repo is a temporary git repository rooted in a test temp dir.
type Repo struct {
	t   *testing.T
	Dir string
}

// NewRepo creates an initialized git repository in a fresh temp directory.
// It skips the test (rather than failing) when git is not on PATH, so the
// suite stays green in minimal environments.
func NewRepo(t *testing.T) *Repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	// Canonicalize the temp dir so it matches `git rev-parse --show-toplevel`.
	// On macOS t.TempDir() lives under /var/... which is a symlink to
	// /private/var/...; without this, relative-path computation against the git
	// root mismatches.
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	r := &Repo{t: t, Dir: dir}
	r.run(nil, "init", "-q")
	r.run(nil, "config", "user.email", "test@example.com")
	r.run(nil, "config", "user.name", "Test User")
	r.run(nil, "config", "commit.gpgsign", "false")
	return r
}

// run executes a git command in the repo, failing the test on error. Extra
// environment entries (e.g. commit dates) are appended to the inherited env.
func (r *Repo) run(env []string, args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// Write creates or overwrites a file under the repo without committing it.
// rel uses forward slashes. It returns the absolute path.
func (r *Repo) Write(rel, content string) string {
	r.t.Helper()
	p := r.Path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		r.t.Fatal(err)
	}
	return p
}

// Commit writes the given files (rel path -> content) and commits them all with
// the supplied author/committer date, so blame and log report that timestamp.
func (r *Repo) Commit(when time.Time, msg string, files map[string]string) {
	r.t.Helper()
	for rel, content := range files {
		r.Write(rel, content)
	}
	r.run(nil, "add", "-A")
	stamp := when.Format(time.RFC3339)
	r.run([]string{
		"GIT_AUTHOR_DATE=" + stamp,
		"GIT_COMMITTER_DATE=" + stamp,
	}, "commit", "-q", "-m", msg)
}

// Path returns the absolute path to a file under the repo (rel uses slashes).
func (r *Repo) Path(rel string) string {
	return filepath.Join(r.Dir, filepath.FromSlash(rel))
}

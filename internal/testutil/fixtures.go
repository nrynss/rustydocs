package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// fixturesRoot returns the absolute path to the testdata directory that ships
// alongside this file, independent of the test's working directory.
func fixturesRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata")
}

// FixturePath returns the absolute path to a fixture under testutil/testdata
// (rel uses forward slashes).
func FixturePath(rel string) string {
	return filepath.Join(fixturesRoot(), filepath.FromSlash(rel))
}

// ReadFixture returns the contents of a fixture file (rel uses forward slashes).
func ReadFixture(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(FixturePath(rel))
	if err != nil {
		t.Fatalf("read fixture %q: %v", rel, err)
	}
	return string(data)
}

// CommitTree copies an entire fixture subtree into the repo under destPrefix
// ("." for the repo root) and commits it with the given author/committer date.
func (r *Repo) CommitTree(when time.Time, msg, fixtureSubdir, destPrefix string) {
	r.t.Helper()
	src := FixturePath(fixtureSubdir)
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(src, p)
		if relErr != nil {
			return relErr
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		r.Write(filepath.ToSlash(filepath.Join(destPrefix, rel)), string(data))
		return nil
	})
	if err != nil {
		r.t.Fatalf("copy fixture tree %q: %v", fixtureSubdir, err)
	}
	r.run(nil, "add", "-A")
	stamp := when.Format(time.RFC3339)
	r.run([]string{
		"GIT_AUTHOR_DATE=" + stamp,
		"GIT_COMMITTER_DATE=" + stamp,
	}, "commit", "-q", "-m", msg)
}

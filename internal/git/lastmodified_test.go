package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/testutil"
)

func lastModifiedTestEnvironment(t testing.TB) {
	t.Helper()
	for _, name := range []string{
		"GIT_CONFIG", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS",
		"GIT_LITERAL_PATHSPECS", "GIT_GLOB_PATHSPECS", "GIT_NOGLOB_PATHSPECS", "GIT_ICASE_PATHSPECS",
	} {
		unsetLastModifiedTestEnvironment(t, name)
	}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_") {
			unsetLastModifiedTestEnvironment(t, name)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func unsetLastModifiedTestEnvironment(t testing.TB, name string) {
	t.Helper()
	value, wasSet := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unset %s: %v", name, err)
	}
	t.Cleanup(func() {
		var err error
		if wasSet {
			err = os.Setenv(name, value)
		} else {
			err = os.Unsetenv(name)
		}
		if err != nil {
			t.Errorf("restore %s: %v", name, err)
		}
	})
}

func lastModifiedTestGit(t *testing.T, root string, env []string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return out
}

func requireLastModified(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command("git", "last-modified", "-r", "-z", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("Git lacks last-modified -r -z: %v", err)
	}
	if _, err := parseLastModified(out); err != nil {
		t.Skipf("Git lacks supported last-modified output: %v", err)
	}
}

func assertLastModifiedBaseline(t *testing.T, c *FileInfoCache, paths ...string) {
	t.Helper()
	for _, path := range paths {
		want, wantErr := GetFileLastModified(path)
		got, gotErr := c.FileLastModified(path)
		if (gotErr == nil) != (wantErr == nil) || !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: cached (%+v, %v), baseline (%+v, %v)", path, got, gotErr, want, wantErr)
		}
	}
}

// This seam observes actual accelerator subprocesses without replacing the
// lookup seam (which deliberately bypasses the accelerator).
func observeLastModified(c *FileInfoCache) (map[string]int, *sync.Mutex) {
	counts := make(map[string]int)
	mu := new(sync.Mutex)
	c.runGit = func(root, input string, args ...string) ([]byte, error) {
		mu.Lock()
		counts[args[0]]++
		mu.Unlock()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Stdin = strings.NewReader(input)
		return cmd.Output()
	}
	return counts, mu
}

// batchForPath uses the actual Git root key, not a fixture's native path
// spelling. Git reports forward slashes on Windows; filepath uses backslashes.
func batchForPath(t testing.TB, c *FileInfoCache, path string) *lastModifiedEntry {
	t.Helper()
	root, err := c.rootFor(path)
	if err != nil {
		t.Fatalf("resolve batch repository: %v", err)
	}
	return c.repositories[root]
}

func TestFileInfoCache_DefaultDoesNotProbeLastModified(t *testing.T) {
	repo := newSnippetRepo(t)
	for _, c := range []*FileInfoCache{NewFileInfoCache(), {}} {
		c.runGit = func(string, string, ...string) ([]byte, error) {
			t.Fatal("default cache issued an accelerator probe")
			return nil, nil
		}
		assertLastModifiedBaseline(t, c, repo.Path("snippets/shared.mdx"))
		if len(c.repositories) != 0 || len(c.directories) != 0 {
			t.Fatal("default cache created accelerator state")
		}
	}
}

func TestFileInfoCache_LastModifiedRespectsInjectedLookup(t *testing.T) {
	c := NewFileInfoCacheWithLastModified()
	c.runGit = func(string, string, ...string) ([]byte, error) {
		t.Fatal("injected lookup issued an accelerator probe")
		return nil, nil
	}
	calls := 0
	c.lookup = func(path string) (*FileInfo, error) {
		calls++
		return &FileInfo{Path: path, LastAuthor: "injected"}, nil
	}
	for i := 0; i < 2; i++ {
		info, err := c.FileLastModified("injected.md")
		if err != nil || info.LastAuthor != "injected" {
			t.Fatalf("injection lost: %+v, %v", info, err)
		}
	}
	if calls != 1 {
		t.Fatalf("injected calls = %d", calls)
	}
}

func TestFileInfoCache_LastModifiedLinearHistory(t *testing.T) {
	lastModifiedTestEnvironment(t)
	repo := testutil.NewRepo(t)
	files := map[string]string{
		"docs/old.md": "original\n", "docs/revised.md": "original\n",
		"docs/space name.md": "space\n", "docs/日本語.md": "unicode\n",
		"docs/magic[1].md": "magic\n", "docs/magic1.md": "matched by pathspec\n",
		"docs/removed.md": "delete later\n", "docs/rename.md": "rename later\n",
		"docs/mode.md": "mode\n",
	}
	if runtime.GOOS != "windows" {
		files["docs/tab\tname.md"] = "tab\n"
		files["docs/new\nline.md"] = "newline\n"
		files["docs/back\\slash.md"] = "backslash\n"
	}
	repo.Commit(commitTime, "initial", files)
	repo.Write("docs/revised.md", "revised\n")
	lastModifiedTestGit(t, repo.Dir, nil, "rm", "docs/removed.md")
	lastModifiedTestGit(t, repo.Dir, nil, "mv", "docs/rename.md", "docs/renamed.md")
	lastModifiedTestGit(t, repo.Dir, nil, "update-index", "--chmod=+x", "docs/mode.md")
	lastModifiedTestGit(t, repo.Dir, nil, "add", "docs/revised.md")
	lastModifiedTestGit(t, repo.Dir, []string{
		"GIT_AUTHOR_NAME=Different Author", "GIT_AUTHOR_DATE=2024-04-05T12:13:14+0530",
		"GIT_COMMITTER_DATE=2025-08-09T01:02:03Z",
	}, "commit", "-q", "-m", "modify rename remove mode")
	requireLastModified(t, repo.Dir)
	c := NewFileInfoCacheWithLastModified()
	counts, _ := observeLastModified(c)
	for rel := range files {
		assertLastModifiedBaseline(t, c, repo.Path(rel))
	}
	assertLastModifiedBaseline(t, c, repo.Path("docs/renamed.md"), repo.Path("docs"),
		repo.Write("docs/draft.md", "untracked\n"), repo.Path("docs/absent.md"))
	if counts["last-modified"] != 1 || counts["log"] != 1 {
		t.Fatalf("batch commands = %v, want one last-modified and one metadata log", counts)
	}
	if entry := batchForPath(t, c, repo.Path("docs/revised.md")); entry == nil || len(entry.files) == 0 {
		t.Fatal("real Git batch was not enabled")
	}
	info, err := c.FileLastModified(repo.Path("docs/revised.md"))
	if err != nil || info.LastAuthor != "Different Author" || info.LastModified.Year() != 2024 {
		t.Fatalf("author evidence lost: %+v, %v", info, err)
	}
}

func TestFileInfoCache_LastModifiedConcurrentRepositories(t *testing.T) {
	lastModifiedTestEnvironment(t)
	repos := []*testutil.Repo{testutil.NewRepo(t), testutil.NewRepo(t)}
	for i, repo := range repos {
		files := make(map[string]string)
		for j := 0; j < 12; j++ {
			files[fmt.Sprintf("snippets/%d.md", j)] = fmt.Sprintf("repository %d\n", i)
		}
		repo.Commit(commitTime.Add(time.Duration(i)*time.Hour), "initial", files)
		requireLastModified(t, repo.Dir)
	}
	c := NewFileInfoCacheWithLastModified()
	counts, mu := observeLastModified(c)
	var wg sync.WaitGroup
	for _, repo := range repos {
		for j := 0; j < 12; j++ {
			for n := 0; n < 3; n++ {
				wg.Add(1)
				go func(path string) {
					defer wg.Done()
					assertLastModifiedBaseline(t, c, path)
				}(repo.Path(fmt.Sprintf("snippets/%d.md", j)))
			}
		}
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if counts["last-modified"] != 2 || counts["log"] != 2 {
		t.Fatalf("commands = %v, want one batch per repository", counts)
	}
	if hits, misses := c.Stats(); hits != 48 || misses != 24 {
		t.Fatalf("entry stats = %d, %d", hits, misses)
	}
}

func TestFileInfoCache_LastModifiedFallbackOnce(t *testing.T) {
	lastModifiedTestEnvironment(t)
	repo := newSnippetRepo(t)
	repo.Commit(commitTime.Add(time.Hour), "second", map[string]string{"snippets/second.mdx": "body\n"})
	for _, fault := range []string{"unsupported", "old-format", "malformed", "duplicate", "metadata-failed", "metadata-missing", "metadata-invalid", "metadata-partial"} {
		t.Run(fault, func(t *testing.T) {
			c := NewFileInfoCacheWithLastModified()
			var mu sync.Mutex
			batchCalls, metadataCalls := 0, 0
			c.runGit = func(root, input string, args ...string) ([]byte, error) {
				mu.Lock()
				defer mu.Unlock()
				switch args[0] {
				case "config", "rev-list", "replace":
					return nil, nil
				case "rev-parse":
					return []byte("false\n.git/info/grafts\n"), nil
				case "last-modified":
					batchCalls++
					oid := strings.Repeat("a", 40)
					switch fault {
					case "unsupported":
						return nil, errors.New("unknown command")
					case "old-format":
						return []byte(oid + "\tsnippets/shared.mdx\n"), nil
					case "malformed":
						return []byte("bad\tsnippets/shared.mdx\x00"), nil
					case "duplicate":
						return []byte(strings.Repeat(oid+"\tsnippets/shared.mdx\x00", 2)), nil
					}
					if fault == "metadata-partial" {
						return []byte(oid + "\tsnippets/shared.mdx\x00" + strings.Repeat("b", 40) + "\tsnippets/second.mdx\x00"), nil
					}
					return []byte(oid + "\tsnippets/shared.mdx\x00" + oid + "\tsnippets/second.mdx\x00"), nil
				case "log":
					metadataCalls++
					switch fault {
					case "metadata-failed":
						return nil, errors.New("metadata command failed")
					case "metadata-missing":
						return nil, nil
					case "metadata-invalid":
						return []byte(strings.Repeat("a", 40) + "\x00Author\x00invalid date\x00\n"), nil
					case "metadata-partial":
						return []byte(strings.Repeat("a", 40) + "\x00Author\x002024-01-01 00:00:00 +0000\x00\ntruncated"), nil
					default:
						return []byte("truncated"), nil
					}
				default:
					return nil, fmt.Errorf("unexpected command %v", args)
				}
			}
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					assertLastModifiedBaseline(t, c, repo.Path("snippets/shared.mdx"), repo.Path("snippets/second.mdx"))
				}()
			}
			wg.Wait()
			wantMetadataCalls := 0
			if strings.HasPrefix(fault, "metadata-") {
				wantMetadataCalls = 1
			}
			if batchCalls != 1 || metadataCalls != wantMetadataCalls {
				t.Fatalf("batch/metadata calls = %d/%d", batchCalls, metadataCalls)
			}
		})
	}
}

func TestLastModifiedParsers(t *testing.T) {
	oid := strings.Repeat("a", 40)
	for _, output := range []string{"", oid + "\ta.md", "^" + oid + "\ta.md\x00", oid + "\t../a.md\x00", oid + "\t/a.md\x00", oid + "\ta/../b.md\x00", oid + "\t\x00", oid + "\ta.md\x00\x00", strings.Repeat("0", 40) + "\ta.md\x00"} {
		if _, err := parseLastModified([]byte(output)); err == nil {
			t.Errorf("accepted invalid output %q", output)
		}
	}
	for _, output := range []string{"", oid + "\x00A\x00bad\x00\n", oid + "\x00A\x002024-01-01 00:00:00 +0000\x00", oid + "\x00A\nB\x002024-01-01 00:00:00 +0000\x00\n", strings.Repeat("b", 40) + "\x00A\x002024-01-01 00:00:00 +0000\x00\n"} {
		if _, err := parseLastModifiedMetadata([]byte(output), map[string]bool{oid: true}); err == nil {
			t.Errorf("accepted invalid metadata %q", output)
		}
	}
	sha256 := strings.Repeat("b", 64)
	if paths, err := parseLastModified([]byte(sha256 + "\ta\tb\nc.md\x00")); err != nil || paths["a\tb\nc.md"] != sha256 {
		t.Fatalf("SHA256/special path rejected: %v, %v", paths, err)
	}
	metadata := []byte(oid + "\x00A\x002024-01-01 00:00:00 +0000\x00\n")
	if _, err := parseLastModifiedMetadata(bytes.Repeat(metadata, 2), map[string]bool{oid: true}); err == nil {
		t.Fatal("duplicate metadata accepted")
	}
}

func TestFileInfoCache_LastModifiedSymlinksAndSubmodule(t *testing.T) {
	lastModifiedTestEnvironment(t)
	target := newSnippetRepo(t)
	outer := newSnippetRepo(t)
	requireLastModified(t, target.Dir)
	link := outer.Path("foreign.mdx")
	if err := os.Symlink(target.Path("snippets/shared.mdx"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	alias := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(target.Dir, alias); err != nil {
		t.Fatal(err)
	}
	localLink := target.Path("local.mdx")
	if err := os.Symlink("snippets/shared.mdx", localLink); err != nil {
		t.Fatal(err)
	}
	lastModifiedTestGit(t, outer.Dir, nil, "-c", "protocol.file.allow=always", "submodule", "add", "-q", target.Dir, "nested")
	lastModifiedTestGit(t, outer.Dir, nil, "commit", "-q", "-am", "add submodule")
	for _, paths := range [][]string{
		{target.Path("snippets/shared.mdx"), link},
		{link, target.Path("snippets/shared.mdx")},
	} {
		c := NewFileInfoCacheWithLastModified()
		assertLastModifiedBaseline(t, c, paths...)
		assertLastModifiedBaseline(t, c, localLink, filepath.Join(alias, "snippets/shared.mdx"),
			outer.Path("nested/snippets/shared.mdx"), outer.Path("nested"))
		if len(c.repositories) != 2 {
			// Foreign targets are rejected before a batch; outer is queried as
			// a directory, also baseline. Target and nested repo each get one.
			t.Fatalf("repository batches = %d, want 2", len(c.repositories))
		}
	}
}

func TestFileInfoCache_LastModifiedUnsafeHistories(t *testing.T) {
	lastModifiedTestEnvironment(t)
	for _, kind := range []string{"merge", "log-root", "log-follow", "diff-config", "replace", "grafts", "shallow", "pathspec-env", "git-config-env"} {
		t.Run(kind, func(t *testing.T) {
			repo := newSnippetRepo(t)
			repo.Commit(commitTime.Add(time.Hour), "second", map[string]string{"other.md": "other\n"})
			switch kind {
			case "merge":
				lastModifiedTestGit(t, repo.Dir, nil, "checkout", "-q", "-b", "side", "HEAD~1")
				repo.Commit(commitTime.Add(2*time.Hour), "side", map[string]string{"side.md": "side\n"})
				lastModifiedTestGit(t, repo.Dir, nil, "checkout", "-q", "-")
				lastModifiedTestGit(t, repo.Dir, nil, "merge", "-q", "--no-ff", "-m", "merge side", "side")
			case "log-root":
				lastModifiedTestGit(t, repo.Dir, nil, "config", "log.showRoot", "false")
			case "log-follow":
				lastModifiedTestGit(t, repo.Dir, nil, "config", "log.follow", "true")
			case "diff-config":
				lastModifiedTestGit(t, repo.Dir, nil, "config", "diff.renames", "false")
			case "replace":
				lastModifiedTestGit(t, repo.Dir, nil, "replace", "HEAD", "HEAD~1")
			case "grafts":
				if err := os.WriteFile(repo.Path(".git/info/grafts"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "shallow":
				clone := filepath.Join(t.TempDir(), "clone")
				lastModifiedTestGit(t, repo.Dir, nil, "clone", "-q", "--depth=1", "file://"+filepath.ToSlash(repo.Dir), clone)
				resolved, err := filepath.EvalSymlinks(clone)
				if err != nil {
					t.Fatal(err)
				}
				repo.Dir = resolved
			case "pathspec-env":
				t.Setenv("GIT_ICASE_PATHSPECS", "1")
			case "git-config-env":
				t.Setenv("GIT_CONFIG", os.DevNull)
				t.Setenv("GIT_CONFIG_COUNT", "1")
				t.Setenv("GIT_CONFIG_KEY_0", "log.follow")
				t.Setenv("GIT_CONFIG_VALUE_0", "true")
			}
			c := NewFileInfoCacheWithLastModified()
			counts, _ := observeLastModified(c)
			assertLastModifiedBaseline(t, c, repo.Path("snippets/shared.mdx"), repo.Path("other.md"))
			entry := batchForPath(t, c, repo.Path("snippets/shared.mdx"))
			if counts["last-modified"] != 0 || len(c.repositories) != 1 || entry == nil || entry.files != nil {
				t.Fatalf("unsafe history used batch: %v", counts)
			}
		})
	}
}

func TestFileInfoCache_LastModifiedSHA256(t *testing.T) {
	lastModifiedTestEnvironment(t)
	repo := testutil.NewRepo(t) // supplies configured identity for the new repo
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-q", "--object-format=sha256", resolved)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("Git lacks SHA256 repositories: %v: %s", err, out)
	}
	repo.Dir = resolved
	lastModifiedTestGit(t, repo.Dir, nil, "config", "user.name", "SHA256 Author")
	lastModifiedTestGit(t, repo.Dir, nil, "config", "user.email", "sha256@example.com")
	lastModifiedTestGit(t, repo.Dir, nil, "config", "commit.gpgsign", "false")
	repo.Commit(commitTime, "initial", map[string]string{"doc.md": "body\n"})
	requireLastModified(t, repo.Dir)
	c := NewFileInfoCacheWithLastModified()
	assertLastModifiedBaseline(t, c, repo.Path("doc.md"))
	info, err := c.FileLastModified(repo.Path("doc.md"))
	entry := batchForPath(t, c, repo.Path("doc.md"))
	if err != nil || info == nil || len(info.LastCommit) != 64 || entry == nil || len(entry.files) != 1 {
		t.Fatalf("SHA256 batch failed: %+v, %v", info, err)
	}
}

func TestFileInfoCache_LastModifiedAuthorFormatting(t *testing.T) {
	lastModifiedTestEnvironment(t)
	repo := newSnippetRepo(t)
	requireLastModified(t, repo.Dir)
	lastModifiedTestGit(t, repo.Dir, nil, "config", "i18n.logOutputEncoding", "ISO-8859-1")
	// Raw objects let us retain author whitespace that `git commit` cleans.
	// The baseline's TrimSpace affects only the complete log output, leaving
	// whitespace on its internal author line untouched.
	for _, name := range []string{"  Leading and trailing  ", "\tTabbed Author\t", "René Author"} {
		raw := lastModifiedTestGit(t, repo.Dir, nil, "cat-file", "commit", "HEAD")
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "author ") {
				_, suffix, _ := strings.Cut(line, " <")
				lines[i] = "author " + name + " <" + suffix
			}
		}
		cmd := exec.Command("git", "hash-object", "-t", "commit", "-w", "--stdin")
		cmd.Dir = repo.Dir
		cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
		oid, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		lastModifiedTestGit(t, repo.Dir, nil, "update-ref", "HEAD", strings.TrimSpace(string(oid)))
		c := NewFileInfoCacheWithLastModified()
		assertLastModifiedBaseline(t, c, repo.Path("snippets/shared.mdx"))
		if entry := batchForPath(t, c, repo.Path("snippets/shared.mdx")); entry == nil || len(entry.files) == 0 {
			t.Fatalf("author %q unexpectedly disabled batch", name)
		}
	}
}

func TestFileInfoCache_LastModifiedRunLifetimeAndMailmap(t *testing.T) {
	lastModifiedTestEnvironment(t)
	repo := newSnippetRepo(t)
	requireLastModified(t, repo.Dir)
	repo.Commit(commitTime.Add(time.Hour), "mailmap", map[string]string{
		".mailmap": "Mapped User <mapped@example.com> Test User <test@example.com>\n",
	})
	path := repo.Path("snippets/shared.mdx")
	c := NewFileInfoCacheWithLastModified()
	counts, _ := observeLastModified(c)
	assertLastModifiedBaseline(t, c, path)
	info, _ := c.FileLastModified(path)
	if info.LastAuthor != "Test User" || counts["last-modified"] != 1 {
		t.Fatalf("%%an was mapped or fast path disabled: %+v, %v", info, counts)
	}
	repo.Commit(commitTime.Add(2*time.Hour), "update", map[string]string{"snippets/shared.mdx": "updated\n"})
	// The checkout is stable within a run; a fresh cache sees the next run.
	assertLastModifiedBaseline(t, NewFileInfoCacheWithLastModified(), path)
}

func TestFileInfoCache_LastModifiedNoRepositoryOrHistory(t *testing.T) {
	lastModifiedTestEnvironment(t)
	repo := testutil.NewRepo(t) // unborn HEAD
	path := repo.Write("draft.md", "draft\n")
	c := NewFileInfoCacheWithLastModified()
	counts, _ := observeLastModified(c)
	assertLastModifiedBaseline(t, c, path, path)
	if counts["rev-list"] != 1 || counts["last-modified"] != 0 {
		t.Fatalf("unborn history repeatedly probed: %v", counts)
	}
	outside := filepath.Join(t.TempDir(), "orphan.md")
	if err := os.WriteFile(outside, []byte("orphan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertLastModifiedBaseline(t, c, outside, outside)
}

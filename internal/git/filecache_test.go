package git

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/testutil"
)

// commitTime is the fixed commit date every fixture repo in this file uses, so
// the memoized and unmemoized results can be compared exactly.
var commitTime = time.Date(2024, 3, 4, 10, 30, 45, 0, time.UTC)

func newSnippetRepo(t *testing.T) *testutil.Repo {
	t.Helper()
	repo := testutil.NewRepo(t)
	repo.Commit(commitTime, "add snippet", map[string]string{
		"snippets/shared.mdx": "shared body\n",
	})
	return repo
}

func TestFileInfoCache_MatchesUncachedResult(t *testing.T) {
	repo := newSnippetRepo(t)
	path := repo.Path("snippets/shared.mdx")

	want, err := GetFileLastModified(path)
	if err != nil {
		t.Fatalf("GetFileLastModified: %v", err)
	}
	if want == nil {
		t.Skip("no history for the fixture file")
	}

	c := NewFileInfoCache()
	got, err := c.FileLastModified(path)
	if err != nil {
		t.Fatalf("FileLastModified: %v", err)
	}
	if got == nil {
		t.Fatal("cached lookup returned nil for a committed file")
	}
	if *got != *want {
		t.Errorf("cached result %+v differs from uncached %+v", *got, *want)
	}
}

func TestFileInfoCache_SecondLookupIsAHit(t *testing.T) {
	repo := newSnippetRepo(t)
	path := repo.Path("snippets/shared.mdx")

	c := NewFileInfoCache()
	first, err := c.FileLastModified(path)
	if err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	second, err := c.FileLastModified(path)
	if err != nil {
		t.Fatalf("second lookup: %v", err)
	}
	if first == nil || second == nil {
		t.Fatal("expected history for a committed file")
	}
	if *first != *second {
		t.Errorf("repeat lookup differs: %+v vs %+v", *first, *second)
	}
	if first == second {
		t.Error("cache handed the same *FileInfo pointer to two callers")
	}

	hits, misses := c.Stats()
	if misses != 1 {
		t.Errorf("misses = %d, want 1 (one git invocation)", misses)
	}
	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
}

// countingLookup wraps GetFileLastModified in a counter, so a test can assert
// the invariant #65 actually promises — that the underlying git query runs
// once per distinct path — instead of trusting the cache's own hit/miss
// counters, which stay correct even if the lookup is moved outside once.Do.
func countingLookup(calls *int32) func(string) (*FileInfo, error) {
	return func(p string) (*FileInfo, error) {
		atomic.AddInt32(calls, 1)
		return GetFileLastModified(p)
	}
}

// TestFileInfoCache_RepeatLookupRunsGitOnce is the deduplication guarantee for
// the sequential case: the second and later asks for a path must not reach git
// at all.
func TestFileInfoCache_RepeatLookupRunsGitOnce(t *testing.T) {
	repo := newSnippetRepo(t)
	path := repo.Path("snippets/shared.mdx")

	var calls int32
	c := NewFileInfoCache()
	c.lookup = countingLookup(&calls)

	const lookups = 5
	for i := 0; i < lookups; i++ {
		info, err := c.FileLastModified(path)
		if err != nil {
			t.Fatalf("lookup %d: %v", i, err)
		}
		if info == nil {
			t.Fatalf("lookup %d returned no info for a committed file", i)
		}
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("underlying lookup ran %d times across %d asks, want 1", got, lookups)
	}
}

// TestFileInfoCache_KeyNormalisation checks that genuinely different spellings
// of the same file — relative vs absolute, an uncleaned ".." detour, and (on
// macOS) the /var vs /private/var symlink the temp dir sits behind — collapse
// to one entry rather than one per string. Every spelling here is built by
// concatenation, not filepath.Join, because Join cleans its result and would
// silently hand back the canonical string, testing nothing.
func TestFileInfoCache_KeyNormalisation(t *testing.T) {
	repo := newSnippetRepo(t)
	abs := repo.Path("snippets/shared.mdx")
	sep := string(filepath.Separator)
	viaDot := repo.Dir + sep + "snippets" + sep + ".." + sep + "snippets" + sep + "shared.mdx"

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo.Dir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	viaLink := filepath.Join(link, "snippets", "shared.mdx")

	// The relative leg needs the working directory to be the repo, since that
	// is what filepath.Abs resolves against. No test in this package runs in
	// parallel, and internal/config/profile_test.go already chdirs this way.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo.Dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	rel := "snippets" + sep + "shared.mdx"

	spellings := []string{abs, rel, viaDot, viaLink}
	for i, p := range spellings {
		for j := 0; j < i; j++ {
			if spellings[j] == p {
				t.Fatalf("spellings %d and %d are the same string %q; the case is vacuous", j, i, p)
			}
		}
	}

	c := NewFileInfoCache()
	var calls int32
	c.lookup = countingLookup(&calls)
	for _, p := range spellings {
		if _, err := c.FileLastModified(p); err != nil {
			t.Fatalf("lookup %q: %v", p, err)
		}
	}
	if _, misses := c.Stats(); misses != 1 {
		t.Errorf("misses = %d, want 1: %d spellings of one file should share an entry", misses, len(spellings))
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("underlying lookup ran %d times, want 1", got)
	}
}

// TestFileInfoCache_ReportsCallerSpelling documents that the copy handed back
// carries the path the caller asked for, exactly as the uncached call does,
// even when an earlier caller primed the entry with a different spelling.
func TestFileInfoCache_ReportsCallerSpelling(t *testing.T) {
	repo := newSnippetRepo(t)
	abs := repo.Path("snippets/shared.mdx")
	// Built by concatenation on purpose: filepath.Join cleans its result, so
	// Join(repo.Dir, "snippets", "..", "snippets", "shared.mdx") is abs itself
	// and the test would prime and read with one and the same string.
	sep := string(filepath.Separator)
	viaDot := repo.Dir + sep + "snippets" + sep + ".." + sep + "snippets" + sep + "shared.mdx"
	if viaDot == abs {
		t.Fatalf("the two spellings are identical (%q); the test proves nothing", abs)
	}

	c := NewFileInfoCache()
	if _, err := c.FileLastModified(abs); err != nil {
		t.Fatalf("priming lookup: %v", err)
	}
	got, err := c.FileLastModified(viaDot)
	if err != nil {
		t.Fatalf("second lookup: %v", err)
	}
	if got == nil {
		t.Fatal("second lookup returned no info")
	}
	if got.Path != viaDot {
		t.Errorf("Path = %q, want the caller's own spelling %q", got.Path, viaDot)
	}
}

// TestFileInfoCache_CachesNoHistory covers the (nil, nil) shape: a file that
// exists inside a repo but was never committed.
func TestFileInfoCache_CachesNoHistory(t *testing.T) {
	repo := newSnippetRepo(t)
	uncommitted := repo.Write("snippets/draft.mdx", "draft\n")

	want, wantErr := GetFileLastModified(uncommitted)
	if want != nil || wantErr != nil {
		t.Skipf("uncommitted file did not produce the (nil, nil) shape: %+v, %v", want, wantErr)
	}

	c := NewFileInfoCache()
	for i := 0; i < 3; i++ {
		info, err := c.FileLastModified(uncommitted)
		if info != nil || err != nil {
			t.Fatalf("lookup %d: got (%+v, %v), want (nil, nil)", i, info, err)
		}
	}
	if _, misses := c.Stats(); misses != 1 {
		t.Errorf("misses = %d, want 1: a no-history result must be cached too", misses)
	}
}

// TestFileInfoCache_CachesError covers the (nil, err) shape: a path with no
// git repository anywhere above it.
func TestFileInfoCache_CachesError(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "orphan.md")
	if err := os.WriteFile(outside, []byte("no repo here\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := GetFileLastModified(outside); err == nil {
		t.Skip("temp dir is inside a git repository; cannot produce the error shape")
	}

	c := NewFileInfoCache()
	var firstErr error
	for i := 0; i < 3; i++ {
		info, err := c.FileLastModified(outside)
		if info != nil {
			t.Fatalf("lookup %d returned info for a path with no repository", i)
		}
		if err == nil {
			t.Fatalf("lookup %d returned no error", i)
		}
		if i == 0 {
			firstErr = err
		} else if err != firstErr {
			t.Errorf("lookup %d returned a different error: %v vs %v", i, err, firstErr)
		}
	}
	if _, misses := c.Stats(); misses != 1 {
		t.Errorf("misses = %d, want 1: an error result must be cached too", misses)
	}
}

func TestFileInfoCache_NilReceiverBehavesLikeDirectCall(t *testing.T) {
	repo := newSnippetRepo(t)
	path := repo.Path("snippets/shared.mdx")

	want, wantErr := GetFileLastModified(path)
	var c *FileInfoCache
	got, gotErr := c.FileLastModified(path)

	if (wantErr == nil) != (gotErr == nil) {
		t.Fatalf("nil cache error mismatch: %v vs %v", gotErr, wantErr)
	}
	if (want == nil) != (got == nil) {
		t.Fatalf("nil cache info mismatch: %+v vs %+v", got, want)
	}
	if want != nil && *got != *want {
		t.Errorf("nil cache result %+v differs from direct call %+v", *got, *want)
	}
	if hits, misses := c.Stats(); hits != 0 || misses != 0 {
		t.Errorf("nil cache Stats() = (%d, %d), want (0, 0)", hits, misses)
	}
}

// TestFileInfoCache_ConcurrentSamePath is the deduplication guarantee: many
// goroutines asking for one path at once must produce exactly one git
// invocation and identical results. Run under -race.
func TestFileInfoCache_ConcurrentSamePath(t *testing.T) {
	repo := newSnippetRepo(t)
	path := repo.Path("snippets/shared.mdx")

	const goroutines = 32
	var calls int32
	c := NewFileInfoCache()
	c.lookup = countingLookup(&calls)
	results := make([]*FileInfo, goroutines)
	errs := make([]error, goroutines)

	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.Wait()
			results[i], errs[i] = c.FileLastModified(path)
		}(i)
	}
	start.Done()
	wg.Wait()

	hits, misses := c.Stats()
	if misses != 1 {
		t.Errorf("misses = %d, want 1: concurrent lookups must share one git invocation", misses)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("underlying lookup ran %d times, want 1: concurrent lookups must share one git invocation", got)
	}
	if hits != goroutines-1 {
		t.Errorf("hits = %d, want %d", hits, goroutines-1)
	}
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if results[i] == nil {
			t.Fatalf("goroutine %d got no info", i)
		}
		if *results[i] != *results[0] {
			t.Errorf("goroutine %d result %+v differs from %+v", i, *results[i], *results[0])
		}
	}
}

// TestFileInfoCache_ConcurrentDistinctPaths exercises the map under contention
// from several keys at once (race detector coverage for entryFor).
func TestFileInfoCache_ConcurrentDistinctPaths(t *testing.T) {
	repo := testutil.NewRepo(t)
	files := map[string]string{}
	for i := 0; i < 8; i++ {
		files[filepath.ToSlash(filepath.Join("snippets", string(rune('a'+i))+".mdx"))] = "body\n"
	}
	repo.Commit(commitTime, "add snippets", files)

	c := NewFileInfoCache()
	var wg sync.WaitGroup
	for rel := range files {
		for r := 0; r < 4; r++ {
			wg.Add(1)
			go func(rel string) {
				defer wg.Done()
				if _, err := c.FileLastModified(repo.Path(rel)); err != nil {
					t.Errorf("lookup %s: %v", rel, err)
				}
			}(rel)
		}
	}
	wg.Wait()

	hits, misses := c.Stats()
	if misses != len(files) {
		t.Errorf("misses = %d, want %d", misses, len(files))
	}
	if hits != len(files)*3 {
		t.Errorf("hits = %d, want %d", hits, len(files)*3)
	}
}

// TestFileInfoCache_ZeroValueUsable checks that a FileInfoCache{} (no
// constructor) still works: entryFor lazily creates the map.
func TestFileInfoCache_ZeroValueUsable(t *testing.T) {
	repo := newSnippetRepo(t)
	var c FileInfoCache
	if _, err := c.FileLastModified(repo.Path("snippets/shared.mdx")); err != nil {
		t.Fatalf("zero-value cache lookup: %v", err)
	}
	if _, misses := c.Stats(); misses != 1 {
		t.Errorf("misses = %d, want 1", misses)
	}
}

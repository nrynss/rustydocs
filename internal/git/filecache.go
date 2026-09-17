package git

import (
	"path/filepath"
	"sync"
)

// FileInfoCache memoizes GetFileLastModified for the duration of one analysis
// run. Reusable resolution asks for the same include over and over — a snippet
// shared by 300 pages costs 300 `git log` subprocesses without it, and process
// spawn dominates the wall clock of a Mintlify run (#65).
//
// The cache is deliberately an explicit value rather than package state: it is
// created per run (analyzer.AnalyzeWithProgress) and handed to the workers, so
// nothing leaks between runs or between tests.
//
// Correctness rests on a single fact: one run analyses one commit state. Every
// lookup in a run therefore has one right answer, and memoizing it cannot
// change a report. Nothing here is written to disk and nothing survives the
// run; blame output is not cached, only the per-file `git log` lookup.
//
// A nil *FileInfoCache is valid and means "no caching": every method falls
// through to the plain function, so callers that have no cache work unchanged.
// All methods are safe for concurrent use.
type FileInfoCache struct {
	mu      sync.Mutex
	entries map[string]*fileInfoEntry
	hits    int
	misses  int

	// lookup is the underlying, uncached query. It is nil everywhere in the
	// CLI, which means GetFileLastModified; only tests set it, so they can
	// count real invocations and assert the deduplication guarantee directly
	// rather than through the hit/miss counters the cache keeps about itself.
	// It is written once, before the cache is shared, and only read after.
	lookup func(string) (*FileInfo, error)
}

// fileInfoEntry is one memoized lookup. once serialises the underlying git call
// so concurrent askers for the same path produce exactly one subprocess; info
// and err are written inside once.Do and only read after it returns.
type fileInfoEntry struct {
	once sync.Once
	info *FileInfo
	err  error
}

// NewFileInfoCache returns an empty cache ready for concurrent use.
func NewFileInfoCache() *FileInfoCache {
	return &FileInfoCache{entries: make(map[string]*fileInfoEntry)}
}

// FileLastModified returns GetFileLastModified(filePath), memoized per
// normalised path. Negative results are cached too — both shapes the function
// can return, the (nil, err) of a path git cannot answer for and the
// (nil, nil) of a path with no commits. A broken snippet referenced from every
// page is precisely the case that hurts most, so it must not stay slow.
//
// A nil receiver performs the uncached call.
func (c *FileInfoCache) FileLastModified(filePath string) (*FileInfo, error) {
	if c == nil {
		return GetFileLastModified(filePath)
	}

	lookup := c.lookup
	if lookup == nil {
		lookup = GetFileLastModified
	}

	entry := c.entryFor(fileInfoCacheKey(filePath))
	entry.once.Do(func() {
		entry.info, entry.err = lookup(filePath)
	})

	if entry.info == nil {
		return nil, entry.err
	}
	// Hand back a copy carrying the caller's own spelling of the path, so the
	// result is indistinguishable from an uncached call (GetFileLastModified
	// sets FileInfo.Path to the path it was given) and callers never share a
	// pointer with one another.
	info := *entry.info
	info.Path = filePath
	return &info, entry.err
}

// entryFor returns the entry for key, creating it if absent. The map lock is
// held only for the lookup: the git subprocess runs under the entry's own
// sync.Once, outside this mutex, so one slow lookup never serialises the pool.
func (c *FileInfoCache) entryFor(key string) *fileInfoEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]*fileInfoEntry)
	}
	if entry, ok := c.entries[key]; ok {
		c.hits++
		return entry
	}
	entry := &fileInfoEntry{}
	c.entries[key] = entry
	c.misses++
	return entry
}

// Stats returns the number of lookups served from an existing entry (hits) and
// the number that created one (misses). Misses equal the number of underlying
// `git log` invocations, since each entry runs its lookup exactly once. It
// exists for tests and ad-hoc profiling; nothing in the CLI reports it.
//
// A nil cache reports zeroes.
func (c *FileInfoCache) Stats() (hits, misses int) {
	if c == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses
}

// fileInfoCacheKey normalises a path to the identity the cache keys on.
//
// The key choice matters: GetFileLastModified itself does filepath.Abs plus
// EvalSymlinks internally, so the very same file reaches it as several
// different strings — relative and absolute spellings, and on macOS the
// /var -> /private/var symlink alone gives every temp-dir path two forms.
// Keying on the raw argument would leave those as separate entries and lose
// most of the sharing the cache exists for, so the key is the absolute,
// symlink-resolved path.
//
// Keying on the resolved path assumes both spellings of a file resolve to the
// same git repository: GetFileLastModified derives its git root from the
// *unresolved* filepath.Dir(filePath), so two spellings whose directories live
// in different repositories (a snippet symlinked across a repo boundary, say)
// would share one key while having different uncached answers — no resolver
// reaches that today, but one that could would need the git root in the key.
//
// Normalisation is best effort: a path that cannot be made absolute or cannot
// be resolved (it does not exist, which is a legitimate lookup that will be
// cached as a negative result) falls back to the furthest form reached, and
// ultimately to the raw string. Two spellings that fail to collapse cost an
// extra entry, never a wrong answer.
func fileInfoCacheKey(filePath string) string {
	key := filePath
	if abs, err := filepath.Abs(key); err == nil {
		key = abs
	}
	if resolved, err := filepath.EvalSymlinks(key); err == nil {
		key = resolved
	}
	return key
}

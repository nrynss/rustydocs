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
// Callers must keep the checkout (history, working tree, and symlinks) stable
// during analysis. The analyzer does not pin HEAD or provide a snapshot. Only
// per-file git log results are cached, and nothing survives the run.
//
// A nil *FileInfoCache is valid and means "no caching": every method falls
// through to the plain function, so callers that have no cache work unchanged.
// All methods are safe for concurrent use.
type FileInfoCache struct {
	mu      sync.Mutex
	entries map[fileInfoKey]*fileInfoEntry
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
	return &FileInfoCache{entries: make(map[fileInfoKey]*fileInfoEntry)}
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
func (c *FileInfoCache) entryFor(key fileInfoKey) *fileInfoEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[fileInfoKey]*fileInfoEntry)
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

// Include the directory from which git selects its repository as well as the
// resolved file. A file symlink in repository A pointing into repository B
// does not have the same git-log answer as the target in B. Resolving directory
// aliases still deduplicates whole-checkout symlinks (including /var on macOS).
// Using the directory rather than probing git roots avoids adding subprocesses
// for missing files and non-repositories. Different directories in the same
// repository may conservatively retain separate entries.
type fileInfoKey struct {
	directory string
	file      string
}

// fileInfoCacheKey combines the lookup directory context with the best-effort
// normalized file path.
func fileInfoCacheKey(filePath string) fileInfoKey {
	return fileInfoKey{
		directory: normaliseCachePath(filepath.Dir(filePath)),
		file:      normaliseCachePath(filePath),
	}
}

// Failed normalization retains the caller's spelling so cached filesystem
// errors do not accidentally borrow another caller's path context.
func normaliseCachePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return path
	}
	return resolved
}

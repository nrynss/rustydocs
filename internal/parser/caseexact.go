package parser

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// caseExactUnder reports whether candidate is spelled the way the filesystem
// spells it, component by component, below the trusted base directory.
//
// Why this exists. Resolution builds a candidate path out of a capture written
// in a documentation file — <Snippet file="Note.mdx" />, a bare <Note />, an
// import specifier — and then asks the operating system whether that file is
// there. On a case-insensitive filesystem the answer is yes for a file whose
// real name is note.mdx, and what happens next is *platform-dependent*:
//
//   - Linux (case-sensitive): os.Stat fails, the candidate is rejected, and the
//     reference is reported unresolved.
//   - macOS (case-insensitive): os.Stat succeeds, so resolveDirectPath calls
//     the reference resolved and DisplayName labels it "snippets/Note.mdx" — a
//     file that does not exist under that name — but `git log -- Note.mdx`
//     matches nothing (git is case-exact even with core.ignorecase=true), so
//     the reference ends up with no history and is reported unknown.
//   - Windows (case-insensitive *and* case-normalising): filepath.EvalSymlinks
//     is implemented with toNorm/normBase, which replaces every component with
//     the real on-disk name from FindFirstFile. internal/git makes the pathspec
//     relative to the repo root from that resolved path, so git is asked about
//     snippets\note.mdx and answers — the reference fully resolves and the
//     snippet's commit date is folded into the section's freshness.
//
// Three platforms, three different reports for the same repository, and the
// Windows one is the dangerous direction: a page that merely renders <Note />
// is marked fresh by whatever last touched an unrelated snippets/note.mdx.
// rustydocs exists to give a docs team and its CI the same answer, so candidate
// matching is made case-exact everywhere rather than left to the filesystem.
// The behaviour it converges on is the case-sensitive one.
//
// Scope of the check: only the components below base, because base is built
// from configuration and from directory listings rather than from a capture. A
// candidate that is not under base at all — the "../x.mdx" form, whose cleaned
// path climbs out of the referencing page's directory — has only its own file
// name verified, which is where every realistic collision lives.
func (rp *ReusablePatterns) caseExactUnder(base, candidate string) bool {
	base, candidate = absClean(base), absClean(candidate)

	rel, err := filepath.Rel(base, candidate)
	if err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		base, rel = filepath.Dir(candidate), filepath.Base(candidate)
	}

	dir := base
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		if !rp.dirHasEntry(dir, part) {
			return false
		}
		dir = filepath.Join(dir, part)
	}
	return true
}

// absClean returns path in absolute, lexically cleaned form, falling back to
// the cleaned relative form when the working directory cannot be read.
func absClean(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// dirHasEntry reports whether dir contains an entry named exactly name,
// memoizing each directory listing for the life of this ReusablePatterns (one
// analyzed file, one worker — the same ownership filePaths, shortcodeCache and
// importCache already assume, so no locking).
//
// A directory that cannot be *read* is not evidence either way: a
// traverse-only directory (mode 0111) lets os.Stat see its children while
// os.ReadDir fails, and rejecting those would lose resolutions that work today
// on every platform. Such a directory is therefore trusted, and only a
// directory that genuinely does not exist rejects.
func (rp *ReusablePatterns) dirHasEntry(dir, name string) bool {
	names, cached := rp.dirEntries[dir]
	if !cached {
		names = readDirNames(dir)
		if rp.dirEntries == nil {
			rp.dirEntries = make(map[string]map[string]struct{})
		}
		rp.dirEntries[dir] = names
	}
	if names == nil {
		return true // unreadable: cannot verify, so do not pretend to.
	}
	_, ok := names[name]
	return ok
}

// readDirNames returns the set of entry names in dir, an empty set when dir
// does not exist, and nil when it exists but could not be listed.
func readDirNames(dir string) map[string]struct{} {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]struct{}{}
		}
		return nil
	}
	names := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		names[e.Name()] = struct{}{}
	}
	return names
}

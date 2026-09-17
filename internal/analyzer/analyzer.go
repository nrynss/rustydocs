// Package analyzer provides the main analysis orchestrator for rustydocs.
package analyzer

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/git"
	"github.com/nrynss/rustydocs/internal/parser"
)

// nowFunc returns the current time. It is a package variable so tests can pin
// "now" and assert deterministic staleness math.
var nowFunc = time.Now

// contentExtensionSet builds a set of allowed documentation extensions in the
// canonical (lowercase, dot-prefixed) form isContentFile matches on. Config
// lists arrive already canonicalised by ApplyProfile; normalising again here
// keeps the set correct for a Config the caller built by hand, and the fall
// back to the markdown profile's extensions covers one on which ApplyProfile
// was never called.
func contentExtensionSet(exts []string) map[string]struct{} {
	canonical := config.NormalizeExtensions(exts)
	if len(canonical) == 0 {
		canonical = config.DefaultProfile().ContentExtensions
	}
	set := make(map[string]struct{}, len(canonical))
	for _, e := range canonical {
		set[e] = struct{}{}
	}
	return set
}

// isContentFile reports whether path has one of the allowed documentation
// extensions (case-insensitive).
func isContentFile(filePath string, exts map[string]struct{}) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	_, ok := exts[ext]
	return ok
}

// ReusableInfo contains information about a reusable component.
type ReusableInfo struct {
	Name        string
	LastUpdated *time.Time
	IsFresh     bool
	LastAuthor  string
}

// FileAnalysis contains analysis results for a single file.
type FileAnalysis struct {
	Path                 string
	RelativePath         string
	FileInfo             *git.FileInfo
	Sections             []parser.Section
	StaleSections        []parser.Section
	Reusables            []ReusableInfo
	EffectiveLastUpdated *time.Time
	OldestSectionDate    *time.Time
	DaysStale            int
	OldestSectionDays    int
	// unresolvedReusableRefs holds the distinct reusable references on this
	// file that produced no resolved history — the ones reported "unknown" —
	// as the raw captures the page actually wrote, in the order they were
	// first seen. It is diagnostic only (aggregated into
	// Results.UnresolvedReusables / UnresolvedReusableRefs) and is not part of
	// any report. The count is its length: the loop that fills it already
	// visits each distinct capture on the file exactly once.
	unresolvedReusableRefs []string
	// HistoryMissing is true when git produced no timestamps for the file
	// (neither file-level nor line-level), e.g. an uncommitted file, a shallow
	// clone, or a content tree that is not a git repository. Such a file cannot
	// be assessed for staleness and must NOT be treated as fresh. See #55.
	HistoryMissing bool
}

// IsStale returns true if the file has any stale content.
func (f *FileAnalysis) IsStale() bool {
	return len(f.StaleSections) > 0
}

// Results contains complete analysis results.
type Results struct {
	Files        []FileAnalysis
	AllReusables []ReusableInfo
	Config       *config.Config
	GeneratedAt  time.Time

	// filesExcluded counts files whose extension matched the content allowlist
	// but which exclude_dirs / exclude_patterns dropped from the walk. It is
	// diagnostic only (see FilesExcluded) and is not part of any report.
	filesExcluded int

	// filesSkippedExt counts files that are documentation under some built-in
	// profile (config.KnownContentExtensions) but whose extension is not in
	// the active allowlist, and which the exclusions did not drop anyway;
	// skippedExts holds their distinct extensions. Diagnostic only (see
	// FilesSkippedByExtension / SkippedExtensions), not part of any report.
	filesSkippedExt int
	skippedExts     map[string]struct{}

	// dirsSkipped counts directories the *default* exclusions pruned from the
	// walk (dot-directories, vendored/build trees, and directories holding
	// their own .git); skippedDirs holds their distinct base names. Files under
	// them are never visited, so they contribute to no other count — which is
	// the point: this is the only number that explains where they went.
	// Diagnostic only (see DirsSkipped / SkippedDirNames), not part of any
	// report.
	dirsSkipped int
	skippedDirs map[string]struct{}

	// dirsExcluded counts directories the *user's* exclude_dirs pruned from the
	// walk. Like dirsSkipped it is the only trace they leave — their files are
	// never visited, so they are not in filesExcluded either. Diagnostic only
	// (see DirsExcluded), not part of any report.
	dirsExcluded int

	// filesGitIgnored counts files that matched the content allowlist, survived
	// every directory exclusion, and were then dropped because git itself
	// ignores them. Diagnostic only (see FilesGitIgnored), not part of any
	// report.
	filesGitIgnored int

	// unresolvedReusables counts reusable references (once per file per
	// distinct capture) that resolved to nothing with git history and were
	// therefore reported "unknown"; unresolvedRefs holds the distinct raw
	// captures behind that count, de-duplicated across files and ordered by
	// the (sorted) file they were first seen in. Diagnostic only (see
	// UnresolvedReusables / UnresolvedReusableRefs), not part of any report.
	unresolvedReusables int
	unresolvedRefs      []string
}

// TotalFiles returns the total number of files analyzed.
func (r *Results) TotalFiles() int {
	return len(r.Files)
}

// StaleFiles returns the number of files with stale content.
func (r *Results) StaleFiles() int {
	count := 0
	for _, f := range r.Files {
		if f.IsStale() {
			count++
		}
	}
	return count
}

// FilesMissingHistory returns the number of files for which git produced no
// history, so their staleness is unknown rather than fresh. See #55.
func (r *Results) FilesMissingHistory() int {
	count := 0
	for _, f := range r.Files {
		if f.HistoryMissing {
			count++
		}
	}
	return count
}

// FilesExcluded returns the number of files that matched the content
// extensions but were skipped by exclude_dirs / exclude_patterns. It lets the
// zero-files warning distinguish "nothing matched the extensions" from
// "everything that matched was excluded" (#11).
func (r *Results) FilesExcluded() int {
	return r.filesExcluded
}

// FilesSkippedByExtension returns the number of files that rustydocs would
// analyze under some other built-in profile but that the active extension
// allowlist excluded — e.g. the .mdx files of a Docusaurus/Mintlify tree run
// under the markdown profile. Files that are not documentation under any
// profile (images, .txt, .json, .yaml) are deliberately not counted, and
// neither are files the exclusions would have dropped regardless. It lets the
// CLI warn about a partially-scanned tree, not only a wholly unmatched one
// (#11).
func (r *Results) FilesSkippedByExtension() int {
	return r.filesSkippedExt
}

// SkippedExtensions returns the distinct extensions counted by
// FilesSkippedByExtension, lowercase, dot-prefixed and sorted.
func (r *Results) SkippedExtensions() []string {
	out := make([]string, 0, len(r.skippedExts))
	for ext := range r.skippedExts {
		out = append(out, ext)
	}
	sort.Strings(out)
	return out
}

// DirsSkipped returns the number of directories the default exclusions pruned
// from the content walk: dot-directories (.git, .claude, .cursor), the
// vendored/build trees of config.DefaultExcludeDirNames, and nested standalone
// repositories — a clone or a linked worktree, whose files
// git.GetGitRootForPath would resolve against a different project. A submodule
// is not counted here: it belongs to the repository under analysis and is
// scanned (see isDefaultExcludedDir).
//
// It exists so a user can see why the file count dropped. Files under a pruned
// directory are never visited, so they appear in no other counter (#69).
func (r *Results) DirsSkipped() int {
	return r.dirsSkipped
}

// SkippedDirNames returns the distinct base names of the directories counted by
// DirsSkipped, sorted.
func (r *Results) SkippedDirNames() []string {
	out := make([]string, 0, len(r.skippedDirs))
	for name := range r.skippedDirs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// DirsExcluded returns the number of directories the user's exclude_dirs
// pruned from the content walk. Their files are never visited, so — exactly as
// with DirsSkipped — they contribute to no other counter, and this is the only
// number that explains where they went (#69 review).
func (r *Results) DirsExcluded() int {
	return r.dirsExcluded
}

// FilesGitIgnored returns the number of files that matched the content
// allowlist and survived every directory exclusion, but that git ignores. An
// ignored file is not tracked documentation and blame can say nothing useful
// about it, so analyzing it only manufactures "unknown" rows (#69).
func (r *Results) FilesGitIgnored() int {
	return r.filesGitIgnored
}

// UnresolvedReusables returns the number of reusable references that resolved
// to no file with git history and were therefore reported "unknown" (counted
// once per file per distinct capture). It exists so the CLI can gate its
// missing-project-root note on something actually having failed, rather than
// on the resolver alone: a run whose includes all resolve through a legacy
// reusables directory, and a run with no reusable references at all, both
// report zero and get no note (#7).
func (r *Results) UnresolvedReusables() int {
	return r.unresolvedReusables
}

// UnresolvedReusableRefs returns the distinct raw captures counted by
// UnresolvedReusables — "typo.mdx", "/snippets/gone.mdx" — de-duplicated
// across files and in a deterministic order (files are sorted before they are
// merged, and each file keeps first-seen order within itself). It exists so
// the CLI note can name what failed instead of only counting it: a bare count
// tells a reader that something is wrong but not which reference to go and
// look at (#7).
func (r *Results) UnresolvedReusableRefs() []string {
	return slices.Clone(r.unresolvedRefs)
}

// StaleFilesPct returns the percentage of files with stale content.
func (r *Results) StaleFilesPct() float64 {
	if r.TotalFiles() == 0 {
		return 0
	}
	return float64(r.StaleFiles()) / float64(r.TotalFiles()) * 100
}

// TotalSections returns the total number of sections analyzed.
func (r *Results) TotalSections() int {
	count := 0
	for _, f := range r.Files {
		count += len(f.Sections)
	}
	return count
}

// StaleSections returns the number of stale sections.
func (r *Results) StaleSections() int {
	count := 0
	for _, f := range r.Files {
		count += len(f.StaleSections)
	}
	return count
}

// StaleSectionsPct returns the percentage of stale sections.
func (r *Results) StaleSectionsPct() float64 {
	if r.TotalSections() == 0 {
		return 0
	}
	return float64(r.StaleSections()) / float64(r.TotalSections()) * 100
}

// OldestFile returns the file with the oldest content (based on oldest section).
func (r *Results) OldestFile() *FileAnalysis {
	var oldest *FileAnalysis
	for i := range r.Files {
		if r.Files[i].IsStale() {
			if oldest == nil || r.Files[i].OldestSectionDays > oldest.OldestSectionDays {
				oldest = &r.Files[i]
			}
		}
	}
	return oldest
}

// isDefaultExcludedDir reports whether the default exclusions prune this
// directory from the walk. Two rules, in the order they are cheap:
//
//   - its base name is a dot-directory or one of config.DefaultExcludeDirNames;
//   - it is the root of a standalone repository of its own (config.IsRepoRoot).
//
// The nested-repository rule is the one that cannot be expressed as a name. A
// Claude Code worktree or a vendored checkout is a *different* project:
// git.GetGitRootForPath resolves its files against that repository, so their
// dates describe someone else's history, and on the measured corpus they were
// the bulk of the 1,078 files reported with no history at all.
//
// A submodule is not such a project. Its checkout is a subdirectory of the
// repository under analysis, listed in its .gitmodules and pinned by its
// commits, and a docs site that keeps a shared content tree there means every
// word of it to be part of the documentation. Pruning it dropped that content
// silently, with --no-default-excludes (which also switches off the dot-dir,
// vendored and gitignore rules) as the only way back. It is walked *through*
// instead, and the per-directory git-root cache resolves its files against its
// own repository, which is where their history genuinely lives — the same
// answer config.walkUp gives, so the two walks no longer disagree (#69).
func isDefaultExcludedDir(dirPath, name string) bool {
	if config.IsDefaultExcludedDir(name) {
		return true
	}
	return config.IsRepoRoot(dirPath)
}

// filterGitIgnored returns the files git does not ignore, and how many it
// dropped. A tree git cannot answer for — not a repository, no git on PATH —
// is returned unfiltered: rustydocs supports that case and reports its files
// as unknown.
//
// The files are grouped by their owning repository first, and each group gets
// its own `git check-ignore`. That is not an optimisation, it is the only
// correct shape: check-ignore refuses outright ("fatal: Pathspec 'x' is in
// submodule 'y'") when a pathspec it is handed lies inside a submodule of the
// repository it is asked from. One invocation rooted at the content directory
// therefore *errored* on any tree containing a submodule — and since an error
// means "skip filtering", a single submodule silently disabled the ignore
// filter for the whole repository, parent included. The walk descends into
// submodules by design (#69), so the two features met head-on (PR #71 review).
//
// Grouping also confines the error rule: a repository git cannot answer for
// loses its own filtering and nothing else.
//
// Paths are handed to git relative to the group's root and matched back by
// that exact spelling (see git.CheckIgnore). The root git reports is the
// *physical* path (`git rev-parse --show-toplevel` resolves symlinks), while
// the walk carries logical paths, so each directory's own physical form is
// resolved once and the pathspec built from that; a file whose physical path
// does not sit under the root it was attributed to (a symlink out of the tree)
// falls back to being asked about from the content directory, exactly as
// before.
func filterGitIgnored(baseDir string, files []string) (kept []string, dropped int) {
	if len(files) == 0 {
		return files, 0
	}

	// One group per owning repository, in first-seen order so the subprocesses
	// run deterministically. The zero key is the fallback group, asked from the
	// content directory for the files no root could be derived for.
	type group struct {
		dir  string // directory git is asked from
		idx  []int  // indices into files
		rels []string
	}
	groups := make(map[string]*group)
	var order []string
	add := func(key, dir, rel string, i int) {
		g := groups[key]
		if g == nil {
			g = &group{dir: dir}
			groups[key] = g
			order = append(order, key)
		}
		g.idx = append(g.idx, i)
		g.rels = append(g.rels, rel)
	}

	dirs := resolveDirRoots(files)

	for i, f := range files {
		info := dirs[filepath.Dir(f)]
		if rel, ok := relUnderRoot(info.root, info.phys, filepath.Base(f)); ok {
			add(info.root, info.root, rel, i)
			continue
		}
		rel, err := filepath.Rel(baseDir, f)
		if err != nil {
			// Nothing sensible to ask git about; keep the file.
			rel = f
		}
		add("", baseDir, filepath.ToSlash(rel), i)
	}

	isIgnored := make([]bool, len(files))
	for _, key := range order {
		g := groups[key]
		ignored, err := git.CheckIgnore(g.dir, g.rels)
		if err != nil || len(ignored) == 0 {
			// This repository keeps all its files; the others are unaffected.
			continue
		}
		for j, idx := range g.idx {
			if ignored[g.rels[j]] {
				isIgnored[idx] = true
				dropped++
			}
		}
	}
	if dropped == 0 {
		return files, 0
	}

	kept = make([]string, 0, len(files)-dropped)
	for i, f := range files {
		if !isIgnored[i] {
			kept = append(kept, f)
		}
	}
	return kept, dropped
}

// dirRoot is one directory's owning repository root and its own physical
// (symlink-resolved) path, the two things filterGitIgnored needs to build a
// pathspec. Either may be empty when it could not be determined.
type dirRoot struct{ root, phys string }

// resolveDirRoots resolves those two facts for every distinct directory the
// files sit in, concurrently.
//
// Concurrently because each root costs a `git rev-parse` subprocess and a docs
// corpus is wide: 552 files spread over 466 directories turned a 6-second run
// into a 14-second one when they were spawned back to back. Nothing extra is
// spawned overall — git.GetGitRootForPath's cache is per directory and
// process-wide, so these are exactly the lookups the blame stage would make
// later, only made earlier and in parallel.
//
// Each goroutine writes to its own pre-created entry, so the map itself is
// never written concurrently.
func resolveDirRoots(files []string) map[string]*dirRoot {
	dirs := make(map[string]*dirRoot)
	// A representative file per directory: GetGitRootForPath takes a file.
	samples := make(map[string]string)
	var order []string
	for _, f := range files {
		d := filepath.Dir(f)
		if _, seen := dirs[d]; !seen {
			dirs[d] = &dirRoot{}
			samples[d] = f
			order = append(order, d)
		}
	}

	workers := min(runtime.NumCPU(), len(order))
	ch := make(chan string, len(order))
	for _, d := range order {
		ch <- d
	}
	close(ch)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range ch {
				info := dirs[d]
				if root, err := git.GetGitRootForPath(samples[d]); err == nil {
					info.root = root
				}
				if phys, err := filepath.EvalSymlinks(d); err == nil {
					info.phys = phys
				}
			}
		}()
	}
	wg.Wait()
	return dirs
}

// relUnderRoot builds the slash-separated pathspec for a file whose directory
// resolves to physDir, relative to the repository root. It reports false when
// either path is unknown or when the result escapes the root, which is the
// caller's cue to fall back to asking from the content directory.
func relUnderRoot(root, physDir, base string) (string, bool) {
	if root == "" || physDir == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, physDir)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	if rel == "." {
		return base, true
	}
	return rel + "/" + base, true
}

func shouldExclude(filePath string, cfg *config.Config, baseDir string) bool {
	relative, err := filepath.Rel(baseDir, filePath)
	if err != nil {
		relative = filePath
	}
	// Match on forward-slash paths so behavior is identical across platforms.
	relative = filepath.ToSlash(relative)
	base := path.Base(relative)

	// Glob patterns (path.Match uses "/" and does not let "*" cross separators).
	for _, pattern := range cfg.ExcludePatterns {
		pattern = filepath.ToSlash(pattern)
		if ok, _ := path.Match(pattern, relative); ok {
			return true
		}
		if ok, _ := path.Match(pattern, base); ok {
			return true
		}
		// Treat "dir", "dir/", "dir/*" and "dir/**" as "everything under dir",
		// matched on a segment boundary so "docs" does not match "mydocs/...".
		prefix := strings.TrimSuffix(pattern, "/**")
		prefix = strings.TrimSuffix(prefix, "/*")
		prefix = strings.TrimSuffix(prefix, "/")
		if prefix != "" {
			if relative == prefix || strings.HasPrefix(relative, prefix+"/") {
				return true
			}
		}
	}

	// Excluded directory names, matched on segment boundaries.
	return matchesExcludeDirs(relative, cfg.ExcludeDirs)
}

// matchesExcludeDirs reports whether a slash-separated path, relative to the
// content root, lies at or under one of the configured exclude_dirs. Matching
// is on segment boundaries, so "docs" never matches "mydocs/...".
//
// The HasSuffix arm is what makes a *nested* match prunable. Without it the
// path "guides/drafts" — the directory itself — matched nothing, because it
// neither equals "drafts" nor contains "/drafts/", so isUserExcludedDir let
// the walk descend and every file under it was filtered one at a time by
// shouldExclude instead. The files were excluded either way (the Contains arm
// catches "guides/drafts/x.md"), so this was never a correctness bug; what it
// cost was the subtree prune and the DirsExcluded count that explains where
// those files went (PR #71 review).
func matchesExcludeDirs(relative string, excludeDirs []string) bool {
	for _, dir := range excludeDirs {
		dir = strings.Trim(filepath.ToSlash(dir), "/")
		if dir == "" {
			continue
		}
		if relative == dir ||
			strings.HasPrefix(relative, dir+"/") ||
			strings.Contains(relative, "/"+dir+"/") ||
			strings.HasSuffix(relative, "/"+dir) {
			return true
		}
	}
	return false
}

// isUserExcludedDir reports whether exclude_dirs prunes this directory from the
// walk. It is deliberately exclude_dirs only: that list names directories, so
// every file beneath a match is excluded too and the subtree can be skipped
// whole. exclude_patterns cannot be used this way — "a*" matches the directory
// "api" but not the file "api/x.md" underneath it — so patterns stay a per-file
// filter in shouldExclude.
//
// The user's list gets the same cheap SkipDir prune the default exclusions
// already had. It is the list they went to the trouble of typing, which usually
// means the subtree behind it is one they know is big (#69 review).
func isUserExcludedDir(dirPath string, cfg *config.Config, baseDir string) bool {
	if len(cfg.ExcludeDirs) == 0 {
		return false
	}
	relative, err := filepath.Rel(baseDir, dirPath)
	if err != nil {
		relative = dirPath
	}
	return matchesExcludeDirs(filepath.ToSlash(relative), cfg.ExcludeDirs)
}

// analyzeFile analyzes one documentation file. It returns an error only when
// the configured reusable patterns cannot be compiled; git failures and
// unreadable files degrade to an analysis with no history, never to an error.
//
// cache memoizes the per-file `git log` lookups this file's own header and its
// reusable resolution perform. It is created once per run by
// AnalyzeWithProgress and shared by every worker — a cache built here, or on
// the per-file ReusablePatterns below, could only dedupe within one page. A
// nil cache disables memoization (#65).
func analyzeFile(filePath string, cfg *config.Config, baseDir string, cache *git.FileInfoCache) (FileAnalysis, error) {
	now := nowFunc()
	thresholdDate := now.Add(-time.Duration(cfg.ThresholdDays) * 24 * time.Hour)

	// Get file-level info
	fileInfo, _ := cache.FileLastModified(filePath)

	// Read file content
	content, err := os.ReadFile(filepath.Clean(filePath))
	if err != nil {
		return FileAnalysis{Path: filePath}, nil
	}

	// Get relative path for display. Normalize to forward slashes so reports
	// (HTML anchors, JSON/Markdown paths) are consistent and portable across
	// platforms — filepath.Rel returns OS separators (backslashes on Windows).
	relativePath, err := filepath.Rel(baseDir, filePath)
	if err != nil {
		relativePath = filePath
	}
	relativePath = filepath.ToSlash(relativePath)

	// Create reusable patterns from config
	reusablesDir := cfg.Reusables.Dir
	if reusablesDir == "" {
		reusablesDir = cfg.ReusablesDir // backward compatibility
	}
	if reusablesDir != "" && !filepath.IsAbs(reusablesDir) {
		if cwd, err := os.Getwd(); err == nil {
			reusablesDir = filepath.Join(cwd, reusablesDir)
		}
		// If Getwd fails, leave reusablesDir as relative (will likely fail later but won't crash)
	}

	// The project root (Hugo's site root for shortcode tracing, the docs
	// project root Mintlify snippet paths resolve against) is filled by
	// cfg.ApplyProfile: the root the user supplied, else the root detected for
	// the resolved profile, else "".
	root := cfg.ProjectRoot
	if root != "" && !filepath.IsAbs(root) {
		if cwd, err := os.Getwd(); err == nil {
			root = filepath.Join(cwd, root)
		}
		// If Getwd fails, leave root as relative
	}

	// Patterns are validated once in AnalyzeWithProgress before any worker
	// starts, so this only fails for a Config handed to analyzeFile directly.
	// Never fall back to another profile's patterns: that would silently turn
	// Hugo detection on for a markdown/Mintlify run. See #11. The resolver
	// comes from the resolved profile rather than being inferred from which
	// roots happen to be set, so a Mintlify root resolves snippet paths and a
	// Hugo root traces shortcodes (#7).
	rp, err := parser.NewReusablePatternsFor(parser.ReusableConfig{
		Patterns:     cfg.Reusables.Patterns,
		Extensions:   cfg.Reusables.Extensions,
		ReusablesDir: reusablesDir,
		Root:         root,
		Resolver:     cfg.ResolvedProfile.Resolver,
		ImportMap:    cfg.ResolvedProfile.ImportMap,
		Cache:        cache,
	})
	if err != nil {
		return FileAnalysis{Path: filePath, RelativePath: relativePath}, fmt.Errorf("%s: %w", relativePath, err)
	}

	var sections []parser.Section
	var linesInfo []git.LineInfo

	if !cfg.FileLevelOnly {
		var blameErr error
		linesInfo, blameErr = git.GetBlameInfo(filePath)
		if blameErr != nil {
			// Git blame failed - file may not be tracked, or git error occurred
			// Continue with empty linesInfo, sections will have no line-level timestamps
			linesInfo = nil
		}
		if cfg.ParagraphLevel {
			sections = parser.ParseChunks(string(content), linesInfo, true, rp)
		} else {
			sections = parser.ParseSections(string(content), linesInfo, rp)
		}
	}

	// Analyze each section for staleness
	var staleSections []parser.Section
	allReusables := make(map[string]ReusableInfo)
	// Captures parser.ResolveReusable called out of scope; see the loop below.
	skippedRefs := make(map[string]struct{})
	var unresolvedReusableRefs []string
	var oldestSectionDate *time.Time

	for _, section := range sections {
		// Calculate effective staleness considering reusables
		effectiveDate := parser.CalculateSectionStaleness(&section, filePath, rp)

		if effectiveDate != nil && effectiveDate.Before(thresholdDate) {
			staleSections = append(staleSections, section)
		}

		// Track the oldest section date (for sorting)
		if effectiveDate != nil {
			if oldestSectionDate == nil || effectiveDate.Before(*oldestSectionDate) {
				oldestSectionDate = effectiveDate
			}
		}

		// Track reusables. The map is keyed by the raw capture so a reference
		// repeated across sections is only resolved once; the *reported* name
		// is rp.DisplayName's, which under the path resolver is the resolved
		// file's root-relative path rather than the capture. That is what
		// makes the cross-file aggregate correct: two pages in different
		// directories can both write "./shared.mdx" and mean different files.
		for _, reusableName := range section.Reusables {
			if _, skipped := skippedRefs[reusableName]; skipped {
				continue
			}
			if _, exists := allReusables[reusableName]; !exists {
				reusableInfo, resolution := parser.ResolveReusable(reusableName, filePath, rp)
				if resolution == parser.ResolutionSkipped {
					// Deliberately out of scope: a component the page renders
					// but never imported, or an import of something that is not
					// documentation. Not an include at all, so it earns neither
					// a row in the reusables table nor an unresolved count —
					// remembered so the next section does not re-ask (#68).
					skippedRefs[reusableName] = struct{}{}
					continue
				}
				var lastUpdated *time.Time
				var lastAuthor string
				if reusableInfo != nil {
					lastUpdated = &reusableInfo.LastModified
					lastAuthor = reusableInfo.LastAuthor
				}
				if reusableInfo == nil {
					// Nothing resolved (or what resolved has no history), so
					// this reference is reported "unknown". Recorded so the
					// CLI can say whether resolution actually failed anywhere,
					// and name the captures when it did (#7).
					unresolvedReusableRefs = append(unresolvedReusableRefs, reusableName)
				}
				isFresh := lastUpdated != nil && !lastUpdated.Before(thresholdDate)
				allReusables[reusableName] = ReusableInfo{
					Name:        rp.DisplayName(reusableName, filePath, reusableInfo),
					LastUpdated: lastUpdated,
					IsFresh:     isFresh,
					LastAuthor:  lastAuthor,
				}
			}
		}
	}

	// Calculate overall file staleness (most recent update)
	var effectiveLastUpdated *time.Time
	if fileInfo != nil {
		effectiveLastUpdated = &fileInfo.LastModified
	}

	// Also consider section dates for most recent
	for _, section := range sections {
		sectionDate := parser.CalculateSectionStaleness(&section, filePath, rp)
		if sectionDate != nil {
			if effectiveLastUpdated == nil || sectionDate.After(*effectiveLastUpdated) {
				effectiveLastUpdated = sectionDate
			}
		}
	}

	// Calculate days stale (based on most recent update)
	var daysStale int
	if effectiveLastUpdated != nil {
		daysStale = int(now.Sub(*effectiveLastUpdated).Hours() / 24)
	}

	// Calculate oldest section days (for sorting - files with oldest content first)
	var oldestSectionDays int
	if oldestSectionDate != nil {
		oldestSectionDays = int(now.Sub(*oldestSectionDate).Hours() / 24)
	}

	// Convert reusables map to slice, collapsing captures that turned out to
	// name the same file (e.g. "/snippets/a.mdx" and "snippets/a.mdx" on one
	// page both display as "snippets/a.mdx"). An entry that resolved wins over
	// one that did not, mirroring the cross-file merge in AnalyzeWithProgress.
	reusables := make([]ReusableInfo, 0, len(allReusables))
	byName := make(map[string]int, len(allReusables))
	for _, r := range allReusables {
		if i, seen := byName[r.Name]; seen {
			if reusables[i].LastUpdated == nil && r.LastUpdated != nil {
				reusables[i] = r
			}
			continue
		}
		byName[r.Name] = len(reusables)
		reusables = append(reusables, r)
	}
	// allReusables is ranged from a map, so without this the per-file
	// "**Reusables:**" line and the JSON array come out in a different order on
	// every run, making report diffs flap in CI.
	sort.Slice(reusables, func(i, j int) bool { return reusables[i].Name < reusables[j].Name })

	// No git history at all (no file-level commit and no blame timestamps) means
	// staleness is unknown for this file — record it so it is reported as such
	// instead of silently passing as fresh. See #55.
	historyMissing := fileInfo == nil && len(linesInfo) == 0

	return FileAnalysis{
		Path:                 filePath,
		RelativePath:         relativePath,
		FileInfo:             fileInfo,
		Sections:             sections,
		StaleSections:        staleSections,
		Reusables:            reusables,
		EffectiveLastUpdated: effectiveLastUpdated,
		OldestSectionDate:    oldestSectionDate,
		DaysStale:            daysStale,
		OldestSectionDays:    oldestSectionDays,
		HistoryMissing:       historyMissing,

		unresolvedReusableRefs: unresolvedReusableRefs,
	}, nil
}

// ProgressWriter is used to report analysis progress.
type ProgressWriter interface {
	io.Writer
}

// printProgress prints a progress bar to the writer.
func printProgress(w io.Writer, completed, total int) {
	if w == nil || total == 0 {
		return
	}

	pct := float64(completed) / float64(total) * 100
	barWidth := 30
	filled := int(float64(barWidth) * float64(completed) / float64(total))

	bar := strings.Repeat("=", filled)
	if filled < barWidth {
		bar += ">"
		bar += strings.Repeat(" ", barWidth-filled-1)
	}

	fmt.Fprintf(w, "\r[%s] %3.0f%% (%d/%d files)", bar, pct, completed, total)
}

// Analyze runs analysis on all markdown files in the content directory.
func Analyze(cfg *config.Config) (*Results, error) {
	return AnalyzeWithProgress(cfg, nil)
}

// AnalyzeWithProgress runs analysis with progress reporting.
func AnalyzeWithProgress(cfg *config.Config, progress ProgressWriter) (*Results, error) {
	if cfg.ContentDir == "" {
		return nil, os.ErrInvalid
	}

	// Callers that build a Config directly (rather than via the CLI) may not
	// have resolved a profile; do it here so extensions, reusable patterns and
	// the Hugo root get their profile defaults.
	if cfg.ResolvedProfile.Name == "" {
		if err := cfg.ApplyProfile(); err != nil {
			return nil, err
		}
	}

	// Fail fast on an uncompilable reusable pattern, before any worker starts.
	// analyzeFile never substitutes another profile's patterns (see #11), so an
	// invalid pattern is a configuration error, not something to paper over.
	if _, err := parser.NewReusablePatterns(cfg.Reusables.Patterns, cfg.Reusables.Extensions, "", ""); err != nil {
		return nil, fmt.Errorf("invalid reusables configuration: %w", err)
	}

	baseDir := cfg.ContentDir
	if !filepath.IsAbs(baseDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		baseDir = filepath.Join(cwd, baseDir)
	}

	// Build the allowed documentation-extension set once, plus the union of
	// every built-in profile's extensions: a file in the union but not in the
	// allowlist is documentation this run is not analyzing (an .mdx tree under
	// the markdown profile), and worth a note — unlike an image or a .json,
	// which is simply not documentation.
	extSet := contentExtensionSet(cfg.ContentExtensions)
	knownSet := contentExtensionSet(config.KnownContentExtensions())

	// Find all documentation files matching the allowed extensions, counting
	// the matches the exclusion rules drop so a zero-file run can say why, and
	// the documentation files the allowlist itself dropped (#11).
	var (
		mdFiles      []string
		excluded     int
		skippedExt   int
		skippedExts  = make(map[string]struct{})
		dirsSkipped  int
		skippedDirs  = make(map[string]struct{})
		dirsExcluded int
		gitIgnored   int
	)
	err := filepath.WalkDir(baseDir, func(filePath string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Exclusions prune whole subtrees, which is what makes them worth
			// having: the cost of a junk directory is one lstat, not a
			// git-blame per file inside it. The content root is never pruned —
			// it is the tree the user asked for, and it legitimately may be a
			// dot-directory, or (the common case) the repository root itself,
			// which holds the .git that would otherwise match (#69).
			if filePath == baseDir {
				return nil
			}
			// The user's exclude_dirs prunes first and regardless of
			// --no-default-excludes: that flag turns off the *built-in* rules,
			// never the list the user typed.
			if isUserExcludedDir(filePath, cfg, baseDir) {
				dirsExcluded++
				return filepath.SkipDir
			}
			if cfg.NoDefaultExcludes {
				return nil
			}
			if isDefaultExcludedDir(filePath, d.Name()) {
				dirsSkipped++
				skippedDirs[d.Name()] = struct{}{}
				return filepath.SkipDir
			}
			return nil
		}
		if isContentFile(filePath, extSet) {
			if shouldExclude(filePath, cfg, baseDir) {
				excluded++
			} else {
				mdFiles = append(mdFiles, filePath)
			}
			return nil
		}
		// Not analyzable under the active allowlist. Count it only if another
		// built-in profile would treat it as documentation and the exclusions
		// would not have dropped it anyway (no point suggesting --extensions
		// for a file exclude_dirs removes).
		if isContentFile(filePath, knownSet) && !shouldExclude(filePath, cfg, baseDir) {
			skippedExt++
			skippedExts[strings.ToLower(filepath.Ext(filePath))] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Drop the files git itself ignores, in one subprocess for the whole run.
	// An ignored file is not tracked documentation, so blame has nothing to say
	// about it and analyzing it only manufactures an "unknown" row. A tree that
	// is not a git repository (a supported case, reported as unknown) answers
	// with an error and is simply not filtered (#69).
	if !cfg.NoDefaultExcludes {
		mdFiles, gitIgnored = filterGitIgnored(baseDir, mdFiles)
	}

	// One git-lookup cache for the whole run, created before the pool starts
	// and shared by every worker: a shared include is resolved once rather
	// than once per referencing page (#65).
	fileInfoCache := git.NewFileInfoCache()

	// Determine number of workers
	workers := cfg.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	// Analyze files in parallel
	analyses := make([]FileAnalysis, len(mdFiles))
	var wg sync.WaitGroup
	fileChan := make(chan int, len(mdFiles))
	var completed int64
	total := len(mdFiles)

	// Progress reporter. reporterDone is closed when the goroutine has fully
	// returned, so callers (and tests) can be sure no further writes to the
	// progress writer happen after AnalyzeWithProgress returns.
	done := make(chan struct{})
	reporterDone := make(chan struct{})
	if progress != nil {
		go func() {
			defer close(reporterDone)
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					printProgress(progress, int(atomic.LoadInt64(&completed)), total)
				case <-done:
					printProgress(progress, total, total)
					fmt.Fprintln(progress) // newline after progress bar
					return
				}
			}
		}()
	} else {
		close(reporterDone)
	}

	// Start workers. The first per-file error is kept and returned after the
	// pool drains; remaining files still run so the progress bar completes.
	var (
		firstErr  error
		firstErrM sync.Mutex
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range fileChan {
				fa, ferr := analyzeFile(mdFiles[idx], cfg, baseDir, fileInfoCache)
				if ferr != nil {
					firstErrM.Lock()
					if firstErr == nil {
						firstErr = ferr
					}
					firstErrM.Unlock()
				}
				analyses[idx] = fa
				atomic.AddInt64(&completed, 1)
			}
		}()
	}

	// Send files to workers
	for i := range mdFiles {
		fileChan <- i
	}
	close(fileChan)

	wg.Wait()

	// Stop the progress reporter and wait for it to finish, so no progress
	// output races a caller writing to the same stream afterwards.
	if progress != nil {
		close(done)
	}
	<-reporterDone

	if firstErr != nil {
		return nil, firstErr
	}

	// Sort by relative path
	sort.Slice(analyses, func(i, j int) bool {
		return analyses[i].RelativePath < analyses[j].RelativePath
	})

	// Collect all unique reusables across all files. ReusableInfo.Name is the
	// reported identity, which under the path resolver is the resolved file's
	// root-relative path (see parser.ReusablePatterns.DisplayName) — keying on
	// the raw capture instead would merge two pages' distinct "./shared.mdx"
	// snippets into one row showing only the older date.
	reusableMap := make(map[string]ReusableInfo)
	unresolved := 0
	var unresolvedRefs []string
	seenUnresolved := make(map[string]struct{})
	for _, analysis := range analyses {
		unresolved += len(analysis.unresolvedReusableRefs)
		// The count stays per-file (two pages that both write "typo.mdx" name
		// two broken includes), but the *names* are de-duplicated: repeating
		// one capture in a list is noise, not diagnosis.
		for _, ref := range analysis.unresolvedReusableRefs {
			if _, seen := seenUnresolved[ref]; seen {
				continue
			}
			seenUnresolved[ref] = struct{}{}
			unresolvedRefs = append(unresolvedRefs, ref)
		}
		for _, r := range analysis.Reusables {
			existing, exists := reusableMap[r.Name]
			if !exists || (existing.LastUpdated == nil && r.LastUpdated != nil) {
				reusableMap[r.Name] = r
			}
		}
	}

	// Convert to sorted slice
	allReusables := make([]ReusableInfo, 0, len(reusableMap))
	for _, r := range reusableMap {
		allReusables = append(allReusables, r)
	}
	sort.Slice(allReusables, func(i, j int) bool {
		return allReusables[i].Name < allReusables[j].Name
	})

	return &Results{
		Files:           analyses,
		AllReusables:    allReusables,
		Config:          cfg,
		GeneratedAt:     nowFunc(),
		filesExcluded:   excluded,
		filesSkippedExt: skippedExt,
		skippedExts:     skippedExts,
		dirsSkipped:     dirsSkipped,
		skippedDirs:     skippedDirs,
		filesGitIgnored: gitIgnored,
		dirsExcluded:    dirsExcluded,

		unresolvedReusables: unresolved,
		unresolvedRefs:      unresolvedRefs,
	}, nil
}

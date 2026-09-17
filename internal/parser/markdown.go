// Package parser provides markdown parsing utilities.
package parser

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/git"
)

// Chunk represents a chunk of content (paragraph or section) within a markdown file.
type Chunk struct {
	Title     string // Header title or "Paragraph N"
	Level     int    // Header level (1 for #, 2 for ##, etc.) or 0 for paragraph
	StartLine int
	EndLine   int
	Lines     []git.LineInfo
	Reusables []string
	IsHeader  bool // True if this chunk starts with a header
}

// Section is an alias for Chunk for backward compatibility.
type Section = Chunk

// LastUpdated returns the most recent update timestamp for this chunk.
func (c *Chunk) LastUpdated() *time.Time {
	if len(c.Lines) == 0 {
		return nil
	}
	var latest time.Time
	for _, line := range c.Lines {
		if line.Timestamp.After(latest) {
			latest = line.Timestamp
		}
	}
	return &latest
}

// OldestLine returns the oldest line timestamp in this chunk.
func (c *Chunk) OldestLine() *time.Time {
	if len(c.Lines) == 0 {
		return nil
	}
	oldest := c.Lines[0].Timestamp
	for _, line := range c.Lines[1:] {
		if line.Timestamp.Before(oldest) {
			oldest = line.Timestamp
		}
	}
	return &oldest
}

// LastAuthor returns the author of the most recent update.
func (c *Chunk) LastAuthor() string {
	if len(c.Lines) == 0 {
		return ""
	}
	var latest git.LineInfo
	for _, line := range c.Lines {
		if line.Timestamp.After(latest.Timestamp) {
			latest = line
		}
	}
	return latest.Author
}

// DisplayTitle returns the chunk's display title. For header chunks this is the
// heading text; for paragraph chunks Title already carries the "(L<n>)" line
// marker set in createParagraphChunk, so the value is returned as-is either way.
func (c *Chunk) DisplayTitle() string {
	return c.Title
}

var (
	// Pattern to match markdown headers
	headerPattern = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
)

// ReusablePatterns holds compiled regex patterns for detecting reusables and
// the settings that decide how a detected reference is resolved to a file.
type ReusablePatterns struct {
	patterns   []*regexp.Regexp
	extensions []string
	// root is the resolved profile's project root: the Hugo site root holding
	// layouts/ and data/ under ResolverHugo, the docs project root that snippet
	// paths resolve against under ResolverPath. Empty when the profile has no
	// root concept or none was detected.
	root string
	// resolver decides what a captured reference means; see config.Resolver.
	resolver       config.Resolver
	reusablesDir   string              // Legacy: direct reusables directory
	filePaths      map[string]string   // Cache: name -> file path
	shortcodeCache map[string][]string // Cache: shortcode name -> data file paths
	cacheBuilt     bool
	// rootOnce guards resolvedRoot, the symlink-resolved form of root computed
	// on first use by resolvedRootPath.
	rootOnce     sync.Once
	resolvedRoot string
}

// ReusableConfig describes reusable detection and resolution for one run: the
// patterns to compile, the extensions tried when a reference carries none, the
// legacy reusables directory, the profile's project root and the resolver that
// says what a capture means (see config.Resolver).
type ReusableConfig struct {
	Patterns     []string
	Extensions   []string
	ReusablesDir string
	Root         string
	Resolver     config.Resolver
}

// NewReusablePatternsFor creates a ReusablePatterns from a resolved profile's
// reusable settings. Returns an error if any pattern fails to compile.
func NewReusablePatternsFor(rc ReusableConfig) (*ReusablePatterns, error) {
	rp := &ReusablePatterns{
		extensions:     rc.Extensions,
		root:           rc.Root,
		resolver:       rc.Resolver,
		reusablesDir:   rc.ReusablesDir,
		filePaths:      make(map[string]string),
		shortcodeCache: make(map[string][]string),
	}
	for _, p := range rc.Patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid reusable pattern %q: %w", p, err)
		}
		rp.patterns = append(rp.patterns, re)
	}
	return rp, nil
}

// NewReusablePatterns creates a new ReusablePatterns from config patterns,
// inferring the resolver the way rustydocs did before profiles carried one: a
// non-empty hugoRoot means Hugo shortcode resolution, an empty one means no
// root-based resolution at all. Callers that have a resolved profile should
// use NewReusablePatternsFor and pass its Resolver.
// Returns an error if any pattern fails to compile.
func NewReusablePatterns(patterns []string, extensions []string, reusablesDir string, hugoRoot string) (*ReusablePatterns, error) {
	resolver := config.ResolverNone
	if hugoRoot != "" {
		resolver = config.ResolverHugo
	}
	return NewReusablePatternsFor(ReusableConfig{
		Patterns:     patterns,
		Extensions:   extensions,
		ReusablesDir: reusablesDir,
		Root:         hugoRoot,
		Resolver:     resolver,
	})
}

// hugoProfile returns the built-in Hugo profile, the single source of truth for
// the default reusable-reference regexes and extensions.
func hugoProfile() config.Profile {
	p, ok := config.LookupProfile(config.ProfileHugo)
	if !ok {
		panic("parser: hugo profile missing from config registry")
	}
	return p
}

// DefaultReusablePatterns returns the Hugo profile's default patterns (Hugo
// shortcodes and MDX components) with no resolution roots.
func DefaultReusablePatterns() *ReusablePatterns {
	// These patterns are hardcoded and should always compile successfully
	hp := hugoProfile()
	rp, err := NewReusablePatterns(
		hp.ReusablePatterns,
		hp.ReusableExtensions,
		"",
		"",
	)
	if err != nil {
		// This should never happen with hardcoded patterns
		panic(fmt.Sprintf("default reusable patterns failed to compile: %v", err))
	}
	return rp
}

// ParseSections parses markdown content into chunks based on headers and paragraphs.
// Each header starts a new chunk, and within sections, paragraphs (separated by blank lines)
// are tracked separately for more granular staleness detection.
func ParseSections(content string, linesInfo []git.LineInfo, rp *ReusablePatterns) []Chunk {
	return ParseChunks(content, linesInfo, false, rp)
}

// ParseChunks parses markdown content into chunks.
// If paragraphLevel is true, it also splits by paragraphs within sections.
func ParseChunks(content string, linesInfo []git.LineInfo, paragraphLevel bool, rp *ReusablePatterns) []Chunk {
	// Normalize CRLF so Windows line endings don't leave a trailing \r in
	// section titles or content. Line counts are unchanged (split is still on
	// "\n"), so git-blame line-number alignment is preserved.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	contentLines := strings.Split(content, "\n")

	// Find all headers and their positions
	type header struct {
		lineNum int
		level   int
		title   string
	}
	var headers []header

	for i, line := range contentLines {
		if match := headerPattern.FindStringSubmatch(line); match != nil {
			level := len(match[1])
			title := strings.TrimSpace(match[2])
			headers = append(headers, header{
				lineNum: i + 1, // 1-indexed
				level:   level,
				title:   title,
			})
		}
	}

	if len(headers) == 0 {
		// No headers found, parse by paragraphs
		return parseParagraphs(contentLines, linesInfo, "(no header)", 0, rp)
	}

	// Create chunks from headers
	var chunks []Chunk
	for i, h := range headers {
		// Determine end line (start of next header or end of file)
		endLine := len(contentLines)
		if i+1 < len(headers) {
			endLine = headers[i+1].lineNum - 1
		}

		if paragraphLevel {
			// Parse paragraphs within this section
			sectionContent := contentLines[h.lineNum-1 : endLine]
			sectionChunks := parseParagraphs(sectionContent, linesInfo, h.title, h.lineNum-1, rp)
			// Mark the first chunk as the header
			if len(sectionChunks) > 0 {
				sectionChunks[0].IsHeader = true
				sectionChunks[0].Level = h.level
			}
			chunks = append(chunks, sectionChunks...)
		} else {
			// Get lines that belong to this section
			var sectionLines []git.LineInfo
			for _, li := range linesInfo {
				if li.LineNumber >= h.lineNum && li.LineNumber <= endLine {
					sectionLines = append(sectionLines, li)
				}
			}

			// Find reusables in this section
			sectionContent := strings.Join(contentLines[h.lineNum-1:endLine], "\n")
			reusables := FindReusables(sectionContent, rp)

			chunks = append(chunks, Chunk{
				Title:     h.title,
				Level:     h.level,
				StartLine: h.lineNum,
				EndLine:   endLine,
				Lines:     sectionLines,
				Reusables: reusables,
				IsHeader:  true,
			})
		}
	}

	return chunks
}

// parseParagraphs splits content into paragraph-level chunks.
func parseParagraphs(contentLines []string, linesInfo []git.LineInfo, parentTitle string, lineOffset int, rp *ReusablePatterns) []Chunk {
	var chunks []Chunk
	var currentStart int
	var inParagraph bool
	paragraphNum := 0

	for i, line := range contentLines {
		trimmed := strings.TrimSpace(line)
		isEmpty := trimmed == ""

		if !isEmpty && !inParagraph {
			// Start of a new paragraph
			currentStart = i
			inParagraph = true
		} else if isEmpty && inParagraph {
			// End of paragraph
			paragraphNum++
			chunk := createParagraphChunk(contentLines, linesInfo, currentStart, i-1, lineOffset, parentTitle, paragraphNum, rp)
			if len(chunk.Lines) > 0 {
				chunks = append(chunks, chunk)
			}
			inParagraph = false
		}
	}

	// Handle last paragraph if file doesn't end with blank line
	if inParagraph {
		paragraphNum++
		chunk := createParagraphChunk(contentLines, linesInfo, currentStart, len(contentLines)-1, lineOffset, parentTitle, paragraphNum, rp)
		if len(chunk.Lines) > 0 {
			chunks = append(chunks, chunk)
		}
	}

	// If no paragraphs found, return the whole content as one chunk
	if len(chunks) == 0 && len(contentLines) > 0 {
		startLine := lineOffset + 1
		endLine := lineOffset + len(contentLines)
		var chunkLines []git.LineInfo
		for _, li := range linesInfo {
			if li.LineNumber >= startLine && li.LineNumber <= endLine {
				chunkLines = append(chunkLines, li)
			}
		}
		chunks = append(chunks, Chunk{
			Title:     parentTitle,
			StartLine: startLine,
			EndLine:   endLine,
			Lines:     chunkLines,
			Reusables: FindReusables(strings.Join(contentLines, "\n"), rp),
		})
	}

	return chunks
}

func createParagraphChunk(contentLines []string, linesInfo []git.LineInfo, start, end, lineOffset int, parentTitle string, paragraphNum int, rp *ReusablePatterns) Chunk {
	startLine := lineOffset + start + 1
	endLine := lineOffset + end + 1

	var chunkLines []git.LineInfo
	for _, li := range linesInfo {
		if li.LineNumber >= startLine && li.LineNumber <= endLine {
			chunkLines = append(chunkLines, li)
		}
	}

	chunkContent := strings.Join(contentLines[start:end+1], "\n")
	reusables := FindReusables(chunkContent, rp)

	// Check if this paragraph starts with a header
	title := fmt.Sprintf("%s (L%d)", parentTitle, startLine)
	isHeader := false
	if match := headerPattern.FindStringSubmatch(contentLines[start]); match != nil {
		title = strings.TrimSpace(match[2])
		isHeader = true
	}

	return Chunk{
		Title:     title,
		StartLine: startLine,
		EndLine:   endLine,
		Lines:     chunkLines,
		Reusables: reusables,
		IsHeader:  isHeader,
	}
}

// FindReusables finds all reusable references in content using the given patterns.
func FindReusables(content string, rp *ReusablePatterns) []string {
	if rp == nil {
		rp = DefaultReusablePatterns()
	}
	var reusables []string
	seen := make(map[string]bool)
	for _, pattern := range rp.patterns {
		matches := pattern.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) > 1 && !seen[match[1]] {
				reusables = append(reusables, match[1])
				seen[match[1]] = true
			}
		}
	}
	return reusables
}

// GetReusableInfo returns git metadata for a reusable component referenced by
// sourceFile (the absolute path of the file the reference was found in; "" when
// it is not known). It resolves the reference to a file the way the active
// resolver prescribes and returns that file's most recent modification, or nil
// when nothing resolves — a reusable with no info is reported as unknown, never
// as fresh.
func GetReusableInfo(reusableName, sourceFile string, rp *ReusablePatterns) *git.FileInfo {
	if rp == nil {
		return nil
	}

	// The path resolver takes the capture literally: it is a file path, not a
	// name to look up by convention, so it is resolved on its own terms and
	// first. See lookupDirectPath.
	if rp.resolver == config.ResolverPath {
		if info := rp.lookupDirectPath(reusableName, sourceFile); info != nil {
			return info
		}
	}

	rp.ensureCache()

	name := normalizeReusableName(reusableName)

	// 1. Try direct lookup in reusablesDir (legacy)
	if rp.reusablesDir != "" {
		if info := rp.lookupInDir(name, rp.reusablesDir); info != nil {
			return info
		}
	}

	// 2. Try Hugo shortcode lookup
	if rp.resolver == config.ResolverHugo && rp.root != "" {
		if info := rp.lookupShortcode(name); info != nil {
			return info
		}
	}

	// 3. Try cached path lookup
	if info := rp.lookupPath(name); info != nil {
		return info
	}

	return nil
}

// lookupDirectPath resolves a captured path (config.ResolverPath) to a file and
// returns its git info. See resolveDirectPath for the lookup order. Resolution
// needs a known root: without one there is nothing to resolve against and
// nothing to bound the result, so nil is returned.
func (rp *ReusablePatterns) lookupDirectPath(ref, sourceFile string) *git.FileInfo {
	target, ok := rp.resolveDirectPath(ref, sourceFile)
	if !ok {
		return nil
	}
	return rp.mostRecentFile([]string{target})
}

// resolveDirectPath performs the config.ResolverPath lookup itself: it returns
// the existing file a captured path refers to, or ("", false) when nothing
// inside the project root matches. The bases searched, and why, are in
// directPathBases; the first candidate that exists under any of them wins.
//
// It is separate from lookupDirectPath because resolution and git history are
// independent questions — a snippet that exists but has never been committed
// resolves fine and simply has no history, and DisplayName needs the resolved
// path in exactly that case so two uncommitted files referenced by the same
// relative capture do not collapse into one reported row (#7).
func (rp *ReusablePatterns) resolveDirectPath(ref, sourceFile string) (string, bool) {
	ref = filepath.ToSlash(strings.TrimSpace(ref))
	if ref == "" || rp.root == "" {
		return "", false
	}

	bases, ref := rp.directPathBases(ref, sourceFile)
	if ref == "" {
		return "", false
	}

	for _, base := range bases {
		for _, candidate := range rp.pathCandidates(base, ref) {
			if !rp.withinRoot(candidate) {
				// A reference that leaves the docs project — with "..", or
				// through a symlink that points out of the tree — must not
				// pull an arbitrary file of the machine into a report. This
				// also filters candidates that do not exist at all.
				continue
			}
			if info, err := os.Stat(candidate); err != nil || info.IsDir() {
				continue
			}
			// The file exists; it is the answer, so no further candidate is
			// tried.
			return candidate, true
		}
	}
	return "", false
}

// snippetDirNames are the conventional snippet directories at a Mintlify
// project root, in lookup order. Mintlify's own docs use "snippets/"; the
// underscore-prefixed variant is common in the wild (a leading underscore
// keeps the directory out of the published navigation in several static site
// generators, and Mintlify projects migrated from them keep the name).
var snippetDirNames = []string{"snippets", "_snippets"}

// directPathBases returns the directories a captured reference is tried
// against, in order, together with the reference to try under them — the
// leading "/" of a root-absolute capture is consumed by the base choice. The
// shape of the capture decides:
//
//	"/snippets/x.mdx"  project-root-absolute: the root, and only the root.
//	"./x.mdx", "../x.mdx"  explicitly page-relative: the referencing page's
//	                   directory first, then the shared bases below (a
//	                   dot-slash reference to a file that turns out to live in
//	                   snippets/ still resolves rather than silently going
//	                   unknown).
//	"x.mdx", "cloud/x.mdx"  the common form, and the one that matters:
//	                   Mintlify resolves a bare file= against the project's
//	                   *snippets directory*, not against the page and not
//	                   against the root. So snippets/ and _snippets/ come
//	                   first, then the root, and only then the referencing
//	                   page's directory as a tolerant last resort.
//
// The ordering of the bare form is load-bearing. Real Mintlify projects
// (sequin, turso-docs, agno, elementary) overwhelmingly write
// <Snippet file="aws-access-key-config.mdx" /> meaning
// <root>/snippets/aws-access-key-config.mdx; searching the page directory and
// the root only, as rustydocs first did, resolved almost none of them and
// reported every snippet "unknown". A bare name that exists in both snippets/
// and next to the page resolves to snippets/, which is what Mintlify itself
// renders (#7).
func (rp *ReusablePatterns) directPathBases(ref, sourceFile string) (bases []string, rest string) {
	if rootRel, ok := strings.CutPrefix(ref, "/"); ok {
		return []string{rp.root}, rootRel
	}

	pageDir := ""
	if sourceFile != "" {
		pageDir = filepath.Dir(sourceFile)
	}

	shared := make([]string, 0, len(snippetDirNames)+2)
	for _, name := range snippetDirNames {
		shared = append(shared, filepath.Join(rp.root, name))
	}
	shared = append(shared, rp.root)

	switch {
	case pageDir == "":
		return shared, ref
	case strings.HasPrefix(ref, "./"), strings.HasPrefix(ref, "../"):
		return append([]string{pageDir}, shared...), ref
	default:
		return append(shared, pageDir), ref
	}
}

// pathCandidates returns the files tried for a slash-separated reference under
// one base directory, in order. A reference that already carries an extension
// is taken as-is; an extensionless one mirrors lookupInDir, trying each
// reusable extension and then the directory's index file.
func (rp *ReusablePatterns) pathCandidates(base, ref string) []string {
	joined := filepath.Join(base, filepath.FromSlash(ref))
	if filepath.Ext(ref) != "" {
		return []string{filepath.Clean(joined)}
	}
	candidates := make([]string, 0, 1+2*len(rp.extensions))
	candidates = append(candidates, filepath.Clean(joined))
	for _, ext := range rp.extensions {
		candidates = append(candidates, filepath.Clean(joined+ext))
	}
	for _, ext := range rp.extensions {
		candidates = append(candidates, filepath.Clean(filepath.Join(joined, "index"+ext)))
	}
	return candidates
}

// resolvedRootPath returns the project root with symlinks resolved, computed
// once per ReusablePatterns. Resolving the root matters as much as resolving
// the candidate: on macOS /var and /tmp are themselves symlinks (/var ->
// /private/var), so comparing a resolved candidate against an unresolved root
// would reject every legitimate snippet under a temp-dir checkout. The same
// resolve-both-sides rule is why git.GetFileLastModified resolves a file path
// before making it relative to `git rev-parse --show-toplevel`.
//
// A root that cannot be resolved (it does not exist) falls back to its
// absolute, lexically cleaned form so containment still rejects ".." escapes.
func (rp *ReusablePatterns) resolvedRootPath() string {
	rp.rootOnce.Do(func() {
		if rp.root == "" {
			return
		}
		abs := rp.root
		if a, err := filepath.Abs(abs); err == nil {
			abs = a
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			rp.resolvedRoot = resolved
			return
		}
		rp.resolvedRoot = filepath.Clean(abs)
	})
	return rp.resolvedRoot
}

// withinRoot reports whether a candidate path really lives inside the project
// root. Both sides have their symlinks resolved before the comparison, so
// containment is a fact about the filesystem rather than about the spelling of
// the path: a "snippets/out -> /elsewhere/secretrepo" symlink inside the root
// no longer lets <Snippet file="/snippets/out/passwd.mdx" /> fold a foreign
// repository's commit date into the report. A candidate that walks out of the
// root with ".." is still rejected, now after resolution rather than instead
// of it.
//
// A candidate that does not exist cannot be resolved and is simply not a match
// (filepath.EvalSymlinks fails on a missing path): the caller skips it and
// tries the next candidate, exactly as it does for a path that stats away. An
// empty root rejects everything.
func (rp *ReusablePatterns) withinRoot(candidate string) bool {
	root := rp.resolvedRootPath()
	if root == "" {
		return false
	}
	abs := candidate
	if a, err := filepath.Abs(candidate); err == nil {
		abs = a
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// Missing, broken symlink, or unreadable: not a match.
		return false
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// DisplayName returns the name a reusable referenced from sourceFile should be
// reported under. info is the git metadata the reference resolved to, or nil
// when it has none.
//
// Under the path resolver the captured reference is not a stable identity: two
// pages in different directories can both reference "./shared.mdx" and mean
// two different files, so reporting them under the raw capture collapses them
// into one row of the cross-file "Reusable Components" table. The resolved
// file's path relative to the project root is both unique and the more useful
// label, so it is what the reports show.
//
// Crucially the display path is derived from *resolution*, not from git: a
// snippet that exists but has never been committed has no info at all, and
// falling back to the raw capture there would re-collapse exactly the rows
// this exists to keep apart. Only a reference that resolves to no file inside
// the root (or one under another resolver) keeps the raw capture.
func (rp *ReusablePatterns) DisplayName(ref, sourceFile string, info *git.FileInfo) string {
	if rp == nil || rp.resolver != config.ResolverPath {
		return ref
	}
	root := rp.resolvedRootPath()
	if root == "" {
		return ref
	}

	var target string
	switch {
	case info != nil && info.Path != "":
		target = info.Path
	default:
		resolved, ok := rp.resolveDirectPath(ref, sourceFile)
		if !ok {
			return ref
		}
		target = resolved
	}

	abs := target
	if a, err := filepath.Abs(abs); err == nil {
		abs = a
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ref
	}
	return filepath.ToSlash(rel)
}

// layoutRoots returns the layouts directories searched for shortcode
// templates, in Hugo's own lookup order: the project's own layouts/ first, then
// the layouts/ of each theme under themes/ (sorted by theme directory name, as
// os.ReadDir returns them). A project template therefore always wins over a
// theme one. A missing or unreadable themes/ directory simply yields the
// project layouts.
func (rp *ReusablePatterns) layoutRoots() []string {
	roots := []string{filepath.Join(rp.root, "layouts")}
	themesDir := filepath.Join(rp.root, "themes")
	entries, err := os.ReadDir(themesDir)
	if err != nil {
		return roots
	}
	for _, e := range entries {
		if entryIsDir(themesDir, e) {
			roots = append(roots, filepath.Join(themesDir, e.Name(), "layouts"))
		}
	}
	return roots
}

// entryIsDir reports whether a directory entry names a directory, following
// symlinks. os.ReadDir reports the entry's own type as recorded by readdir, so
// a symlink is never IsDir() even when it points at a directory — and
// symlinking a checkout into themes/<name> is the standard Hugo local
// theme-development workflow. Only symlink entries are stat'ed, so the common
// case costs nothing extra; a broken symlink stats with an error and is
// skipped.
func entryIsDir(parent string, e fs.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&fs.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(filepath.Join(parent, e.Name()))
	return err == nil && info.IsDir()
}

// shortcodeCandidates returns the template paths tried for a shortcode name
// under one layouts directory, in the order they are tried.
func shortcodeCandidates(layoutsDir, name string) []string {
	shortcodesDir := filepath.Join(layoutsDir, "shortcodes")
	return []string{
		// shortcodes/{name}.html
		filepath.Join(shortcodesDir, name+".html"),
		// shortcodes/{name}/index.html (for shortcodes in subdirs)
		filepath.Join(shortcodesDir, name, "index.html"),
		// nested path: shortcodes/reusables/{name}.html
		filepath.Join(shortcodesDir, "reusables", name+".html"),
	}
}

// lookupShortcode finds a Hugo shortcode and traces its data file dependencies.
// The project's layouts/ is searched first, then each theme's layouts/ under
// themes/ (theme-provided shortcodes are common on sites whose only marker is a
// themes/ directory). Returns the most recent modification date from shortcode
// template and data files.
func (rp *ReusablePatterns) lookupShortcode(name string) *git.FileInfo {
	if rp.root == "" {
		return nil
	}

	// Check cache first
	if paths, ok := rp.shortcodeCache[name]; ok {
		return rp.mostRecentFile(paths)
	}

	// Look for the shortcode template, project layouts before theme layouts.
	shortcodePath := ""
	for _, layouts := range rp.layoutRoots() {
		for _, candidate := range shortcodeCandidates(layouts, name) {
			if _, err := os.Stat(candidate); err == nil {
				shortcodePath = candidate
				break
			}
		}
		if shortcodePath != "" {
			break
		}
	}

	if shortcodePath == "" {
		return nil
	}

	// Parse the shortcode template for data file references
	dataFiles := rp.parseShortcodeDataRefs(shortcodePath)

	// Collect all paths: shortcode template + data files
	allPaths := append([]string{shortcodePath}, dataFiles...)
	rp.shortcodeCache[name] = allPaths

	return rp.mostRecentFile(allPaths)
}

// parseShortcodeDataRefs parses a Hugo shortcode HTML for data file references.
func (rp *ReusablePatterns) parseShortcodeDataRefs(shortcodePath string) []string {
	data, err := os.ReadFile(filepath.Clean(shortcodePath))
	if err != nil {
		return nil
	}

	content := string(data)
	var dataFiles []string

	// Pattern 1: readFile "path"
	readFileRe := regexp.MustCompile(`readFile\s+"([^"]+)"`)
	for _, match := range readFileRe.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			fullPath := filepath.Join(rp.root, match[1])
			if _, err := os.Stat(fullPath); err == nil {
				dataFiles = append(dataFiles, fullPath)
			}
		}
	}

	// Pattern 2: partial "name" - look in layouts/partials/
	partialRe := regexp.MustCompile(`partial\s+"([^"]+)"`)
	for _, match := range partialRe.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			partialPath := filepath.Join(rp.root, "layouts", "partials", match[1])
			if !strings.HasSuffix(partialPath, ".html") {
				partialPath += ".html"
			}
			if _, err := os.Stat(partialPath); err == nil {
				dataFiles = append(dataFiles, partialPath)
			}
		}
	}

	// Pattern 3: .Site.Data.xxx or index .Site.Data "xxx" - look in data/
	// This is complex in Hugo, so we do a simple heuristic
	dataRe := regexp.MustCompile(`\.Site\.Data\.(\w+)`)
	for _, match := range dataRe.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			// Try common extensions
			for _, ext := range []string{".yaml", ".yml", ".json", ".toml"} {
				dataPath := filepath.Join(rp.root, "data", match[1]+ext)
				if _, err := os.Stat(dataPath); err == nil {
					dataFiles = append(dataFiles, dataPath)
					break
				}
			}
		}
	}

	return dataFiles
}

// mostRecentFile returns git info for the most recently modified file in the list.
func (rp *ReusablePatterns) mostRecentFile(paths []string) *git.FileInfo {
	var mostRecent *git.FileInfo
	for _, p := range paths {
		info, err := git.GetFileLastModified(p)
		if err != nil || info == nil {
			continue
		}
		if mostRecent == nil || info.LastModified.After(mostRecent.LastModified) {
			mostRecent = info
		}
	}
	return mostRecent
}

// lookupInDir tries to find a file in a directory by name.
func (rp *ReusablePatterns) lookupInDir(name, dir string) *git.FileInfo {
	for _, ext := range rp.extensions {
		candidate := filepath.Join(dir, name+ext)
		if info, err := git.GetFileLastModified(candidate); err == nil && info != nil {
			return info
		}
	}
	// Try as subdirectory with index file
	for _, ext := range rp.extensions {
		candidate := filepath.Join(dir, name, "index"+ext)
		if info, err := git.GetFileLastModified(candidate); err == nil && info != nil {
			return info
		}
	}
	return nil
}

func (rp *ReusablePatterns) ensureCache() {
	if rp.cacheBuilt {
		return
	}

	// Build cache from reusablesDir if set
	if rp.reusablesDir != "" {
		rp.buildDirCache(rp.reusablesDir)
	}

	rp.cacheBuilt = true
}

func (rp *ReusablePatterns) buildDirCache(dir string) {
	cleanDir := filepath.Clean(dir)
	extSet := make(map[string]struct{}, len(rp.extensions))
	for _, ext := range rp.extensions {
		extSet[ext] = struct{}{}
	}

	_ = filepath.WalkDir(cleanDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}

		ext := filepath.Ext(d.Name())
		if _, ok := extSet[ext]; !ok {
			return nil
		}

		base := strings.TrimSuffix(d.Name(), ext)
		rel, relErr := filepath.Rel(cleanDir, path)
		if relErr == nil {
			relNoExt := strings.TrimSuffix(rel, ext)
			rp.storePath(relNoExt, path)
		}
		rp.storePath(base, path)

		if base == "index" {
			parent := filepath.Base(filepath.Dir(path))
			if parent != "" && parent != "." {
				rp.storePath(parent, path)
			}
		}
		return nil
	})
}

func (rp *ReusablePatterns) storePath(key, path string) {
	if key == "" {
		return
	}
	key = strings.TrimPrefix(key, "./")
	key = strings.TrimPrefix(key, "/")
	key = filepath.ToSlash(key)
	if _, exists := rp.filePaths[key]; !exists {
		rp.filePaths[key] = filepath.Clean(path)
	}
}

func (rp *ReusablePatterns) lookupPath(name string) *git.FileInfo {
	if path, ok := rp.filePaths[name]; ok {
		fileInfo, err := git.GetFileLastModified(path)
		if err == nil && fileInfo != nil {
			return fileInfo
		}
	}
	return nil
}

func normalizeReusableName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimPrefix(name, "reusables/")
	name = filepath.ToSlash(name)
	return name
}

// CalculateSectionStaleness calculates the effective staleness date for a chunk
// of sourceFile (the absolute path of the file the chunk came from, "" when it
// is not known — a path-resolved reusable then loses its relative-to-the-page
// fallback). Takes into account both the chunk's own lines and any reusable
// components.
func CalculateSectionStaleness(section *Chunk, sourceFile string, rp *ReusablePatterns) *time.Time {
	var dates []time.Time

	// Get the most recent line date in the section
	if lastUpdated := section.LastUpdated(); lastUpdated != nil {
		dates = append(dates, *lastUpdated)
	}

	// Check reusable freshness
	for _, reusable := range section.Reusables {
		if info := GetReusableInfo(reusable, sourceFile, rp); info != nil {
			dates = append(dates, info.LastModified)
		}
	}

	if len(dates) == 0 {
		return nil
	}

	// Return the most recent date
	latest := dates[0]
	for _, d := range dates[1:] {
		if d.After(latest) {
			latest = d
		}
	}
	return &latest
}

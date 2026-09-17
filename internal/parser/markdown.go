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
	// cache memoizes the per-file `git log` lookups resolution performs. It is
	// owned by the caller and shared across every ReusablePatterns of a run —
	// one is built per file (see analyzer.analyzeFile), so a cache living here
	// would only ever dedupe within a single page. Nil means no caching.
	cache *git.FileInfoCache
	// importMap enables the MDX import-map layer (config.Profile.ImportMap):
	// a component capture is looked up among the symbols the referencing page
	// imported before it is treated as a path. importCache holds the built map
	// per source file; see importsFor.
	importMap   bool
	importCache map[string]map[string]importTarget
	// componentPattern records, per entry of patterns, whether it is the
	// shared component pattern (config.MDXComponentPattern) rather than a
	// pattern that only ever matches an include. It is what gives a capture a
	// provenance; see includeCaptures.
	componentPattern []bool
	// includeCaptures holds the captures FindReusables saw come out of an
	// *include* pattern — <Snippet file="…" /> today. Such a capture is an
	// include by construction and must resolve or be counted unresolved, so it
	// is never classified ResolutionSkipped, however component-shaped it looks
	// (a capitalised, extensionless "AlsoMissing" is a perfectly ordinary
	// snippet name, and the path resolver supports exactly that spelling
	// through its .mdx/.md/index.* fallback).
	//
	// A capture that is not in here carries no provenance — resolution was
	// asked about a name FindReusables never produced — and falls back to the
	// shape heuristic, which is the pre-#68 behaviour.
	//
	// No locking, for the same reason importCache needs none: a
	// ReusablePatterns belongs to one worker for the duration of one file.
	includeCaptures map[string]struct{}
	// dirEntries memoizes the directory listings caseExactUnder reads to check
	// a candidate's spelling against the filesystem's. A nil entry records a
	// directory that exists but could not be listed; see dirHasEntry.
	dirEntries map[string]map[string]struct{}
}

// Resolution says what became of a reusable reference, so the caller can tell a
// broken include from one that is out of scope by design.
//
// The distinction exists for the import map (#68). A Mintlify page renders
// <Card>, <Tabs> and <Accordion> — capitalised tags that the component pattern
// captures and that name no file at all — and imports React components whose
// dates must never be folded in. Reporting either as an unresolved include
// would bury the handful of genuinely broken snippet references among thousands
// of non-problems, which is the failure the unresolved-reusables note was added
// to prevent in the first place.
type Resolution int

const (
	// ResolutionResolved: the reference named a file with git history, which
	// the caller folds into the section's freshness.
	ResolutionResolved Resolution = iota
	// ResolutionUnresolved: the reference should have named a file and did
	// not — it is missing, or it exists but has never been committed. Reported
	// as unknown and counted, since it is a defect a reader can act on.
	ResolutionUnresolved
	// ResolutionSkipped: the reference is deliberately out of scope. Under the
	// import map that is a capitalised tag no import introduced (a built-in or
	// layout component) or an import of something that is not documentation
	// (.jsx, .js, .css). Not a failure, not counted, and not reported at all.
	ResolutionSkipped
)

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
	// ImportMap enables the MDX import-map layer on top of the resolver; see
	// config.Profile.ImportMap (#68).
	ImportMap bool
	// Cache, when non-nil, memoizes the git lookups resolution performs. It is
	// created once per analysis run and shared by every file's
	// ReusablePatterns; nil disables caching (#65).
	Cache *git.FileInfoCache
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
		cache:          rc.Cache,
		importMap:      rc.ImportMap,
	}
	for _, p := range rc.Patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid reusable pattern %q: %w", p, err)
		}
		rp.patterns = append(rp.patterns, re)
		// Provenance is decided by pattern identity rather than by inspecting
		// the capture: a profile lists config.MDXComponentPattern verbatim when
		// it wants component usage read, and anything else it lists is an
		// include form.
		rp.componentPattern = append(rp.componentPattern, p == config.MDXComponentPattern)
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
	for i, pattern := range rp.patterns {
		matches := pattern.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) <= 1 {
				continue
			}
			// Record provenance before the de-duplication, not after: a
			// capture first seen from the component pattern and then from an
			// include pattern is an include, and the second sighting is the
			// one that says so.
			if !rp.isComponentPattern(i) {
				rp.noteIncludeCapture(match[1])
			}
			if !seen[match[1]] {
				reusables = append(reusables, match[1])
				seen[match[1]] = true
			}
		}
	}
	return reusables
}

// isComponentPattern reports whether patterns[i] is the shared component
// pattern. A ReusablePatterns built before componentPattern existed (or by a
// caller that bypassed NewReusablePatternsFor) has a short slice; treating the
// missing entries as component patterns keeps the pre-provenance behaviour.
func (rp *ReusablePatterns) isComponentPattern(i int) bool {
	if i >= len(rp.componentPattern) {
		return true
	}
	return rp.componentPattern[i]
}

// noteIncludeCapture records that this capture came out of an include pattern,
// so ResolveReusable will never write it off as a component (#68 regression
// against #7).
func (rp *ReusablePatterns) noteIncludeCapture(ref string) {
	if rp.includeCaptures == nil {
		rp.includeCaptures = make(map[string]struct{})
	}
	rp.includeCaptures[ref] = struct{}{}
}

// fromIncludePattern reports whether FindReusables produced this capture from
// an include pattern on this run.
func (rp *ReusablePatterns) fromIncludePattern(ref string) bool {
	_, ok := rp.includeCaptures[ref]
	return ok
}

// GetReusableInfo returns git metadata for a reusable component referenced by
// sourceFile (the absolute path of the file the reference was found in; "" when
// it is not known). It resolves the reference to a file the way the active
// resolver prescribes and returns that file's most recent modification, or nil
// when nothing resolves — a reusable with no info is reported as unknown, never
// as fresh.
func GetReusableInfo(reusableName, sourceFile string, rp *ReusablePatterns) *git.FileInfo {
	info, _ := ResolveReusable(reusableName, sourceFile, rp)
	return info
}

// ResolveReusable is GetReusableInfo plus the reason there is no info: see
// Resolution. Callers that report on failures (the analyzer's unresolved count,
// and through it the CLI note) must use this form, because a nil info alone
// cannot distinguish a broken include from a reference that was never meant to
// resolve.
//
// The import map is consulted first when the profile enables it, because a
// symbol the page imported is unambiguous evidence of what the capture means —
// more so than any path heuristic. A capture that no import introduced falls
// through to the ordinary resolution below, so <Snippet file="…" /> keeps
// working on the same page as imports; only if that also finds nothing is a
// component-shaped capture called skipped rather than unresolved (#68).
func ResolveReusable(reusableName, sourceFile string, rp *ReusablePatterns) (*git.FileInfo, Resolution) {
	if rp == nil {
		return nil, ResolutionUnresolved
	}

	// The map answers questions about *symbols*. A capture an include pattern
	// produced is a path, not a symbol, so it is resolved as one even on a page
	// that happens to bind the same name (#68).
	if rp.importMap && !rp.fromIncludePattern(reusableName) {
		if target, ok := rp.importsFor(sourceFile)[reusableName]; ok {
			if target.skipped {
				return nil, ResolutionSkipped
			}
			if target.path != "" {
				if info := rp.mostRecentFile([]string{target.path}); info != nil {
					return info, ResolutionResolved
				}
			}
			// A content import that named no file, or one that exists but has
			// never been committed: a real defect, reported unknown.
			return nil, ResolutionUnresolved
		}
	}

	if info := rp.resolveExisting(reusableName, sourceFile); info != nil {
		return info, ResolutionResolved
	}

	// Under the import map, a capitalised tag that resolved to nothing is a
	// component the page renders rather than an include it is missing — the
	// overwhelming majority of captures on a real MDX page. Out of scope, not a
	// failure.
	//
	// Only a capture the *component* pattern produced qualifies. A capture from
	// an include pattern — <Snippet file="AlsoMissing" /> — is an include by
	// construction, and "capitalised and extensionless" is a shape the path
	// resolver explicitly supports, so classifying it skipped made a broken
	// snippet vanish from the report, the unresolved count and the stderr note
	// alike (#68 regression against #7).
	if rp.importMap && isComponentSymbol(reusableName) && !rp.fromIncludePattern(reusableName) {
		return nil, ResolutionSkipped
	}
	return nil, ResolutionUnresolved
}

// componentSymbolPattern matches a capture that can only be a JSX component
// name: capitalised, and carrying neither an extension nor a path separator, so
// it can never be mistaken for the file path a ResolverPath capture usually is.
var componentSymbolPattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9_$]*$`)

func isComponentSymbol(ref string) bool {
	return componentSymbolPattern.MatchString(ref)
}

// resolveExisting performs the pre-import-map resolution: the path resolver,
// then the legacy reusables directory, the Hugo shortcode lookup and the cached
// path lookup. Returns nil when nothing resolves.
func (rp *ReusablePatterns) resolveExisting(reusableName, sourceFile string) *git.FileInfo {
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
			if !rp.caseExactUnder(base, candidate) {
				// A case-insensitive filesystem said yes to a spelling the
				// file does not actually have — <Note /> finding
				// snippets/note.mdx. Accepting it would make the report
				// depend on which operating system ran rustydocs; see
				// caseExactUnder.
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
		// No history, so the display path has to come from resolution. An
		// imported symbol resolves through the map — otherwise a snippet that
		// is imported but not yet committed would be reported under its bare
		// symbol name, which is exactly the collapsing this function exists to
		// prevent.
		resolved, ok := rp.displayTarget(ref, sourceFile)
		if !ok {
			return ref
		}
		target = resolved
	}

	abs := target
	if a, err := filepath.Abs(abs); err == nil {
		abs = a
	}

	// Containment is decided on the symlink-resolved form, so a path reached
	// through a symlinked tree is still recognised as inside the project. The
	// *label*, though, is built from the spelling the reference actually used:
	// filepath.EvalSymlinks case-normalises on Windows and nowhere else, so
	// resolving the label would relabel a broken "/snippets/Note.mdx" as the
	// neighbouring "snippets/note.mdx" that happens to exist — pointing the
	// reader at the wrong file for a reference that did not resolve, and
	// reporting a different name per platform for one repository.
	checked := abs
	if resolved, err := filepath.EvalSymlinks(checked); err == nil {
		checked = resolved
	}
	if !relWithin(root, checked) {
		return ref
	}

	// Prefer the unresolved spelling; fall back to the resolved one when the
	// two roots disagree (an unresolved path under a symlinked root will not
	// be relative to the resolved root at all).
	if rel, ok := relUnder(rp.root, abs); ok {
		return rel
	}
	rel, ok := relUnder(root, checked)
	if !ok {
		return ref
	}
	return rel
}

// relWithin reports whether path sits inside base.
func relWithin(base, path string) bool {
	_, ok := relUnder(base, path)
	return ok
}

// relUnder returns path relative to base in slash form, and whether it is
// inside base at all. base is made absolute first so a relative project root
// still works.
func relUnder(base, path string) (string, bool) {
	if base == "" {
		return "", false
	}
	if a, err := filepath.Abs(base); err == nil {
		base = a
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// displayTarget returns the file a reference names, for labelling purposes: the
// import map's answer when the profile has one and the symbol was imported,
// otherwise the ordinary path resolution. A deliberately skipped import (a
// .jsx component) has no file to name and reports false.
//
// An import that resolved to nothing still names a file, and that name is what
// is reported: a broken "import Card from '/snippets/Card.mdx'" belongs in the
// table under snippets/Card.mdx, not under "Card", which every other page's
// unrelated Card would share (#68 review). See importTarget.named.
func (rp *ReusablePatterns) displayTarget(ref, sourceFile string) (string, bool) {
	if rp.importMap && !rp.fromIncludePattern(ref) {
		if target, ok := rp.importsFor(sourceFile)[ref]; ok {
			if target.skipped {
				return "", false
			}
			if target.path != "" {
				return target.path, true
			}
			return target.named, target.named != ""
		}
	}
	return rp.resolveDirectPath(ref, sourceFile)
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
			if _, err := os.Stat(candidate); err != nil {
				continue
			}
			if !rp.caseExactUnder(rp.root, candidate) {
				// {{< Note >}} must not pick up shortcodes/note.html on a
				// case-insensitive filesystem; see caseExactUnder.
				continue
			}
			shortcodePath = candidate
			break
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
// Every path it derives from the template's text is case-exact-checked for the
// same reason resolution is: the date it contributes must not depend on the
// case-folding rules of the filesystem the run happened on.
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
			if _, err := os.Stat(fullPath); err == nil && rp.caseExactUnder(rp.root, fullPath) {
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
			if _, err := os.Stat(partialPath); err == nil && rp.caseExactUnder(rp.root, partialPath) {
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
				if _, err := os.Stat(dataPath); err == nil && rp.caseExactUnder(rp.root, dataPath) {
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
		info, err := rp.cache.FileLastModified(p)
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
//
// The candidate's spelling is checked against the filesystem's before git is
// asked, for the reason caseExactUnder gives: on Windows the path git receives
// has been case-normalised on the way, so a capture that differs from the file
// name only in case resolves there and nowhere else.
func (rp *ReusablePatterns) lookupInDir(name, dir string) *git.FileInfo {
	for _, ext := range rp.extensions {
		candidate := filepath.Join(dir, name+ext)
		if !rp.caseExactUnder(dir, candidate) {
			continue
		}
		if info, err := rp.cache.FileLastModified(candidate); err == nil && info != nil {
			return info
		}
	}
	// Try as subdirectory with index file
	for _, ext := range rp.extensions {
		candidate := filepath.Join(dir, name, "index"+ext)
		if !rp.caseExactUnder(dir, candidate) {
			continue
		}
		if info, err := rp.cache.FileLastModified(candidate); err == nil && info != nil {
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
		fileInfo, err := rp.cache.FileLastModified(path)
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

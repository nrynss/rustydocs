// Package parser provides markdown parsing utilities.
package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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

// ReusablePatterns holds compiled regex patterns for detecting reusables.
type ReusablePatterns struct {
	patterns       []*regexp.Regexp
	extensions     []string
	hugoRoot       string              // Hugo project root (contains layouts/, data/)
	reusablesDir   string              // Legacy: direct reusables directory
	filePaths      map[string]string   // Cache: name -> file path
	shortcodeCache map[string][]string // Cache: shortcode name -> data file paths
	cacheBuilt     bool
}

// NewReusablePatterns creates a new ReusablePatterns from config patterns.
// Returns an error if any pattern fails to compile.
func NewReusablePatterns(patterns []string, extensions []string, reusablesDir string, hugoRoot string) (*ReusablePatterns, error) {
	rp := &ReusablePatterns{
		extensions:     extensions,
		reusablesDir:   reusablesDir,
		hugoRoot:       hugoRoot,
		filePaths:      make(map[string]string),
		shortcodeCache: make(map[string][]string),
	}
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid reusable pattern %q: %w", p, err)
		}
		rp.patterns = append(rp.patterns, re)
	}
	return rp, nil
}

// defaultReusablePatternStrings are the built-in reusable-reference regexes used
// by DefaultReusablePatterns. Exposed at package scope so tests can build a
// ReusablePatterns with custom roots from the same source of truth.
var defaultReusablePatternStrings = []string{
	// Hugo shortcodes: {{< name >}}, {{% name %}}, {{< name param >}}, etc.
	`\{\{[<%]\s*([a-zA-Z][\w/-]*)\s*[^%>]*[%>]\}\}`,
	// MDX/JSX components: <Component>, <Component />, <Component prop="val">
	`<([A-Z][a-zA-Z0-9]*)\s*[^>]*/?>`,
}

// DefaultReusablePatterns returns default patterns for Hugo shortcodes and MDX components.
func DefaultReusablePatterns() *ReusablePatterns {
	// These patterns are hardcoded and should always compile successfully
	rp, err := NewReusablePatterns(
		defaultReusablePatternStrings,
		[]string{".md", ".mdx", ".html"},
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

// GetReusableInfo returns git metadata for a reusable component.
// It traces shortcodes to their source files and returns the most recent modification.
func GetReusableInfo(reusableName string, rp *ReusablePatterns) *git.FileInfo {
	if rp == nil {
		return nil
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
	if rp.hugoRoot != "" {
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

// lookupShortcode finds a Hugo shortcode and traces its data file dependencies.
// Returns the most recent modification date from shortcode template and data files.
func (rp *ReusablePatterns) lookupShortcode(name string) *git.FileInfo {
	if rp.hugoRoot == "" {
		return nil
	}

	// Check cache first
	if paths, ok := rp.shortcodeCache[name]; ok {
		return rp.mostRecentFile(paths)
	}

	// Look for shortcode template
	shortcodesDir := filepath.Join(rp.hugoRoot, "layouts", "shortcodes")
	shortcodePath := ""

	// Try: layouts/shortcodes/{name}.html
	candidate := filepath.Join(shortcodesDir, name+".html")
	if _, err := os.Stat(candidate); err == nil {
		shortcodePath = candidate
	}

	// Try: layouts/shortcodes/{name}/index.html (for shortcodes in subdirs)
	if shortcodePath == "" {
		candidate = filepath.Join(shortcodesDir, name, "index.html")
		if _, err := os.Stat(candidate); err == nil {
			shortcodePath = candidate
		}
	}

	// Try nested path: layouts/shortcodes/reusables/{name}.html
	if shortcodePath == "" {
		candidate = filepath.Join(shortcodesDir, "reusables", name+".html")
		if _, err := os.Stat(candidate); err == nil {
			shortcodePath = candidate
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
			fullPath := filepath.Join(rp.hugoRoot, match[1])
			if _, err := os.Stat(fullPath); err == nil {
				dataFiles = append(dataFiles, fullPath)
			}
		}
	}

	// Pattern 2: partial "name" - look in layouts/partials/
	partialRe := regexp.MustCompile(`partial\s+"([^"]+)"`)
	for _, match := range partialRe.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			partialPath := filepath.Join(rp.hugoRoot, "layouts", "partials", match[1])
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
				dataPath := filepath.Join(rp.hugoRoot, "data", match[1]+ext)
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

// CalculateSectionStaleness calculates the effective staleness date for a chunk.
// Takes into account both the chunk's own lines and any reusable components.
func CalculateSectionStaleness(section *Chunk, rp *ReusablePatterns) *time.Time {
	var dates []time.Time

	// Get the most recent line date in the section
	if lastUpdated := section.LastUpdated(); lastUpdated != nil {
		dates = append(dates, *lastUpdated)
	}

	// Check reusable freshness
	for _, reusable := range section.Reusables {
		if info := GetReusableInfo(reusable, rp); info != nil {
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

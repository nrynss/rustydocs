// Package parser provides markdown parsing utilities.
package parser

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/nrynss/rustydocs/internal/git"
)

// Chunk represents a chunk of content (paragraph or section) within a markdown file.
type Chunk struct {
	LogicalKey   string
	HeadingPath  []string
	Fingerprint  string
	Dependencies []Dependency
	IsStale      bool

	Title     string // Header title or "Paragraph N"
	Level     int    // Header level (1 for #, 2 for ##, etc.) or 0 for paragraph
	StartLine int
	EndLine   int
	Lines     []git.LineInfo
	Reusables []string
	IsHeader  bool // True if this chunk starts with a header
	// EffectiveLastUpdated is the date the staleness classification used for
	// this section: its own lines' most recent commit folded with the commit
	// dates of its resolved includes (CalculateSectionStaleness) — the max of
	// the two, so it is never older than the section's own latest line. The
	// analyzer sets it on the stale sections it reports, and DisplayDate
	// returns it, so a stale row's date and day count are always the ones the
	// count itself was decided on: a blameless section stale through an old
	// include shows the include's date instead of "Unknown", and a section
	// whose include is newer than its own text does not read staler than it
	// was counted. Nil when nothing resolvable dates the section.
	EffectiveLastUpdated *time.Time
}

// DisplayDate returns the date a stale-section row should show: the date the
// classification used (EffectiveLastUpdated — own latest line folded with
// resolved includes) when the analyzer recorded one, else the section's own
// most recent line date. Nil when there is no resolvable date at all, which
// renders as "Unknown" (#56).
func (c *Chunk) DisplayDate() *time.Time {
	if c.EffectiveLastUpdated != nil {
		return c.EffectiveLastUpdated
	}
	return c.LastUpdated()
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
	if latest.IsZero() {
		return nil
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

// ParseSections parses markdown content into chunks based on headers and paragraphs.
// Each header starts a new chunk, and within sections, paragraphs (separated by blank lines)
// are tracked separately for more granular staleness detection.
func ParseSections(content string, linesInfo []git.LineInfo, rp *ReusablePatterns) []Chunk {
	return ParseChunks(content, linesInfo, false, rp)
}

// ParseChunks parses markdown content into chunks.
// If paragraphLevel is true, it also splits by paragraphs within sections.
func ParseChunks(content string, linesInfo []git.LineInfo, paragraphLevel bool, rp *ReusablePatterns) (chunks []Chunk) {
	original := strings.ReplaceAll(content, "\r\n", "\n")
	defer func() { enrichChunks(chunks, original, rp) }()
	// Normalize CRLF so Windows line endings don't leave a trailing \r in
	// section titles or content. Line counts are unchanged (split is still on
	// "\n"), so git-blame line-number alignment is preserved.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	{
		// Keep nonblank fenced lines nonblank so paragraph chunks retain
		// their blame history, while hiding headings and references.
		masked := []byte(content)
		for _, span := range fencedSpans(content) {
			lineHasContent := false
			for i := span[0]; i < span[1]; i++ {
				if masked[i] == '\n' {
					lineHasContent = false
				} else if !lineHasContent && masked[i] != ' ' && masked[i] != '\t' {
					masked[i] = 'x'
					lineHasContent = true
				} else {
					masked[i] = ' '
				}
			}
		}
		content = string(masked)
	}
	if rp != nil && rp.importMap {
		var ignored map[int]bool
		content, ignored = maskImports(content)
		kept := make([]git.LineInfo, 0, len(linesInfo))
		for _, line := range linesInfo {
			if !ignored[line.LineNumber] {
				kept = append(kept, line)
			}
		}
		linesInfo = kept
		if strings.TrimSpace(content) == "" {
			return nil
		}
	}
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
		return parseParagraphs(contentLines, linesInfo, noHeaderTitle, 0, rp)
	}

	// Create chunks from headers
	// Anything above the first header is the page preamble: prose, a note, or
	// a rendered include sitting under the frontmatter and before any heading.
	// It used to be discarded outright, which hid its blame dates and, worse,
	// every reusable referenced only there (#70).
	chunks = append(chunks, parsePreamble(contentLines, linesInfo, headers[0].lineNum, paragraphLevel, rp)...)

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

const (
	// noHeaderTitle labels the chunks of a file that has no header at all.
	noHeaderTitle = "(no header)"
	// preambleTitle labels the chunk holding the content above the first
	// header of a file that *does* have headers (#70).
	//
	// It is deliberately not "(no header)": in a file with headings that would
	// read as a claim about the whole page, and it must also not borrow the
	// first heading's text, which would put two rows with the same title and
	// different line ranges next to each other in the report. The parenthesised
	// lowercase form matches the existing convention, so neither title can
	// collide with a real heading — a heading rendering as literal "(preamble)"
	// is indistinguishable by design, and harmless.
	preambleTitle = "(preamble)"
)

// parsePreamble chunks the span above a file's first header, which sits at
// 1-indexed line firstHeaderLine. It returns nil when that span holds nothing
// worth reporting: no lines at all, only blank lines, or only frontmatter.
//
// Frontmatter is skipped rather than chunked. A YAML (`---`) or TOML (`+++`)
// block at the very top of the file is metadata — title, description, sidebar
// weight — not prose, and folding it in would give a preamble chunk to
// essentially every page in a docs tree while attributing a bulk metadata edit
// to the page's prose. Skipping it costs nothing here, because a page with
// headers is already represented in the report by those sections.
//
// The headerless path in ParseChunks deliberately keeps its existing behaviour
// of chunking frontmatter along with everything else: there the chunks are the
// page's only representation, so dropping the frontmatter of a frontmatter-only
// stub would erase the file from the report entirely.
func parsePreamble(contentLines []string, linesInfo []git.LineInfo, firstHeaderLine int, paragraphLevel bool, rp *ReusablePatterns) []Chunk {
	start := frontmatterLines(contentLines) // 0-indexed start of the preamble
	end := firstHeaderLine - 1              // exclusive, 0-indexed: the header line itself
	if end > len(contentLines) {
		end = len(contentLines)
	}
	if start >= end {
		return nil
	}

	preambleLines := contentLines[start:end]
	if !hasContent(preambleLines) {
		return nil
	}

	if paragraphLevel {
		// Same treatment sections get under --paragraph-level: the preamble is
		// split on blank lines, each paragraph titled "(preamble) (L<n>)" by
		// createParagraphChunk. No chunk is marked IsHeader, because none of
		// them starts with one.
		return parseParagraphs(preambleLines, linesInfo, preambleTitle, start, rp)
	}

	startLine := start + 1 // 1-indexed, to match git.LineInfo.LineNumber
	var chunkLines []git.LineInfo
	for _, li := range linesInfo {
		if li.LineNumber >= startLine && li.LineNumber <= end {
			chunkLines = append(chunkLines, li)
		}
	}
	return []Chunk{{
		Title:     preambleTitle,
		Level:     0,
		StartLine: startLine,
		EndLine:   end,
		Lines:     chunkLines,
		Reusables: FindReusables(strings.Join(preambleLines, "\n"), rp),
		IsHeader:  false,
	}}
}

// frontmatterLines returns the number of leading lines taken up by a YAML
// (`---`) or TOML (`+++`) frontmatter block, including both delimiters, or 0
// when the file does not open with one.
//
// The opening delimiter must be the very first line and the block must be
// closed; an unterminated one is ordinary content — a lone `---` on line 1 is a
// legal thematic break, and reading the rest of the file as metadata because of
// it would be far worse than treating a genuinely broken block as prose.
func frontmatterLines(contentLines []string) int {
	if len(contentLines) == 0 {
		return 0
	}
	delim := strings.TrimRight(contentLines[0], " \t")
	if delim != "---" && delim != "+++" {
		return 0
	}
	for i := 1; i < len(contentLines); i++ {
		if strings.TrimRight(contentLines[i], " \t") == delim {
			return i + 1
		}
	}
	return 0
}

// hasContent reports whether any line is more than whitespace.
func hasContent(lines []string) bool {
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			return true
		}
	}
	return false
}

// parseParagraphs splits content into paragraph-level chunks.
//
// A paragraph chunk exists to carry the blame of its lines, and a chunk with
// none would have nothing to date, so chunks whose Lines came back empty used
// to be dropped. But blame covers every line of a file: empty Lines can only
// mean the file has no resolvable history — an untracked file that was never
// committed, a tree outside any repository, a blame failure — and dropping
// every chunk then collapsed a headerless file into one whole-file row,
// erasing exactly the structure the chunking exists to show. A shallow clone
// does not do this: blame there still dates every line, to the tip commit.
// With no history every chunk is unknown regardless, so the structure is kept
// instead: the empty-Lines guard only applies when there is blame to compare
// against.
func parseParagraphs(contentLines []string, linesInfo []git.LineInfo, parentTitle string, lineOffset int, rp *ReusablePatterns) []Chunk {
	var chunks []Chunk
	var currentStart int
	var inParagraph bool
	paragraphNum := 0
	keepChunk := len(linesInfo) > 0

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
			if len(chunk.Lines) > 0 || !keepChunk {
				chunks = append(chunks, chunk)
			}
			inParagraph = false
		}
	}

	// Handle last paragraph if file doesn't end with blank line
	if inParagraph {
		paragraphNum++
		chunk := createParagraphChunk(contentLines, linesInfo, currentStart, len(contentLines)-1, lineOffset, parentTitle, paragraphNum, rp)
		if len(chunk.Lines) > 0 || !keepChunk {
			chunks = append(chunks, chunk)
		}
	}

	// If no paragraphs found, return the whole content as one chunk. With no
	// history this can only be an all-blank span: a headerless file with
	// paragraphs now keeps them (see keepChunk above).
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

// createParagraphChunk builds one paragraph chunk from
// contentLines[start:end+1], carrying the blame of the lines it covers and
// the references found in its content.
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

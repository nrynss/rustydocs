package parser

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/git"
)

// blameEveryLine fabricates one blame entry per line of content, all stamped
// with the same timestamp unless the line number appears in overrides. It lets
// the shape-level tests assert which lines landed in which chunk without
// needing a git repository; the date-sensitive assertions live in the analyzer
// tests, which use internal/testutil.
func blameEveryLine(content string, base time.Time, overrides map[int]time.Time) []git.LineInfo {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	info := make([]git.LineInfo, 0, len(lines))
	for i, l := range lines {
		ts := base
		if o, ok := overrides[i+1]; ok {
			ts = o
		}
		info = append(info, git.LineInfo{
			LineNumber: i + 1,
			Author:     "Test User",
			Timestamp:  ts,
			Content:    l,
		})
	}
	return info
}

func chunkTitles(chunks []Chunk) []string {
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, c.Title)
	}
	return out
}

type chunkShape struct {
	title     string
	level     int
	startLine int
	endLine   int
	isHeader  bool
}

func shapes(chunks []Chunk) []chunkShape {
	out := make([]chunkShape, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, chunkShape{c.Title, c.Level, c.StartLine, c.EndLine, c.IsHeader})
	}
	return out
}

// TestParseChunks_Preamble is the shape-level table for #70: which files get a
// leading preamble chunk, where it starts and ends, and that the header chunks
// that follow are untouched.
func TestParseChunks_Preamble(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []chunkShape
	}{
		{
			name: "prose above the first header becomes its own chunk",
			content: "Intro prose.\n" + // 1
				"\n" + // 2
				"## First\n" + // 3
				"body\n", // 4
			want: []chunkShape{
				{preambleTitle, 0, 1, 2, false},
				{"First", 2, 3, 5, true},
			},
		},
		{
			name: "frontmatter is skipped, the prose under it is not",
			content: "---\n" + // 1
				"title: Page\n" + // 2
				"---\n" + // 3
				"\n" + // 4
				"Intro prose.\n" + // 5
				"\n" + // 6
				"# Heading\n" + // 7
				"body\n", // 8
			want: []chunkShape{
				{preambleTitle, 0, 4, 6, false},
				{"Heading", 1, 7, 9, true},
			},
		},
		{
			name: "TOML frontmatter is skipped too",
			content: "+++\n" + // 1
				"title = \"Page\"\n" + // 2
				"+++\n" + // 3
				"Intro prose.\n" + // 4
				"## Heading\n" + // 5
				"body\n", // 6
			want: []chunkShape{
				{preambleTitle, 0, 4, 4, false},
				{"Heading", 2, 5, 7, true},
			},
		},
		{
			name: "frontmatter only: no preamble chunk",
			content: "---\n" + // 1
				"title: Page\n" + // 2
				"---\n" + // 3
				"# Heading\n" + // 4
				"body\n", // 5
			want: []chunkShape{
				{"Heading", 1, 4, 6, true},
			},
		},
		{
			name: "frontmatter then blank lines only: no preamble chunk",
			content: "---\n" + // 1
				"title: Page\n" + // 2
				"---\n" + // 3
				"\n" + // 4
				"   \n" + // 5
				"# Heading\n" + // 6
				"body\n", // 7
			want: []chunkShape{
				{"Heading", 1, 6, 8, true},
			},
		},
		{
			name: "blank lines only above the first header: no preamble chunk",
			content: "\n" + // 1
				"\t\n" + // 2
				"## Heading\n" + // 3
				"body\n", // 4
			want: []chunkShape{
				{"Heading", 2, 3, 5, true},
			},
		},
		{
			name: "file starting with a header is unchanged",
			content: "# Heading\n" + // 1
				"body\n" + // 2
				"\n" + // 3
				"## Second\n" + // 4
				"more\n", // 5
			want: []chunkShape{
				{"Heading", 1, 1, 3, true},
				{"Second", 2, 4, 6, true},
			},
		},
		{
			name: "unterminated frontmatter delimiter is treated as content",
			content: "---\n" + // 1
				"not really frontmatter\n" + // 2
				"# Heading\n" + // 3
				"body\n", // 4
			want: []chunkShape{
				{preambleTitle, 0, 1, 2, false},
				{"Heading", 1, 3, 5, true},
			},
		},
		{
			name: "a lone thematic break above the header is content",
			content: "---\n" + // 1
				"# Heading\n" + // 2
				"body\n", // 3
			want: []chunkShape{
				{preambleTitle, 0, 1, 1, false},
				{"Heading", 1, 2, 4, true},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shapes(ParseSections(tc.content, nil, DefaultReusablePatterns()))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("chunks =\n  %+v\nwant\n  %+v", got, tc.want)
			}
		})
	}
}

// TestParseChunks_PreambleCarriesItsOwnBlame checks that the preamble chunk
// gets exactly the blame lines of its span — not the frontmatter's above it,
// not the first section's below it — so its dates are its own.
func TestParseChunks_PreambleCarriesItsOwnBlame(t *testing.T) {
	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	newest := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	content := "---\n" + // 1 frontmatter, newest of all: must not be folded in
		"title: Page\n" + // 2
		"---\n" + // 3
		"\n" + // 4
		"Intro prose.\n" + // 5  preamble, old
		"\n" + // 6
		"## First\n" + // 7  section, recent
		"body\n" // 8

	lines := blameEveryLine(content, old, map[int]time.Time{
		1: newest, 2: newest, 3: newest,
		7: recent, 8: recent,
	})

	chunks := ParseSections(content, lines, DefaultReusablePatterns())
	if len(chunks) != 2 {
		t.Fatalf("chunks = %v, want preamble + one section", chunkTitles(chunks))
	}

	pre := chunks[0]
	if got := pre.LastUpdated(); got == nil || !got.Equal(old) {
		t.Errorf("preamble LastUpdated = %v, want %s (the frontmatter's date must not leak in)", got, old)
	}
	if got := pre.OldestLine(); got == nil || !got.Equal(old) {
		t.Errorf("preamble OldestLine = %v, want %s", got, old)
	}
	for _, li := range pre.Lines {
		if li.LineNumber < 4 || li.LineNumber > 6 {
			t.Errorf("preamble holds line %d, outside its span 4..6", li.LineNumber)
		}
	}
	if got := chunks[1].LastUpdated(); got == nil || !got.Equal(recent) {
		t.Errorf("section LastUpdated = %v, want %s", got, recent)
	}
}

// TestParseChunks_PreambleReusables proves the point of #70 at the parser
// level: a reference that appears only above the first header is detected.
func TestParseChunks_PreambleReusables(t *testing.T) {
	rp, err := NewReusablePatterns(
		[]string{`<Snippet\s+file="([^"]+)"\s*/?>`, `<([A-Z][A-Za-z0-9_$]*)\s*[^>]*/?>`},
		[]string{".mdx", ".md"}, "", "")
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	content := "---\n" +
		"title: Page\n" +
		"---\n" +
		"\n" +
		"import TrustCaveats from \"/snippets/trust.mdx\";\n" +
		"\n" +
		"<Snippet file=\"caveat.mdx\" />\n" +
		"<TrustCaveats />\n" +
		"\n" +
		"## First\n" +
		"<OnlyHere />\n"

	chunks := ParseSections(content, nil, rp)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %v, want preamble + one section", chunkTitles(chunks))
	}
	// "Snippet" is the component pattern's own view of the <Snippet …/> tag; it
	// is captured on every Mintlify page and classified skipped at resolution.
	// Listed here so the assertion stays exact rather than a containment check.
	want := []string{"caveat.mdx", "Snippet", "TrustCaveats"}
	if !reflect.DeepEqual(chunks[0].Reusables, want) {
		t.Errorf("preamble reusables = %v, want %v", chunks[0].Reusables, want)
	}
	// The section keeps only its own reference: the preamble's are not
	// duplicated into it.
	if !reflect.DeepEqual(chunks[1].Reusables, []string{"OnlyHere"}) {
		t.Errorf("section reusables = %v, want [OnlyHere]", chunks[1].Reusables)
	}
}

// TestParseChunks_PreambleParagraphLevel checks --paragraph-level behaves on
// the preamble the way it does inside a section: one chunk per paragraph,
// titled with the "(preamble) (L<n>)" line marker, and never marked IsHeader.
func TestParseChunks_PreambleParagraphLevel(t *testing.T) {
	content := "---\n" + // 1
		"title: Page\n" + // 2
		"---\n" + // 3
		"\n" + // 4
		"First preamble paragraph.\n" + // 5
		"\n" + // 6
		"Second preamble paragraph,\n" + // 7
		"continued.\n" + // 8
		"\n" + // 9
		"## Heading\n" + // 10
		"body\n" // 11

	lines := blameEveryLine(content, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), nil)
	chunks := ParseChunks(content, lines, true, DefaultReusablePatterns())

	want := []chunkShape{
		{preambleTitle + " (L5)", 0, 5, 5, false},
		{preambleTitle + " (L7)", 0, 7, 8, false},
		{"Heading", 2, 10, 11, true},
	}
	if got := shapes(chunks); !reflect.DeepEqual(got, want) {
		t.Errorf("chunks =\n  %+v\nwant\n  %+v", got, want)
	}
}

// TestParseChunks_HeaderlessUnchanged pins the behaviour #70 deliberately did
// not touch: a file with no header at all still goes through parseParagraphs
// over the whole file, frontmatter included, so a frontmatter-only stub keeps
// producing a chunk instead of vanishing from the report.
func TestParseChunks_HeaderlessUnchanged(t *testing.T) {
	t.Run("prose", func(t *testing.T) {
		content := "Just prose.\n\nMore prose.\n"
		lines := blameEveryLine(content, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), nil)
		want := []chunkShape{
			{noHeaderTitle + " (L1)", 0, 1, 1, false},
			{noHeaderTitle + " (L3)", 0, 3, 3, false},
		}
		if got := shapes(ParseChunks(content, lines, true, DefaultReusablePatterns())); !reflect.DeepEqual(got, want) {
			t.Errorf("chunks = %+v, want %+v", got, want)
		}
	})

	t.Run("frontmatter only", func(t *testing.T) {
		content := "---\ntitle: Stub\n---\n"
		lines := blameEveryLine(content, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), nil)
		chunks := ParseSections(content, lines, DefaultReusablePatterns())
		if len(chunks) != 1 {
			t.Fatalf("chunks = %v, want exactly one", chunkTitles(chunks))
		}
		if chunks[0].Title != noHeaderTitle+" (L1)" {
			t.Errorf("title = %q, want %q", chunks[0].Title, noHeaderTitle+" (L1)")
		}
		if len(chunks[0].Lines) == 0 {
			t.Error("the frontmatter-only stub lost its blame lines")
		}
	})
}

func TestFrontmatterLines(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{"none", "# Heading\n", 0},
		{"empty file", "", 0},
		{"yaml", "---\na: 1\n---\nbody\n", 3},
		{"toml", "+++\na = 1\n+++\nbody\n", 3},
		{"empty yaml block", "---\n---\nbody\n", 2},
		{"trailing spaces tolerated", "---  \na: 1\n---\t\nbody\n", 3},
		{"indented opener is not frontmatter", "  ---\na: 1\n---\n", 0},
		{"unterminated", "---\na: 1\nbody\n", 0},
		{"delimiters must match", "---\na = 1\n+++\n", 0},
		{"closing fence may be the last line", "---\na: 1\n---", 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := frontmatterLines(strings.Split(tc.content, "\n"))
			if got != tc.want {
				t.Errorf("frontmatterLines(%q) = %d, want %d", tc.content, got, tc.want)
			}
		})
	}
}

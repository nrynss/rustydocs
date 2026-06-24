package parser

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/git"
	"github.com/nrynss/rustydocs/internal/testutil"
)

// realDir returns the symlink-resolved repo root. On macOS t.TempDir() lives
// under /var/folders/... which is a symlink to /private/var/folders/...;
// "git rev-parse --show-toplevel" reports the resolved path, so we must build
// absolute paths from the resolved root for filepath.Rel (and thus git log)
// to line up.
func realDir(t *testing.T, repo *testutil.Repo) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(repo.Dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", repo.Dir, err)
	}
	return resolved
}

// defaultPatternStrings mirrors the hardcoded patterns used by
// DefaultReusablePatterns, so resolution tests can build a ReusablePatterns
// with custom roots while keeping the same matching behavior.
var defaultPatternStrings = []string{
	`\{\{[<%]\s*([a-zA-Z][\w/-]*)\s*[^%>]*[%>]\}\}`,
	`<([A-Z][a-zA-Z0-9]*)\s*[^>]*/?>`,
}

func mkLine(n int, ts time.Time, author string) git.LineInfo {
	return git.LineInfo{
		LineNumber: n,
		Author:     author,
		Timestamp:  ts,
		Content:    "line",
	}
}

func TestChunk_LastUpdated(t *testing.T) {
	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)

	c := &Chunk{Lines: []git.LineInfo{
		mkLine(1, old, "alice"),
		mkLine(2, recent, "bob"),
		mkLine(3, mid, "carol"),
	}}

	got := c.LastUpdated()
	if got == nil {
		t.Fatal("LastUpdated returned nil for non-empty chunk")
	}
	if !got.Equal(recent) {
		t.Errorf("LastUpdated = %v, want %v", got, recent)
	}

	empty := &Chunk{}
	if empty.LastUpdated() != nil {
		t.Error("LastUpdated on empty chunk should be nil")
	}
}

func TestChunk_OldestLine(t *testing.T) {
	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)

	c := &Chunk{Lines: []git.LineInfo{
		mkLine(1, mid, "alice"),
		mkLine(2, old, "bob"),
		mkLine(3, recent, "carol"),
	}}

	got := c.OldestLine()
	if got == nil {
		t.Fatal("OldestLine returned nil for non-empty chunk")
	}
	if !got.Equal(old) {
		t.Errorf("OldestLine = %v, want %v", got, old)
	}

	empty := &Chunk{}
	if empty.OldestLine() != nil {
		t.Error("OldestLine on empty chunk should be nil")
	}
}

func TestChunk_LastAuthor(t *testing.T) {
	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)

	c := &Chunk{Lines: []git.LineInfo{
		mkLine(1, old, "alice"),
		mkLine(2, recent, "bob"),
	}}

	if got := c.LastAuthor(); got != "bob" {
		t.Errorf("LastAuthor = %q, want %q", got, "bob")
	}

	empty := &Chunk{}
	if got := empty.LastAuthor(); got != "" {
		t.Errorf("LastAuthor on empty chunk = %q, want empty", got)
	}
}

func TestChunk_DisplayTitle(t *testing.T) {
	header := &Chunk{Title: "Installation", IsHeader: true}
	if got := header.DisplayTitle(); got != "Installation" {
		t.Errorf("DisplayTitle (header) = %q, want %q", got, "Installation")
	}

	para := &Chunk{Title: "Body (L5)", IsHeader: false}
	if got := para.DisplayTitle(); got != "Body (L5)" {
		t.Errorf("DisplayTitle (paragraph) = %q, want %q", got, "Body (L5)")
	}
}

func TestParseChunks_ParagraphLevel_MultipleParagraphs(t *testing.T) {
	content := "# Section\n\nFirst paragraph.\n\nSecond paragraph.\n"

	// Paragraph chunks with zero Lines are dropped by parseParagraphs, so feed
	// line info (one entry per source line) to keep both paragraph chunks.
	lines := []git.LineInfo{}
	for i := 1; i <= 5; i++ {
		lines = append(lines, mkLine(i, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "alice"))
	}
	chunks := ParseChunks(content, lines, true, DefaultReusablePatterns())

	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks (header + 2 paragraphs), got %d: %+v", len(chunks), titles(chunks))
	}

	// The first chunk should be marked as the header for the section.
	if !chunks[0].IsHeader {
		t.Errorf("first chunk should be a header, got %+v", chunks[0])
	}
	if chunks[0].Title != "Section" {
		t.Errorf("first chunk title = %q, want %q", chunks[0].Title, "Section")
	}
}

func TestParseChunks_ParagraphLevel_NoHeaders(t *testing.T) {
	content := "Just some text.\n\nMore text.\n"
	lines := []git.LineInfo{
		mkLine(1, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "alice"),
		mkLine(3, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "alice"),
	}
	chunks := ParseChunks(content, lines, true, DefaultReusablePatterns())

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk for headerless content")
	}
	// Paragraph chunks of a headerless file use the "(no header)" parent title.
	for _, c := range chunks {
		if c.IsHeader {
			t.Errorf("headerless content should produce no header chunks, got %+v", c)
		}
	}
}

func TestParseChunks_ParagraphLevel_NoTrailingBlankLine(t *testing.T) {
	// Last paragraph is not followed by a blank line; it must still be captured.
	content := "First.\n\nLast paragraph no newline"
	lines := []git.LineInfo{
		mkLine(1, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "alice"),
		mkLine(3, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "alice"),
	}
	chunks := ParseChunks(content, lines, true, DefaultReusablePatterns())

	if len(chunks) < 2 {
		t.Fatalf("expected the trailing paragraph to be captured, got %d chunks: %v", len(chunks), titles(chunks))
	}
	// The last chunk should cover the final line (line 3).
	last := chunks[len(chunks)-1]
	if last.EndLine != 3 {
		t.Errorf("last chunk EndLine = %d, want 3", last.EndLine)
	}
}

func TestParseChunks_ParagraphLevel_HeaderInsideParagraph(t *testing.T) {
	// A subheader inside a section's body should be detected as a header chunk
	// (IsHeader true) with its title taken from the header text.
	content := "# Top\n\nintro line\n\n## Nested Heading\n\nbody\n"
	lines := []git.LineInfo{}
	for i := 1; i <= 7; i++ {
		lines = append(lines, mkLine(i, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "alice"))
	}
	chunks := ParseChunks(content, lines, true, DefaultReusablePatterns())

	var found bool
	for _, c := range chunks {
		if c.Title == "Nested Heading" && c.IsHeader {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a header chunk titled %q, got chunks: %v", "Nested Heading", titles(chunks))
	}
}

func titles(chunks []Chunk) []string {
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, c.Title)
	}
	return out
}

func TestFindReusables_HugoAndMDX(t *testing.T) {
	content := "Intro {{< note >}} and a component <Callout type=\"info\" /> here.\n" +
		"Repeat {{< note >}} should be deduped.\n"
	got := FindReusables(content, DefaultReusablePatterns())

	want := map[string]bool{"note": false, "Callout": false}
	for _, r := range got {
		if _, ok := want[r]; ok {
			want[r] = true
		}
	}
	if !want["note"] {
		t.Errorf("expected reusable %q in %v", "note", got)
	}
	if !want["Callout"] {
		t.Errorf("expected reusable %q in %v", "Callout", got)
	}

	// Dedupe: "note" appears twice in content but only once in the result.
	count := 0
	for _, r := range got {
		if r == "note" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected %q deduped to a single entry, got %d", "note", count)
	}
}

func TestFindReusables_NilPatternsFallsBackToDefaults(t *testing.T) {
	got := FindReusables("{{< warn >}}", nil)
	if len(got) != 1 || got[0] != "warn" {
		t.Errorf("nil patterns should fall back to defaults; got %v", got)
	}
}

func TestNewReusablePatterns_InvalidRegex(t *testing.T) {
	_, err := NewReusablePatterns([]string{"("}, []string{".md"}, "", "")
	if err == nil {
		t.Fatal("expected an error for an invalid regex pattern")
	}
}

func TestDefaultReusablePatterns_Compiles(t *testing.T) {
	rp := DefaultReusablePatterns()
	if rp == nil {
		t.Fatal("DefaultReusablePatterns returned nil")
	}
	// Sanity: the defaults should match a known Hugo shortcode.
	if got := FindReusables("{{< x >}}", rp); len(got) != 1 || got[0] != "x" {
		t.Errorf("default patterns did not match shortcode; got %v", got)
	}
}

func TestNormalizeReusableName(t *testing.T) {
	cases := map[string]string{
		"reusables/foo": "foo",
		"/foo":          "foo",
		"  foo  ":       "foo",
		"foo":           "foo",
		"reusables/a/b": "a/b",
	}
	for in, want := range cases {
		if got := normalizeReusableName(in); got != want {
			t.Errorf("normalizeReusableName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGetReusableInfo_HugoShortcode commits a Hugo shortcode template and a
// data file it references via readFile, at two different dates, and asserts the
// resolved info reflects the most recent of the two.
func TestGetReusableInfo_HugoShortcode(t *testing.T) {
	repo := testutil.NewRepo(t)

	dataDate := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	tmplDate := time.Date(2024, 9, 15, 12, 0, 0, 0, time.UTC)

	// Commit the data dependency first (older).
	repo.Commit(dataDate, "add data", map[string]string{
		"data/x.txt": "some data\n",
	})
	// Then commit the shortcode template (newer) that traces the data file.
	repo.Commit(tmplDate, "add shortcode", map[string]string{
		"layouts/shortcodes/note.html": "{{ readFile \"data/x.txt\" }}\n",
	})

	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md", ".html"}, "", realDir(t, repo))
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	info := GetReusableInfo("note", rp)
	if info == nil {
		t.Fatal("GetReusableInfo(note) returned nil")
	}
	// Most recent of (template, traced data dep) is the template date.
	if !sameInstant(info.LastModified, tmplDate) {
		t.Errorf("LastModified = %v, want most recent %v", info.LastModified, tmplDate)
	}
}

// TestGetReusableInfo_HugoShortcode_DataDepNewer verifies that when the traced
// data dependency is newer than the template, its date wins.
func TestGetReusableInfo_HugoShortcode_DataDepNewer(t *testing.T) {
	repo := testutil.NewRepo(t)

	tmplDate := time.Date(2024, 1, 10, 12, 0, 0, 0, time.UTC)
	dataDate := time.Date(2024, 11, 20, 12, 0, 0, 0, time.UTC)

	repo.Commit(tmplDate, "add shortcode", map[string]string{
		"layouts/shortcodes/note.html": "{{ readFile \"data/x.txt\" }}\n",
	})
	repo.Commit(dataDate, "update data", map[string]string{
		"data/x.txt": "newer data\n",
	})

	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md", ".html"}, "", realDir(t, repo))
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	info := GetReusableInfo("note", rp)
	if info == nil {
		t.Fatal("GetReusableInfo(note) returned nil")
	}
	if !sameInstant(info.LastModified, dataDate) {
		t.Errorf("LastModified = %v, want most recent (data dep) %v", info.LastModified, dataDate)
	}
}

// TestGetReusableInfo_ReusablesDir covers the legacy reusablesDir path,
// including buildDirCache/lookupInDir.
func TestGetReusableInfo_ReusablesDir(t *testing.T) {
	repo := testutil.NewRepo(t)

	fooDate := time.Date(2024, 5, 5, 9, 0, 0, 0, time.UTC)
	repo.Commit(fooDate, "add shared foo", map[string]string{
		"shared/foo.md": "# Foo\nshared content\n",
	})

	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md"}, filepath.Join(realDir(t, repo), "shared"), "")
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	info := GetReusableInfo("foo", rp)
	if info == nil {
		t.Fatal("GetReusableInfo(foo) returned nil")
	}
	if !sameInstant(info.LastModified, fooDate) {
		t.Errorf("LastModified = %v, want %v", info.LastModified, fooDate)
	}
}

// TestGetReusableInfo_ReusablesDir_IndexConvention covers the index-file
// convention (shared/bar/index.md resolvable as "bar"), exercising the
// buildDirCache index branch and lookupInDir's subdirectory fallback.
func TestGetReusableInfo_ReusablesDir_IndexConvention(t *testing.T) {
	repo := testutil.NewRepo(t)

	barDate := time.Date(2024, 7, 7, 9, 0, 0, 0, time.UTC)
	repo.Commit(barDate, "add shared bar index", map[string]string{
		"shared/bar/index.md": "# Bar\nindexed content\n",
	})

	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md"}, filepath.Join(realDir(t, repo), "shared"), "")
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	info := GetReusableInfo("bar", rp)
	if info == nil {
		t.Fatal("GetReusableInfo(bar) returned nil")
	}
	if !sameInstant(info.LastModified, barDate) {
		t.Errorf("LastModified = %v, want %v", info.LastModified, barDate)
	}
}

func TestGetReusableInfo_NilPatterns(t *testing.T) {
	if got := GetReusableInfo("anything", nil); got != nil {
		t.Errorf("GetReusableInfo with nil rp = %v, want nil", got)
	}
}

func TestGetReusableInfo_Unresolvable(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "init", map[string]string{
		"data/x.txt": "x\n",
	})
	root := realDir(t, repo)
	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md", ".html"}, filepath.Join(root, "shared"), root)
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}
	if got := GetReusableInfo("does-not-exist", rp); got != nil {
		t.Errorf("GetReusableInfo for unknown reusable = %v, want nil", got)
	}
}

// TestCalculateSectionStaleness_MostRecent asserts the staleness date is the
// most recent of (section line dates, reusable dates).
func TestCalculateSectionStaleness_MostRecent(t *testing.T) {
	repo := testutil.NewRepo(t)

	reusableDate := time.Date(2024, 12, 1, 12, 0, 0, 0, time.UTC)
	repo.Commit(reusableDate, "add shared", map[string]string{
		"shared/widget.md": "# Widget\n",
	})

	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md"}, filepath.Join(realDir(t, repo), "shared"), "")
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	// Section's own lines are older than the reusable.
	lineDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	section := &Chunk{
		Title:     "Uses widget",
		Lines:     []git.LineInfo{mkLine(1, lineDate, "alice")},
		Reusables: []string{"widget"},
	}

	got := CalculateSectionStaleness(section, rp)
	if got == nil {
		t.Fatal("CalculateSectionStaleness returned nil")
	}
	// The reusable is newer, so it should drive the staleness date.
	if !sameInstant(*got, reusableDate) {
		t.Errorf("staleness = %v, want most recent (reusable) %v", got, reusableDate)
	}
}

// TestCalculateSectionStaleness_LinesDrive verifies the section's own lines win
// when they are newer than the reusable.
func TestCalculateSectionStaleness_LinesDrive(t *testing.T) {
	repo := testutil.NewRepo(t)

	reusableDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	repo.Commit(reusableDate, "add shared", map[string]string{
		"shared/widget.md": "# Widget\n",
	})

	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md"}, filepath.Join(realDir(t, repo), "shared"), "")
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	lineDate := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	section := &Chunk{
		Lines:     []git.LineInfo{mkLine(1, lineDate, "alice")},
		Reusables: []string{"widget"},
	}

	got := CalculateSectionStaleness(section, rp)
	if got == nil {
		t.Fatal("CalculateSectionStaleness returned nil")
	}
	if !got.Equal(lineDate) {
		t.Errorf("staleness = %v, want most recent (line) %v", got, lineDate)
	}
}

// TestCalculateSectionStaleness_Nil verifies nil is returned when a chunk has no
// lines and no resolvable reusables.
func TestCalculateSectionStaleness_Nil(t *testing.T) {
	section := &Chunk{
		Reusables: []string{"unresolvable"},
	}
	// rp with empty roots resolves nothing.
	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md"}, "", "")
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}
	if got := CalculateSectionStaleness(section, rp); got != nil {
		t.Errorf("staleness with no lines and no resolvable reusables = %v, want nil", got)
	}
}

// sameInstant compares two times as instants. git log reports timestamps at
// second precision and may carry a non-UTC zone, so we compare via Unix().
func sameInstant(a, b time.Time) bool {
	return a.Unix() == b.Unix()
}

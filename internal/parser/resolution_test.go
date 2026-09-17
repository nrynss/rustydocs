package parser

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/git"
	"github.com/nrynss/rustydocs/internal/testutil"
)

// defaultPatternStrings reuses the hugo profile's patterns — the same source
// DefaultReusablePatterns reads — so resolution tests build a ReusablePatterns
// with custom roots while keeping identical matching behavior.
var defaultPatternStrings = hugoProfile().ReusablePatterns

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
	// DisplayTitle is a pass-through: the line marker (e.g. "(L5)") is already
	// baked into Title by createParagraphChunk, so the title is returned verbatim
	// regardless of IsHeader.
	for _, tt := range []struct {
		name  string
		chunk Chunk
	}{
		{"header", Chunk{Title: "Installation", IsHeader: true}},
		{"paragraph", Chunk{Title: "Body (L5)", IsHeader: false}},
	} {
		if got := tt.chunk.DisplayTitle(); got != tt.chunk.Title {
			t.Errorf("DisplayTitle (%s) = %q, want %q", tt.name, got, tt.chunk.Title)
		}
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
		"data/x.txt": testutil.ReadFixture(t, "hugo-site/data/x.txt"),
	})
	// Then commit the shortcode template (newer) that traces the data file.
	repo.Commit(tmplDate, "add shortcode", map[string]string{
		"layouts/shortcodes/note.html": testutil.ReadFixture(t, "hugo-site/layouts/shortcodes/note.html"),
	})

	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md", ".html"}, "", repo.Dir)
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	info := GetReusableInfo("note", "", rp)
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
		"layouts/shortcodes/note.html": testutil.ReadFixture(t, "hugo-site/layouts/shortcodes/note.html"),
	})
	repo.Commit(dataDate, "update data", map[string]string{
		"data/x.txt": testutil.ReadFixture(t, "hugo-site/data/x.txt"),
	})

	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md", ".html"}, "", repo.Dir)
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	info := GetReusableInfo("note", "", rp)
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
		"shared/foo.md": testutil.ReadFixture(t, "hugo-site/shared/foo.md"),
	})

	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md"}, filepath.Join(repo.Dir, "shared"), "")
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	info := GetReusableInfo("foo", "", rp)
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
		"shared/bar/index.md": testutil.ReadFixture(t, "hugo-site/shared/bar/index.md"),
	})

	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md"}, filepath.Join(repo.Dir, "shared"), "")
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	info := GetReusableInfo("bar", "", rp)
	if info == nil {
		t.Fatal("GetReusableInfo(bar) returned nil")
	}
	if !sameInstant(info.LastModified, barDate) {
		t.Errorf("LastModified = %v, want %v", info.LastModified, barDate)
	}
}

func TestGetReusableInfo_NilPatterns(t *testing.T) {
	if got := GetReusableInfo("anything", "", nil); got != nil {
		t.Errorf("GetReusableInfo with nil rp = %v, want nil", got)
	}
}

func TestGetReusableInfo_Unresolvable(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "init", map[string]string{
		"data/x.txt": "x\n",
	})
	root := repo.Dir
	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md", ".html"}, filepath.Join(root, "shared"), root)
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}
	if got := GetReusableInfo("does-not-exist", "", rp); got != nil {
		t.Errorf("GetReusableInfo for unknown reusable = %v, want nil", got)
	}
}

// TestCalculateSectionStaleness_MostRecent asserts the staleness date is the
// most recent of (section line dates, reusable dates).
func TestCalculateSectionStaleness_MostRecent(t *testing.T) {
	repo := testutil.NewRepo(t)

	reusableDate := time.Date(2024, 12, 1, 12, 0, 0, 0, time.UTC)
	repo.Commit(reusableDate, "add shared", map[string]string{
		"shared/widget.md": testutil.ReadFixture(t, "hugo-site/shared/widget.md"),
	})

	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md"}, filepath.Join(repo.Dir, "shared"), "")
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

	got := CalculateSectionStaleness(section, "", rp)
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
		"shared/widget.md": testutil.ReadFixture(t, "hugo-site/shared/widget.md"),
	})

	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md"}, filepath.Join(repo.Dir, "shared"), "")
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	lineDate := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	section := &Chunk{
		Lines:     []git.LineInfo{mkLine(1, lineDate, "alice")},
		Reusables: []string{"widget"},
	}

	got := CalculateSectionStaleness(section, "", rp)
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
	if got := CalculateSectionStaleness(section, "", rp); got != nil {
		t.Errorf("staleness with no lines and no resolvable reusables = %v, want nil", got)
	}
}

// sameInstant compares two times as instants. git log reports timestamps at
// second precision and may carry a non-UTC zone, so we compare via Unix().
func sameInstant(a, b time.Time) bool {
	return a.Unix() == b.Unix()
}

// TestGetReusableInfo_ThemeShortcode covers a theme-provided shortcode: the
// site's only layouts live in themes/<theme>/layouts, which is exactly the
// shape the themes/ root marker detects. Before the theme search existed the
// shortcode was detected but never resolved, and the section was reported as
// unknown.
func TestGetReusableInfo_ThemeShortcode(t *testing.T) {
	repo := testutil.NewRepo(t)

	tmplDate := time.Date(2024, 6, 12, 12, 0, 0, 0, time.UTC)
	repo.Commit(tmplDate, "add theme shortcode", map[string]string{
		"config/_default/hugo.toml":             "baseURL = 'x'\n",
		"themes/t/layouts/shortcodes/note.html": "<aside class=\"note\">theme note</aside>\n",
		"content/docs/a.md":                     "# A\n\n{{< note >}}\n",
	})

	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md", ".html"}, "", repo.Dir)
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	info := GetReusableInfo("note", "", rp)
	if info == nil {
		t.Fatal("GetReusableInfo(note) returned nil; theme shortcode was not resolved")
	}
	if !sameInstant(info.LastModified, tmplDate) {
		t.Errorf("LastModified = %v, want %v", info.LastModified, tmplDate)
	}
	want := filepath.Join(repo.Dir, "themes", "t", "layouts", "shortcodes", "note.html")
	if got := rp.shortcodeCache["note"]; len(got) == 0 || got[0] != want {
		t.Errorf("shortcodeCache[note] = %v, want first entry %q", got, want)
	}
	// A second lookup must come from the cache and agree.
	if again := GetReusableInfo("note", "", rp); again == nil || !sameInstant(again.LastModified, tmplDate) {
		t.Errorf("cached lookup = %v, want %v", again, tmplDate)
	}
}

// TestGetReusableInfo_ProjectShortcodeBeatsTheme pins Hugo's lookup order: a
// project layouts/shortcodes template overrides a theme's template of the same
// name, so the project file's (older) date is the one reported.
func TestGetReusableInfo_ProjectShortcodeBeatsTheme(t *testing.T) {
	repo := testutil.NewRepo(t)

	projectDate := time.Date(2023, 2, 3, 12, 0, 0, 0, time.UTC)
	themeDate := time.Date(2025, 1, 20, 12, 0, 0, 0, time.UTC)

	repo.Commit(projectDate, "add project shortcode", map[string]string{
		"layouts/shortcodes/note.html": "<aside>project note</aside>\n",
	})
	repo.Commit(themeDate, "add theme shortcode", map[string]string{
		"themes/t/layouts/shortcodes/note.html": "<aside>theme note</aside>\n",
	})

	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md", ".html"}, "", repo.Dir)
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	info := GetReusableInfo("note", "", rp)
	if info == nil {
		t.Fatal("GetReusableInfo(note) returned nil")
	}
	if !sameInstant(info.LastModified, projectDate) {
		t.Errorf("LastModified = %v, want the project template's %v (project layouts win)",
			info.LastModified, projectDate)
	}
}

// TestLayoutRoots covers the themes/ scan itself: no themes dir, a themes dir
// with a stray regular file, and the ordering of project vs theme layouts.
func TestLayoutRoots(t *testing.T) {
	root := t.TempDir()
	rp, err := NewReusablePatterns(nil, nil, "", root)
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	projectLayouts := filepath.Join(root, "layouts")
	if got := rp.layoutRoots(); len(got) != 1 || got[0] != projectLayouts {
		t.Fatalf("layoutRoots() without themes/ = %v, want [%q]", got, projectLayouts)
	}

	if err := os.MkdirAll(filepath.Join(root, "themes", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "themes", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "themes", "README"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := []string{
		projectLayouts,
		filepath.Join(root, "themes", "a", "layouts"),
		filepath.Join(root, "themes", "b", "layouts"),
	}
	if got := rp.layoutRoots(); !reflect.DeepEqual(got, want) {
		t.Errorf("layoutRoots() = %v, want %v", got, want)
	}

	// A theme symlinked into themes/ (the standard Hugo local theme-development
	// workflow) is a directory only after following the link: os.ReadDir reports
	// the symlink's own type, so filtering on DirEntry.IsDir alone would skip it.
	realTheme := filepath.Join(root, "vendor", "linked")
	if err := os.MkdirAll(filepath.Join(realTheme, "layouts", "shortcodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realTheme, filepath.Join(root, "themes", "c")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	// A dangling symlink must be skipped rather than reported as a layouts root.
	if err := os.Symlink(filepath.Join(root, "does-not-exist"), filepath.Join(root, "themes", "d")); err != nil {
		t.Fatal(err)
	}
	// A symlink to a regular file is not a theme either.
	if err := os.Symlink(filepath.Join(root, "themes", "README"), filepath.Join(root, "themes", "e")); err != nil {
		t.Fatal(err)
	}
	want = append(want, filepath.Join(root, "themes", "c", "layouts"))
	if got := rp.layoutRoots(); !reflect.DeepEqual(got, want) {
		t.Errorf("layoutRoots() with symlinked themes = %v, want %v", got, want)
	}
}

// TestGetReusableInfo_SymlinkedThemeShortcode is the end-to-end form of the
// symlink case: the site's only alert.html lives in a theme that is symlinked
// into themes/, which is how Hugo themes are developed locally. Before
// layoutRoots followed symlinks the shortcode was detected but never resolved,
// and its section was reported as unknown.
func TestGetReusableInfo_SymlinkedThemeShortcode(t *testing.T) {
	repo := testutil.NewRepo(t)

	tmplDate := time.Date(2024, 3, 8, 12, 0, 0, 0, time.UTC)
	repo.Commit(tmplDate, "add vendored theme", map[string]string{
		"hugo.toml": "baseURL = 'x'\n",
		"vendor/mytheme/layouts/shortcodes/alert.html": "<aside>alert</aside>\n",
		"content/docs/a.md":                            "# A\n\n{{< alert >}}\n",
	})
	if err := os.MkdirAll(repo.Path("themes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "vendor", "mytheme"), repo.Path("themes/mytheme")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md", ".html"}, "", repo.Dir)
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}

	info := GetReusableInfo("alert", "", rp)
	if info == nil {
		t.Fatal("GetReusableInfo(alert) returned nil; symlinked theme shortcode was not resolved")
	}
	if !sameInstant(info.LastModified, tmplDate) {
		t.Errorf("LastModified = %v, want %v", info.LastModified, tmplDate)
	}
	want := filepath.Join(repo.Dir, "themes", "mytheme", "layouts", "shortcodes", "alert.html")
	if got := rp.shortcodeCache["alert"]; len(got) == 0 || got[0] != want {
		t.Errorf("shortcodeCache[alert] = %v, want first entry %q", got, want)
	}
}

// mintlifyPatternStrings reuses the mintlify profile's pattern, so the path
// resolver's tests match on exactly what the profile ships.
var mintlifyPatternStrings = func() []string {
	p, ok := config.LookupProfile(config.ProfileMintlify)
	if !ok {
		panic("mintlify profile missing from config registry")
	}
	return p.ReusablePatterns
}()

// newPathRP builds a ReusablePatterns wired exactly as the Mintlify profile is:
// snippet and component patterns, .mdx/.md reusable extensions, the given
// project root, the path resolver and the import map (#68).
func newPathRP(t *testing.T, root string) *ReusablePatterns {
	t.Helper()
	rp, err := NewReusablePatternsFor(ReusableConfig{
		Patterns:   mintlifyPatternStrings,
		Extensions: []string{".mdx", ".md"},
		Root:       root,
		Resolver:   config.ResolverPath,
		ImportMap:  true,
	})
	if err != nil {
		t.Fatalf("NewReusablePatternsFor: %v", err)
	}
	return rp
}

// TestFindReusables_MintlifyPattern checks the parser side of the Mintlify
// patterns: a <Snippet file="…"> capture is the snippet *path*, a Hugo
// shortcode is not picked up at all, and a capitalised tag is captured as a
// symbol for the import map — which is a different kind of capture, resolved
// (and here, skipped) separately. See TestResolveReusable_ImportMap for the
// half of the contract that keeps <Card /> out of the report (#7, #68).
func TestFindReusables_MintlifyPattern(t *testing.T) {
	rp := newPathRP(t, t.TempDir())
	content := "# Title\n\n" +
		`<Snippet file="/snippets/foo.mdx" />` + "\n" +
		"<Card title=\"nope\" />\n" +
		"{{< alert >}}\n" +
		`<Snippet file="./local.mdx" />` + "\n"

	got := FindReusables(content, rp)
	// Paths first (the snippet patterns run first), then component symbols.
	// "Snippet" is the tag of the include itself; it names no import, so it is
	// skipped at resolution just as "Card" is.
	want := []string{"/snippets/foo.mdx", "./local.mdx", "Snippet", "Card"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FindReusables = %v, want %v", got, want)
	}
	for _, sym := range []string{"Snippet", "Card"} {
		if _, res := ResolveReusable(sym, "", rp); res != ResolutionSkipped {
			t.Errorf("ResolveReusable(%q) = %v, want ResolutionSkipped", sym, res)
		}
	}
}

// TestGetReusableInfo_PathResolver covers the direct-path resolver end to end
// against a real repository: root-absolute and page-relative captures resolve,
// a traversal out of the root does not, a missing snippet yields no info, and
// an extensionless capture falls back to the reusable extensions (#7).
func TestGetReusableInfo_PathResolver(t *testing.T) {
	repo := testutil.NewRepo(t)

	snippetDate := time.Date(2024, 5, 20, 12, 0, 0, 0, time.UTC)
	repo.Commit(snippetDate, "add snippets", map[string]string{
		"docs.json":               `{"name":"docs"}`,
		"snippets/foo.mdx":        "shared snippet\n",
		"snippets/bare.md":        "extensionless target\n",
		"snippets/dir/index.mdx":  "index snippet\n",
		"docs/guides/partial.mdx": "page-local partial\n",
		"docs/guides/page.mdx":    "# Page\n",
		"top-level.mdx":           "inside the root, two levels up from the page\n",
	})

	// A sibling tree the traversal cases try to reach: committed in another
	// repo entirely, so an escape that succeeded would come back as real git
	// info rather than as "file not found".
	outside := testutil.NewRepo(t)
	outside.Commit(snippetDate, "outside", map[string]string{"secret.mdx": "not yours\n"})

	root := repo.Dir
	page := repo.Path("docs/guides/page.mdx")
	rp := newPathRP(t, root)

	// A relative capture that climbs out of the docs project and lands on a
	// real, committed file elsewhere on the machine.
	escape, err := filepath.Rel(filepath.Dir(page), outside.Path("secret.mdx"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		ref     string
		source  string
		wantNil bool
	}{
		{name: "root-absolute path", ref: "/snippets/foo.mdx", source: page},
		{name: "root-absolute path with no source file", ref: "/snippets/foo.mdx"},
		{name: "relative to the referencing page", ref: "./partial.mdx", source: page},
		{name: "bare relative to the referencing page", ref: "partial.mdx", source: page},
		{name: "relative falls back to the root", ref: "snippets/foo.mdx", source: page},
		{name: "extensionless tries the reusable extensions", ref: "/snippets/bare"},
		{name: "extensionless directory index", ref: "/snippets/dir"},
		{name: "traversal that stays inside the root", ref: "../../top-level.mdx", source: page},
		{name: "missing snippet", ref: "/snippets/nope.mdx", source: page, wantNil: true},
		{name: "traversal out of the root", ref: escape, source: page, wantNil: true},
		{name: "root-absolute traversal out of the root", ref: "/../top-level.mdx", wantNil: true},
		{name: "absolute path into another tree", ref: outside.Path("secret.mdx"), source: page, wantNil: true},
		{name: "empty reference", ref: "", source: page, wantNil: true},
		{name: "blank reference", ref: "   ", source: page, wantNil: true},
		{name: "root itself", ref: "/", source: page, wantNil: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := GetReusableInfo(tt.ref, tt.source, rp)
			if tt.wantNil {
				if info != nil {
					t.Fatalf("GetReusableInfo(%q) = %+v, want nil", tt.ref, info)
				}
				return
			}
			if info == nil {
				t.Fatalf("GetReusableInfo(%q) returned nil", tt.ref)
			}
			if !sameInstant(info.LastModified, snippetDate) {
				t.Errorf("LastModified = %v, want %v", info.LastModified, snippetDate)
			}
		})
	}
}

// TestGetReusableInfo_PathResolver_BasePrecedence pins the lookup order of the
// direct-path resolver, which is what decides whether a real Mintlify project
// resolves at all: a bare capture is a *snippets-directory* reference first
// (snippets/, then _snippets/), then the project root, and only then the
// referencing page's directory; an explicitly page-relative "./" capture keeps
// the page's directory first; and a root-absolute "/" capture is the root and
// nothing else (#7).
//
// Every candidate file carries a different commit date, so the date that comes
// back names the base that won.
func TestGetReusableInfo_PathResolver_BasePrecedence(t *testing.T) {
	var (
		snippetsDate   = time.Date(2021, 1, 4, 12, 0, 0, 0, time.UTC)
		underscoreDate = time.Date(2022, 2, 5, 12, 0, 0, 0, time.UTC)
		rootDate       = time.Date(2023, 3, 6, 12, 0, 0, 0, time.UTC)
		pageDate       = time.Date(2024, 4, 7, 12, 0, 0, 0, time.UTC)
	)

	repo := testutil.NewRepo(t)
	// "both.mdx" exists in snippets/ and next to the page; "underscore.mdx"
	// only in _snippets/; "rooted.mdx" only at the root; "local.mdx" only
	// next to the page.
	repo.Commit(snippetsDate, "snippets", map[string]string{
		"docs.json":         `{"name":"docs"}`,
		"snippets/both.mdx": "snippets copy\n",
		"docs/page.mdx":     "# Page\n",
	})
	repo.Commit(underscoreDate, "underscore snippets", map[string]string{
		"_snippets/underscore.mdx": "underscore copy\n",
	})
	repo.Commit(rootDate, "root copy", map[string]string{
		"rooted.mdx": "root copy\n",
	})
	repo.Commit(pageDate, "page-local copies", map[string]string{
		"docs/both.mdx":  "page-local copy\n",
		"docs/local.mdx": "page-local only\n",
	})

	page := repo.Path("docs/page.mdx")
	rp := newPathRP(t, repo.Dir)

	tests := []struct {
		name string
		ref  string
		want time.Time
	}{
		{
			name: "a bare name resolves from snippets/",
			ref:  "both.mdx",
			want: snippetsDate,
		},
		{
			name: "a bare name falls through to _snippets/ when snippets/ has no match",
			ref:  "underscore.mdx",
			want: underscoreDate,
		},
		{
			name: "a bare name falls back to the project root",
			ref:  "rooted.mdx",
			want: rootDate,
		},
		{
			name: "a bare name found nowhere else falls back to the page directory",
			ref:  "local.mdx",
			want: pageDate,
		},
		{
			name: "an explicit ./ capture is page-relative",
			ref:  "./both.mdx",
			want: pageDate,
		},
		{
			name: "a root-absolute capture is resolved against the root",
			ref:  "/snippets/both.mdx",
			want: snippetsDate,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := GetReusableInfo(tt.ref, page, rp)
			if info == nil {
				t.Fatalf("GetReusableInfo(%q) returned nil", tt.ref)
			}
			if !sameInstant(info.LastModified, tt.want) {
				t.Errorf("GetReusableInfo(%q) resolved to %v, want the copy committed %v",
					tt.ref, info.LastModified, tt.want)
			}
		})
	}

	// The reported identity follows resolution, so the bare capture that won
	// from snippets/ is named by that path and not by the page-local file of
	// the same name.
	if got := rp.DisplayName("both.mdx", page, GetReusableInfo("both.mdx", page, rp)); got != "snippets/both.mdx" {
		t.Errorf("DisplayName(bare capture) = %q, want snippets/both.mdx", got)
	}

	// With no source file the page directory simply is not a base; the shared
	// ones still apply.
	if info := GetReusableInfo("both.mdx", "", rp); info == nil || !sameInstant(info.LastModified, snippetsDate) {
		t.Errorf("GetReusableInfo(no source) = %+v, want the snippets/ copy", info)
	}
	if info := GetReusableInfo("local.mdx", "", rp); info != nil {
		t.Errorf("GetReusableInfo(page-only snippet, no source) = %+v, want nil", info)
	}
}

// TestGetReusableInfo_PathResolver_NoRoot verifies that without a detected
// project root nothing is resolved: there is no base to resolve against and no
// bound to keep the result inside the docs project.
func TestGetReusableInfo_PathResolver_NoRoot(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(time.Date(2024, 5, 20, 12, 0, 0, 0, time.UTC), "add", map[string]string{
		"snippets/foo.mdx": "x\n",
		"docs/page.mdx":    "# Page\n",
	})
	rp := newPathRP(t, "")
	if got := GetReusableInfo("/snippets/foo.mdx", repo.Path("docs/page.mdx"), rp); got != nil {
		t.Errorf("GetReusableInfo with no root = %+v, want nil", got)
	}
}

// TestGetReusableInfo_PathResolver_UntrackedSnippet checks that an existing but
// uncommitted snippet resolves to no info — reported unknown, never fresh (#55).
func TestGetReusableInfo_PathResolver_UntrackedSnippet(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(time.Date(2024, 5, 20, 12, 0, 0, 0, time.UTC), "init", map[string]string{
		"docs.json":     "{}",
		"docs/page.mdx": "# Page\n",
	})
	repo.Write("snippets/untracked.mdx", "not committed\n")

	rp := newPathRP(t, repo.Dir)
	if got := GetReusableInfo("/snippets/untracked.mdx", repo.Path("docs/page.mdx"), rp); got != nil {
		t.Errorf("GetReusableInfo(untracked) = %+v, want nil", got)
	}
}

// TestCalculateSectionStaleness_PathReusable folds a path-resolved snippet's
// date into the section that references it: the page is old, the snippet is
// new, so the section's effective date is the snippet's.
func TestCalculateSectionStaleness_PathReusable(t *testing.T) {
	repo := testutil.NewRepo(t)
	pageDate := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	snippetDate := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	repo.Commit(pageDate, "page", map[string]string{
		"docs.json":     "{}",
		"docs/page.mdx": "# Page\n",
	})
	repo.Commit(snippetDate, "snippet", map[string]string{
		"snippets/foo.mdx": "fresh\n",
	})

	rp := newPathRP(t, repo.Dir)
	section := &Chunk{
		Title:     "Page",
		Lines:     []git.LineInfo{mkLine(1, pageDate, "alice")},
		Reusables: []string{"/snippets/foo.mdx"},
	}
	got := CalculateSectionStaleness(section, repo.Path("docs/page.mdx"), rp)
	if got == nil {
		t.Fatal("CalculateSectionStaleness returned nil")
	}
	if !sameInstant(*got, snippetDate) {
		t.Errorf("effective date = %v, want the snippet's %v", got, snippetDate)
	}
}

// TestNewReusablePatterns_LegacyResolverInference pins the compatibility
// shim: the four-argument constructor still infers ResolverHugo from a
// non-empty root and ResolverNone from an empty one, so pre-profile callers
// behave exactly as before.
func TestNewReusablePatterns_LegacyResolverInference(t *testing.T) {
	withRoot, err := NewReusablePatterns(defaultPatternStrings, []string{".md"}, "", "/site")
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}
	if withRoot.resolver != config.ResolverHugo || withRoot.root != "/site" {
		t.Errorf("resolver/root = %q/%q, want hugo//site", withRoot.resolver, withRoot.root)
	}
	noRoot, err := NewReusablePatterns(defaultPatternStrings, []string{".md"}, "/shared", "")
	if err != nil {
		t.Fatalf("NewReusablePatterns: %v", err)
	}
	if noRoot.resolver != config.ResolverNone || noRoot.reusablesDir != "/shared" {
		t.Errorf("resolver/dir = %q/%q, want none//shared", noRoot.resolver, noRoot.reusablesDir)
	}
	if _, err := NewReusablePatternsFor(ReusableConfig{Patterns: []string{"("}}); err == nil {
		t.Error("NewReusablePatternsFor with an invalid pattern should error")
	}
}

// TestGetReusableInfo_PathResolver_SymlinkEscape is the regression test for
// purely lexical containment (#7 review). "snippets/out" is a symlink pointing
// at a whole other repository; without resolving symlinks before the
// containment check, <Snippet file="/snippets/out/passwd.mdx" /> folded that
// repository's fresh commit date into the report and marked a genuinely stale
// page fresh. The symlink must be ignored, while a symlink that stays inside
// the project root must still resolve.
func TestGetReusableInfo_PathResolver_SymlinkEscape(t *testing.T) {
	repo := testutil.NewRepo(t)
	oldDate := time.Date(2020, 1, 2, 12, 0, 0, 0, time.UTC)
	repo.Commit(oldDate, "docs", map[string]string{
		"docs.json":          "{}",
		"docs/page.mdx":      "# Page\n",
		"real/inside.mdx":    "a snippet that really lives in the project\n",
		"snippets/keepme.md": "another in-project snippet\n",
	})

	// A separate repository with a much newer commit: if an escape resolved,
	// its date would come back and look fresh.
	outside := testutil.NewRepo(t)
	freshDate := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	outside.Commit(freshDate, "secret", map[string]string{"passwd.mdx": "not yours\n"})

	// snippets/out -> <other repo>   (escapes the root)
	if err := os.Symlink(outside.Dir, repo.Path("snippets/out")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// snippets/link.mdx -> ../real/inside.mdx   (stays inside the root)
	if err := os.Symlink(filepath.Join("..", "real", "inside.mdx"), repo.Path("snippets/link.mdx")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// snippets/dangling.mdx -> nowhere
	if err := os.Symlink(repo.Path("real/gone.mdx"), repo.Path("snippets/dangling.mdx")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	rp := newPathRP(t, repo.Dir)
	page := repo.Path("docs/page.mdx")

	if got := GetReusableInfo("/snippets/out/passwd.mdx", page, rp); got != nil {
		t.Errorf("a snippet reached through a symlink out of the root resolved to %+v, want nil", got)
	}
	// The same file addressed relatively through the same symlink.
	if got := GetReusableInfo("../snippets/out/passwd.mdx", page, rp); got != nil {
		t.Errorf("relative escape through a symlink resolved to %+v, want nil", got)
	}
	// A symlink whose target is inside the root still resolves normally.
	inside := GetReusableInfo("/snippets/link.mdx", page, rp)
	if inside == nil {
		t.Fatal("a symlink pointing inside the root should still resolve")
	}
	if !sameInstant(inside.LastModified, oldDate) {
		t.Errorf("in-root symlink LastModified = %v, want %v", inside.LastModified, oldDate)
	}
	// A dangling symlink is simply not a match, not an error.
	if got := GetReusableInfo("/snippets/dangling.mdx", page, rp); got != nil {
		t.Errorf("dangling symlink resolved to %+v, want nil", got)
	}
	// And the plain ".." rejection still holds.
	escape, err := filepath.Rel(filepath.Dir(page), outside.Path("passwd.mdx"))
	if err != nil {
		t.Fatal(err)
	}
	if got := GetReusableInfo(escape, page, rp); got != nil {
		t.Errorf("\"..\" escape resolved to %+v, want nil", got)
	}
}

// TestWithinRoot_SymlinkedRoot pins the other half of the symlink fix: the root
// itself is resolved before the comparison. On macOS a t.TempDir() lives under
// /var, which is a symlink to /private/var, so comparing a resolved candidate
// against an unresolved root would reject every legitimate snippet.
func TestWithinRoot_SymlinkedRoot(t *testing.T) {
	real := t.TempDir()
	if err := os.MkdirAll(filepath.Join(real, "snippets"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(real, "snippets", "foo.mdx")
	if err := os.WriteFile(target, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Root given through a symlink, candidate given by its real path.
	rp := newPathRP(t, link)
	if !rp.withinRoot(target) {
		t.Error("withinRoot(real path, symlinked root) = false, want true")
	}
	// And vice versa.
	rp2 := newPathRP(t, real)
	if !rp2.withinRoot(filepath.Join(link, "snippets", "foo.mdx")) {
		t.Error("withinRoot(symlinked path, real root) = false, want true")
	}
	// A path that does not exist cannot be resolved, and is not a match.
	if rp.withinRoot(filepath.Join(real, "snippets", "missing.mdx")) {
		t.Error("withinRoot(missing candidate) = true, want false")
	}
	// An empty root rejects everything.
	if newPathRP(t, "").withinRoot(target) {
		t.Error("withinRoot with no root = true, want false")
	}
}

// TestDisplayName covers the reported identity of a resolved reusable: under
// the path resolver it is the resolved file's path relative to the project
// root, and everything else keeps the raw capture (#7 review).
func TestDisplayName(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "snippets"), 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "snippets", "foo.mdx")
	if err := os.WriteFile(inside, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere.mdx")
	if err := os.WriteFile(outside, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rp := newPathRP(t, root)
	if got := rp.DisplayName("/snippets/foo.mdx", "", &git.FileInfo{Path: inside}); got != "snippets/foo.mdx" {
		t.Errorf("DisplayName(resolved) = %q, want %q", got, "snippets/foo.mdx")
	}
	if got := rp.DisplayName("./nope.mdx", "", nil); got != "./nope.mdx" {
		t.Errorf("DisplayName(unresolved) = %q, want the raw capture", got)
	}
	if got := rp.DisplayName("x", "", &git.FileInfo{Path: outside}); got != "x" {
		t.Errorf("DisplayName(outside the root) = %q, want the raw capture", got)
	}
	if got := rp.DisplayName("x", "", &git.FileInfo{}); got != "x" {
		t.Errorf("DisplayName(no path) = %q, want the raw capture", got)
	}
	if got := newPathRP(t, "").DisplayName("x", "", &git.FileInfo{Path: inside}); got != "x" {
		t.Errorf("DisplayName(no root) = %q, want the raw capture", got)
	}

	// Under any other resolver the capture is a name, not a path, so it is
	// reported unchanged.
	hugoRP, err := NewReusablePatternsFor(ReusableConfig{
		Patterns: defaultPatternStrings, Extensions: []string{".md"},
		Root: root, Resolver: config.ResolverHugo,
	})
	if err != nil {
		t.Fatalf("NewReusablePatternsFor: %v", err)
	}
	if got := hugoRP.DisplayName("note", "", &git.FileInfo{Path: inside}); got != "note" {
		t.Errorf("DisplayName(hugo resolver) = %q, want %q", got, "note")
	}

	var nilRP *ReusablePatterns
	if got := nilRP.DisplayName("x", "", &git.FileInfo{Path: inside}); got != "x" {
		t.Errorf("DisplayName(nil receiver) = %q, want the raw capture", got)
	}
}

// TestDisplayName_NoHistoryStillDistinguishes pins the collision fix for
// snippets git knows nothing about. Two uncommitted files in different
// directories, both referenced as <Snippet file="new.mdx" /> from their own
// page, resolve to different files but have no git info at all — deriving the
// display name from the resolution rather than from the (nil) info is what
// keeps them two rows instead of one (#7 review pass 2).
func TestDisplayName_NoHistoryStillDistinguishes(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"g", "a"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "new.mdx"), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	rp := newPathRP(t, root)
	gotG := rp.DisplayName("new.mdx", filepath.Join(root, "g", "page.mdx"), nil)
	gotA := rp.DisplayName("new.mdx", filepath.Join(root, "a", "page.mdx"), nil)
	if gotG != "g/new.mdx" {
		t.Errorf("DisplayName(from g/) = %q, want %q", gotG, "g/new.mdx")
	}
	if gotA != "a/new.mdx" {
		t.Errorf("DisplayName(from a/) = %q, want %q", gotA, "a/new.mdx")
	}
	if gotG == gotA {
		t.Errorf("two distinct uncommitted snippets collapsed into one name %q", gotG)
	}

	// Nothing resolves: the raw capture is still the fallback.
	if got := rp.DisplayName("nope.mdx", filepath.Join(root, "g", "page.mdx"), nil); got != "nope.mdx" {
		t.Errorf("DisplayName(nothing resolves) = %q, want the raw capture", got)
	}
}

// TestFindReusables_MintlifyQuoteStyles checks that both MDX quote styles are
// detected and that <SnippetGroup> is not mistaken for a snippet include
// (#7 review).
func TestFindReusables_MintlifyQuoteStyles(t *testing.T) {
	rp := newPathRP(t, t.TempDir())
	content := "# Title\n\n" +
		`<Snippet file='/snippets/single.mdx' />` + "\n" +
		`<Snippet file="/snippets/double.mdx" />` + "\n" +
		`<Snippet other='x' file='/snippets/attrs-before.mdx' more='y' />` + "\n" +
		`<SnippetGroup file="/snippets/group.mdx" />` + "\n"

	got := append([]string(nil), FindReusables(content, rp)...)
	sort.Strings(got)
	// The tag names are captured as import-map symbols (#68) and are skipped at
	// resolution; what matters here is that no <SnippetGroup> *path* leaked in.
	want := []string{"/snippets/attrs-before.mdx", "/snippets/double.mdx", "/snippets/single.mdx",
		"Snippet", "SnippetGroup"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FindReusables = %v, want %v", got, want)
	}
	for _, ref := range got {
		if ref == "/snippets/group.mdx" {
			t.Error("<SnippetGroup file=…> leaked a snippet path capture")
		}
	}
}

// TestReusableConfig_CacheIsUsed checks the #65 wiring on the parser side: when
// ReusableConfig.Cache is set, repeated resolution of the same snippet goes
// through the shared cache instead of re-invoking git, and the resolved dates
// are identical to a ReusablePatterns built without one.
func TestReusableConfig_CacheIsUsed(t *testing.T) {
	repo := testutil.NewRepo(t)
	when := time.Date(2026, 1, 15, 9, 0, 0, 0, time.UTC)
	repo.Commit(when, "add snippet", map[string]string{
		"snippets/shared.mdx": "shared\n",
		"docs/a.mdx":          "# A\n",
	})
	page := repo.Path("docs/a.mdx")

	uncached := newPathRP(t, repo.Dir)

	cache := git.NewFileInfoCache()
	cached, err := NewReusablePatternsFor(ReusableConfig{
		Patterns:   mintlifyPatternStrings,
		Extensions: []string{".mdx", ".md"},
		Root:       repo.Dir,
		Resolver:   config.ResolverPath,
		Cache:      cache,
	})
	if err != nil {
		t.Fatalf("NewReusablePatternsFor: %v", err)
	}

	want := GetReusableInfo("shared.mdx", page, uncached)
	if want == nil {
		t.Fatal("uncached resolution found no history for the snippet")
	}
	for i := 0; i < 5; i++ {
		got := GetReusableInfo("shared.mdx", page, cached)
		if got == nil {
			t.Fatalf("cached resolution %d found no history", i)
		}
		if !got.LastModified.Equal(want.LastModified) || got.LastCommit != want.LastCommit {
			t.Fatalf("cached resolution %d = %+v, uncached %+v", i, *got, *want)
		}
	}

	hits, misses := cache.Stats()
	if misses != 1 {
		t.Errorf("misses = %d, want 1: the snippet should be looked up once", misses)
	}
	if hits != 4 {
		t.Errorf("hits = %d, want 4", hits)
	}

	// A ReusablePatterns with no cache must behave exactly as before.
	if _, misses := (*git.FileInfoCache)(nil).Stats(); misses != 0 {
		t.Errorf("nil cache reported %d misses", misses)
	}
	if again := GetReusableInfo("shared.mdx", page, uncached); again == nil ||
		!again.LastModified.Equal(want.LastModified) {
		t.Errorf("uncached resolution is not stable: %+v vs %+v", again, want)
	}
}

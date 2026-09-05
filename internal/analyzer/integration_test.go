package analyzer

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/git"
	"github.com/nrynss/rustydocs/internal/parser"
	"github.com/nrynss/rustydocs/internal/testutil"
)

// pinNow freezes analyzer.nowFunc for deterministic staleness math.
func pinNow(t *testing.T, at time.Time) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return at }
	t.Cleanup(func() { nowFunc = old })
}

func TestAnalyze_StaleAndFreshFiles(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -200), "old", map[string]string{
		"docs/old.md": "# Old\n\nold body\n",
	})
	// A separate commit adds new.md only; old.md stays blamed to the first commit.
	repo.Commit(now.AddDate(0, 0, -5), "new", map[string]string{
		"docs/new.md": "# New\n\nnew body\n",
	})

	cfg := config.DefaultConfig()
	cfg.ThresholdDays = 90
	cfg.ContentDir = repo.Path("docs")

	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.TotalFiles() != 2 {
		t.Fatalf("TotalFiles = %d, want 2", res.TotalFiles())
	}
	if res.StaleFiles() != 1 {
		t.Errorf("StaleFiles = %d, want 1", res.StaleFiles())
	}
	if res.StaleSections() != 1 {
		t.Errorf("StaleSections = %d, want 1", res.StaleSections())
	}
	if res.TotalSections() != 2 {
		t.Errorf("TotalSections = %d, want 2", res.TotalSections())
	}
	if oldest := res.OldestFile(); oldest == nil || oldest.RelativePath != "old.md" {
		t.Errorf("OldestFile = %v, want old.md", oldest)
	}
	for _, f := range res.Files {
		if f.HistoryMissing {
			t.Errorf("committed file %q flagged HistoryMissing", f.RelativePath)
		}
	}
}

// TestAnalyze_HugoSiteFixture runs the full analyzer over the committed
// testdata/hugo-site fixture: it exercises Hugo-root auto-detection and Hugo
// shortcode resolution (including the traced readFile data dependency) end to
// end, not just in isolation.
func TestAnalyze_HugoSiteFixture(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := testutil.NewRepo(t)
	repo.CommitTree(now.AddDate(0, 0, -300), "import hugo site", "hugo-site", ".")

	cfg := config.DefaultConfig()
	cfg.ThresholdDays = 90
	cfg.ContentDir = repo.Path("content/docs") // layouts/ lives at the repo root

	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// _index.md, install.md, guide.mdx
	if res.TotalFiles() != 3 {
		t.Fatalf("TotalFiles = %d, want 3 (%v)", res.TotalFiles(), res.Files)
	}
	// Everything was committed 300 days ago (> 90-day threshold).
	if res.StaleFiles() != 3 {
		t.Errorf("StaleFiles = %d, want 3", res.StaleFiles())
	}
	// The {{< note >}} shortcode must be detected and resolved to a date via the
	// auto-detected layouts/ root.
	var note *ReusableInfo
	for i := range res.AllReusables {
		if res.AllReusables[i].Name == "note" {
			note = &res.AllReusables[i]
		}
	}
	if note == nil {
		t.Fatalf("expected 'note' reusable, got %+v", res.AllReusables)
	}
	if note.LastUpdated == nil {
		t.Error("'note' shortcode was not resolved to a date (Hugo root / readFile tracing)")
	}
}

// TestAnalyze_ProfilesControlReusableDetection pins #11: a plain Markdown
// repo (no layouts/ above it) auto-selects the markdown profile, under which
// Hugo shortcodes and MDX components in the text are NOT treated as reusables.
// Selecting the hugo profile explicitly turns detection back on.
func TestAnalyze_ProfilesControlReusableDetection(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -10), "v", map[string]string{
		"docs/page.md":   "# Intro\n\nSee <Foo /> and {{< bar >}} here.\n",
		"docs/other.mdx": "# MDX\n\nonly analyzed under hugo\n",
	})

	countReusables := func(res *Results) int {
		n := 0
		for _, f := range res.Files {
			for _, s := range f.Sections {
				n += len(s.Reusables)
			}
		}
		return n
	}

	// Auto: markdown profile.
	cfg := config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")
	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze(auto): %v", err)
	}
	if cfg.ResolvedProfile.Name != config.ProfileMarkdown || !cfg.ProfileAuto {
		t.Errorf("resolved %q auto=%v, want markdown auto-detected", cfg.ResolvedProfile.Name, cfg.ProfileAuto)
	}
	if res.TotalFiles() != 1 {
		t.Errorf("markdown profile should analyze only page.md, got %d files", res.TotalFiles())
	}
	if n := countReusables(res); n != 0 || len(res.AllReusables) != 0 {
		t.Errorf("markdown profile must not detect reusables; sections=%d all=%v", n, res.AllReusables)
	}

	// Explicit hugo profile.
	hugoCfg := config.DefaultConfig()
	hugoCfg.ContentDir = repo.Path("docs")
	hugoCfg.Profile = config.ProfileHugo
	res, err = Analyze(hugoCfg)
	if err != nil {
		t.Fatalf("Analyze(hugo): %v", err)
	}
	if hugoCfg.ResolvedProfile.Name != config.ProfileHugo || hugoCfg.ProfileAuto {
		t.Errorf("resolved %q auto=%v, want explicit hugo", hugoCfg.ResolvedProfile.Name, hugoCfg.ProfileAuto)
	}
	if res.TotalFiles() != 2 {
		t.Errorf("hugo profile should analyze .md and .mdx, got %d files", res.TotalFiles())
	}
	names := map[string]bool{}
	for _, r := range res.AllReusables {
		names[r.Name] = true
	}
	if !names["Foo"] || !names["bar"] {
		t.Errorf("hugo profile should detect Foo and bar, got %v", res.AllReusables)
	}
}

// TestAnalyze_FilesExcluded pins the diagnostic count behind the zero-files
// warning (#11): files whose extension matched the allowlist but which
// exclude_dirs / exclude_patterns dropped are counted in FilesExcluded, while
// files of other extensions are not (they never matched to begin with).
func TestAnalyze_FilesExcluded(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -10), "v", map[string]string{
		"docs/keep.md":            "# Keep\n\nbody\n",
		"docs/drafts/a.md":        "# A\n\nbody\n",
		"docs/drafts/b.markdown":  "# B\n\nbody\n",
		"docs/drafts/notes.txt":   "not content\n",
		"docs/CHANGELOG.md":       "# Changes\n\nbody\n",
		"docs/other/skipped.json": "{}",
	})

	cfg := config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")
	cfg.ExcludeDirs = []string{"drafts"}
	cfg.ExcludePatterns = []string{"CHANGELOG.md"}
	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.TotalFiles() != 1 {
		t.Errorf("TotalFiles = %d, want 1 (keep.md)", res.TotalFiles())
	}
	if got := res.FilesExcluded(); got != 3 {
		t.Errorf("FilesExcluded = %d, want 3 (drafts/a.md, drafts/b.markdown, CHANGELOG.md)", got)
	}

	// Everything excluded (dir rule plus a glob on the rest): zero analyzed,
	// every extension match counted.
	cfg = config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")
	cfg.ExcludeDirs = []string{"drafts"}
	cfg.ExcludePatterns = []string{"*.md"}
	res, err = Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze(all excluded): %v", err)
	}
	if res.TotalFiles() != 0 || res.FilesExcluded() != 4 {
		t.Errorf("all excluded: TotalFiles=%d FilesExcluded=%d, want 0 and 4", res.TotalFiles(), res.FilesExcluded())
	}

	// No exclusions: nothing is counted as excluded.
	cfg = config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")
	res, err = Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze(no exclusions): %v", err)
	}
	if res.TotalFiles() != 4 || res.FilesExcluded() != 0 {
		t.Errorf("no exclusions: TotalFiles=%d FilesExcluded=%d, want 4 and 0", res.TotalFiles(), res.FilesExcluded())
	}
}

func TestAnalyze_MissingHistoryNotFresh(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -5), "init", map[string]string{
		"docs/tracked.md": "# T\n\nbody\n",
	})
	repo.Write("docs/untracked.md", "# U\n\nbody\n") // never committed

	cfg := config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")

	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got := res.FilesMissingHistory(); got != 1 {
		t.Fatalf("FilesMissingHistory = %d, want 1", got)
	}
	for _, f := range res.Files {
		switch f.RelativePath {
		case "untracked.md":
			if !f.HistoryMissing {
				t.Error("untracked.md should have HistoryMissing=true")
			}
			if f.IsStale() {
				t.Error("untracked.md must not be reported stale (it's unknown, not fresh)")
			}
		case "tracked.md":
			if f.HistoryMissing {
				t.Error("tracked.md should not be HistoryMissing")
			}
		}
	}
}

func TestAnalyze_Modes(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -200), "v", map[string]string{
		"docs/page.md": "# A\n\npara one\n\npara two\n",
	})

	base := config.DefaultConfig()
	base.ThresholdDays = 90
	base.ContentDir = repo.Path("docs")

	// File-level-only: no section parsing, but a tracked file is not missing.
	flo := *base
	flo.FileLevelOnly = true
	res, err := Analyze(&flo)
	if err != nil {
		t.Fatalf("Analyze(file-level-only): %v", err)
	}
	if len(res.Files[0].Sections) != 0 {
		t.Errorf("file-level-only should produce no sections, got %d", len(res.Files[0].Sections))
	}
	if res.Files[0].HistoryMissing {
		t.Error("tracked file wrongly flagged missing in file-level-only mode")
	}

	// Paragraph-level: more than one chunk for a multi-paragraph section.
	pl := *base
	pl.ParagraphLevel = true
	res, err = Analyze(&pl)
	if err != nil {
		t.Fatalf("Analyze(paragraph-level): %v", err)
	}
	if len(res.Files[0].Sections) < 2 {
		t.Errorf("paragraph-level should split into >=2 chunks, got %d", len(res.Files[0].Sections))
	}
}

func TestAnalyze_Errors(t *testing.T) {
	if _, err := AnalyzeWithProgress(&config.Config{ContentDir: ""}, nil); err == nil {
		t.Error("empty ContentDir should error")
	}
	cfg := config.DefaultConfig()
	cfg.ContentDir = t.TempDir() + "/does-not-exist"
	if _, err := Analyze(cfg); err == nil {
		t.Error("nonexistent ContentDir should error")
	}
}

func TestPrintProgress(t *testing.T) {
	var buf bytes.Buffer
	printProgress(&buf, 3, 10)
	out := buf.String()
	if !strings.Contains(out, "30%") {
		t.Errorf("progress missing percentage: %q", out)
	}
	if !strings.Contains(out, "(3/10 files)") {
		t.Errorf("progress missing file counter: %q", out)
	}
	// Guards: nil writer and zero total must not panic or write.
	printProgress(nil, 1, 1)
	buf.Reset()
	printProgress(&buf, 0, 0)
	if buf.Len() != 0 {
		t.Errorf("zero total should produce no output, got %q", buf.String())
	}
}

// TestAnalyzeWithProgress_ProgressBranch exercises the progress-reporter goroutine
// path. It writes to io.Discard because the output content is not asserted here;
// this test only validates that the progress-enabled path runs cleanly.
func TestAnalyzeWithProgress_ProgressBranch(t *testing.T) {
	now := time.Now()
	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -10), "v", map[string]string{
		"docs/a.md": "# A\n\nbody\n",
	})
	cfg := config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")

	res, err := AnalyzeWithProgress(cfg, io.Discard)
	if err != nil {
		t.Fatalf("AnalyzeWithProgress: %v", err)
	}
	if res.TotalFiles() != 1 {
		t.Errorf("TotalFiles = %d, want 1", res.TotalFiles())
	}
}

// TestResults_Accessors covers the aggregate accessors (including the
// divide-by-zero guards) with a hand-built Results, no git required.
func TestResults_Accessors(t *testing.T) {
	old := time.Now().AddDate(0, 0, -300)
	staleSec := parser.Chunk{Title: "S", StartLine: 1, Lines: []git.LineInfo{{LineNumber: 1, Timestamp: old}}}
	freshSec := parser.Chunk{Title: "F", StartLine: 1, Lines: []git.LineInfo{{LineNumber: 1, Timestamp: time.Now()}}}

	res := &Results{
		Files: []FileAnalysis{
			{RelativePath: "a.md", Sections: []parser.Section{staleSec}, StaleSections: []parser.Section{staleSec}, OldestSectionDays: 300},
			{RelativePath: "b.md", Sections: []parser.Section{freshSec}},
			{RelativePath: "c.md", HistoryMissing: true},
		},
	}

	if res.TotalFiles() != 3 {
		t.Errorf("TotalFiles = %d, want 3", res.TotalFiles())
	}
	if res.StaleFiles() != 1 {
		t.Errorf("StaleFiles = %d, want 1", res.StaleFiles())
	}
	if res.FilesMissingHistory() != 1 {
		t.Errorf("FilesMissingHistory = %d, want 1", res.FilesMissingHistory())
	}
	if res.TotalSections() != 2 {
		t.Errorf("TotalSections = %d, want 2", res.TotalSections())
	}
	if res.StaleSections() != 1 {
		t.Errorf("StaleSections = %d, want 1", res.StaleSections())
	}
	if p := res.StaleFilesPct(); p < 33.0 || p > 34.0 {
		t.Errorf("StaleFilesPct = %.2f, want ~33.3", p)
	}
	if p := res.StaleSectionsPct(); p != 50 {
		t.Errorf("StaleSectionsPct = %.2f, want 50", p)
	}
	if of := res.OldestFile(); of == nil || of.RelativePath != "a.md" {
		t.Errorf("OldestFile = %v, want a.md", of)
	}

	// Empty results must not divide by zero.
	empty := &Results{}
	if empty.StaleFilesPct() != 0 || empty.StaleSectionsPct() != 0 {
		t.Error("empty Results percentages should be 0")
	}
	if empty.OldestFile() != nil {
		t.Error("empty Results OldestFile should be nil")
	}
}

// TestAnalyze_InvalidPatternIsAnError guards against the old behaviour of
// silently swapping in the Hugo profile's patterns when a configured reusable
// pattern fails to compile (#11). An invalid pattern must surface as an error
// that names the pattern, before any file is analyzed, regardless of profile.
func TestAnalyze_InvalidPatternIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\n\n{{< alert >}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	const bad = `(unclosed`
	cfg := config.DefaultConfig()
	cfg.ContentDir = dir
	cfg.Profile = config.ProfileMarkdown
	cfg.Reusables.Patterns = []string{bad}

	res, err := Analyze(cfg)
	if err == nil {
		t.Fatal("Analyze with an uncompilable reusable pattern must return an error")
	}
	if res != nil {
		t.Errorf("Analyze must not return results alongside the error, got %d files", res.TotalFiles())
	}
	if !strings.Contains(err.Error(), bad) {
		t.Errorf("error should name the offending pattern %q, got: %v", bad, err)
	}
	// The configured patterns must be left as the user wrote them: no Hugo
	// fallback was substituted.
	if len(cfg.Reusables.Patterns) != 1 || cfg.Reusables.Patterns[0] != bad {
		t.Errorf("Reusables.Patterns were rewritten to %v; the invalid pattern must not be replaced", cfg.Reusables.Patterns)
	}

	// The per-file path is equally strict: analyzeFile returns the compile
	// error rather than switching to the Hugo pattern set.
	fa, ferr := analyzeFile(filepath.Join(dir, "a.md"), cfg, dir)
	if ferr == nil {
		t.Fatal("analyzeFile with an uncompilable reusable pattern must return an error")
	}
	if !strings.Contains(ferr.Error(), bad) {
		t.Errorf("analyzeFile error should name the pattern %q, got: %v", bad, ferr)
	}
	if len(fa.Sections) != 0 || len(fa.Reusables) != 0 {
		t.Errorf("analyzeFile must not have detected anything (sections=%d reusables=%d)", len(fa.Sections), len(fa.Reusables))
	}
}

// TestAnalyze_FilesSkippedByExtension pins the partial-scan diagnostic (#11):
// files that are documentation under another built-in profile but not under
// the active allowlist are counted, while files that are not documentation
// under any profile (.txt, .png) are not, and neither are files the
// exclusions would have dropped anyway.
func TestAnalyze_FilesSkippedByExtension(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -10), "v", map[string]string{
		"docs/a.md":        "# A\n\nbody\n",
		"docs/guide.mdx":   "# Guide\n\nbody\n",
		"docs/notes.txt":   "not content\n",
		"docs/diagram.png": "not really a png\n",
	})

	// markdown profile: .mdx is documentation elsewhere, so it is counted;
	// .txt and .png are not documentation anywhere and are ignored.
	cfg := config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")
	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.TotalFiles() != 1 {
		t.Errorf("TotalFiles = %d, want 1 (a.md)", res.TotalFiles())
	}
	if got := res.FilesSkippedByExtension(); got != 1 {
		t.Errorf("FilesSkippedByExtension = %d, want 1 (guide.mdx)", got)
	}
	if got := res.SkippedExtensions(); len(got) != 1 || got[0] != ".mdx" {
		t.Errorf("SkippedExtensions = %v, want [.mdx]", got)
	}

	// hugo profile: .mdx is in the allowlist, so nothing is skipped.
	cfg = config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")
	cfg.Profile = config.ProfileHugo
	res, err = Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze(hugo): %v", err)
	}
	if res.TotalFiles() != 2 {
		t.Errorf("hugo: TotalFiles = %d, want 2", res.TotalFiles())
	}
	if got := res.FilesSkippedByExtension(); got != 0 {
		t.Errorf("hugo: FilesSkippedByExtension = %d, want 0", got)
	}
	if got := res.SkippedExtensions(); len(got) != 0 {
		t.Errorf("hugo: SkippedExtensions = %v, want empty", got)
	}

	// An .mdx the exclusions would drop anyway is not counted: widening the
	// allowlist would not analyze it.
	cfg = config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")
	cfg.ExcludePatterns = []string{"guide.mdx"}
	res, err = Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze(excluded mdx): %v", err)
	}
	if got := res.FilesSkippedByExtension(); got != 0 {
		t.Errorf("excluded mdx: FilesSkippedByExtension = %d, want 0", got)
	}
}

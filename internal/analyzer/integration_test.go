package analyzer

import (
	"bytes"
	"io"
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
// path. It writes to io.Discard rather than a buffer it reads, since the reporter
// goroutine is not joined before the call returns.
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

package report

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/analyzer"
	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/git"
	"github.com/nrynss/rustydocs/internal/parser"
)

// TestReadableSnapshotProvenanceAndWarnings keeps fresh snippet evidence and coverage gaps visible in both readable formats.
func TestReadableSnapshotProvenanceAndWarnings(t *testing.T) {
	_, res, scan := snapshotFixture(t)
	// A wildly different wall clock must not change any exported age.
	oldNow := nowFunc
	nowFunc = func() time.Time { return daysAgo(-999) }
	t.Cleanup(func() { nowFunc = oldNow })
	for _, format := range []string{"html", "md"} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "report."+format)
			var err error
			if format == "html" {
				err = GenerateHTML(res, res.Config, path)
			} else {
				err = GenerateMarkdown(res, res.Config, path)
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(raw)
			for _, want := range []string{"Sections made fresh by snippets", "Included", "Import use", "snippets/shared.mdx", "supplies freshness", "Own content: 2025-12-06", "2026-06-14", "untracked.mdx", "Unknown or partial history", "history_missing", "reusable_unresolved", "missing.mdx", "Reusable Components"} {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, forbidden := range []string{"localStorage", "rustydocs-states", "markReviewed", "data-state", "status-filter", "class=\"tabs\"", "File last updated", "Oldest content", "oldest-file", "days_stale"} {
				if strings.Contains(text, forbidden) {
					t.Errorf("obsolete presentation: %s", forbidden)
				}
			}
			if strings.Contains(text, "1009") {
				t.Fatal("presentation used wall clock")
			}
			if format == "html" {
				// Fresh snippet use appears inside its dedicated section, not just the
				// global inventory; unknown file details remain visible independently.
				from := strings.Index(text, `<section id="snippet-updates">`)
				to := strings.Index(text, `<section id="unknown">`)
				if from < 0 || to < from || !strings.Contains(text[from:to], "Included") || !strings.Contains(text[from:to], "supplies freshness") {
					t.Fatal("fresh snippet section missing")
				}
				if !strings.Contains(text, `<details class="file-group"`) || !strings.Contains(text, `<nav aria-label="Report navigation">`) {
					t.Fatal("missing navigation/expandable groups")
				}
			}
		})
	}
	if scan.Summary.StaleSections != 4 {
		t.Fatal(scan.Summary)
	}
}

// TestReadableEscapingAndErrorPaths preserves HTML escaping and propagates report-writing failures.
func TestReadableEscapingAndErrorPaths(t *testing.T) {
	res, cfg := sampleResults()
	res.Files[0].Sections[0].Title = "<script>alert('title')</script> | title\nline"
	for _, generate := range []func(*analyzer.Results, *config.Config, string) error{GenerateHTML, GenerateMarkdown} {
		blocker := filepath.Join(t.TempDir(), "blocker")
		if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		if generate(res, cfg, filepath.Join(blocker, "report")) == nil {
			t.Fatal("mkdir error hidden")
		}
		if generate(res, cfg, t.TempDir()) == nil {
			t.Fatal("write error hidden")
		}
	}
	path := filepath.Join(t.TempDir(), "report.html")
	if err := GenerateHTML(res, cfg, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "<script>alert('title')</script>") {
		t.Fatal("unescaped document title")
	}
	cfg.FileLevelOnly = true
	if err := GenerateHTML(res, cfg, path); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "File-only mode: sections were not analyzed") {
		t.Fatal("missing file-only coverage notice")
	}
}

// TestActualHTMLScriptSortsEveryTable executes the generated script against multiple rendered table shapes.
func TestActualHTMLScriptSortsEveryTable(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable for browser-script regression")
	}
	res, cfg := bugfixResults(t)
	rows := []parser.Section{}
	for i, author := range []string{"9", "10", "Alpha"} {
		rows = append(rows, parser.Chunk{Title: []string{"Zebra", "apple", "Beta"}[i], StartLine: []int{10, 2, 3}[i], Lines: []git.LineInfo{{Author: author, Timestamp: daysAgo([]int{200, 100, 400}[i])}}})
	}
	res.Files = append(res.Files, analyzer.FileAnalysis{Path: "docs/another.md", RelativePath: "docs/another.md", Sections: rows})
	old := daysAgo(400)
	mid := daysAgo(100)
	res.AllReusables = append(res.AllReusables, analyzer.ReusableInfo{Name: "second", LastUpdated: &old, LastAuthor: "9"}, analyzer.ReusableInfo{Name: "third", LastUpdated: &mid, LastAuthor: "10"})
	path := filepath.Join(t.TempDir(), "report.html")
	if err := GenerateHTML(res, cfg, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "testdata/sorting.cjs")
	cmd.Stdin = strings.NewReader(string(data))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actual report JavaScript: %v\n%s", err, out)
	}
}

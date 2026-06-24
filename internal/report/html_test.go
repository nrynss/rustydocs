package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/analyzer"
	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/git"
	"github.com/nrynss/rustydocs/internal/parser"
)

// fixedNow is the pinned "now" used by these tests. Choosing a stable instant
// (not time.Now) makes day-deltas exact and reproducible across formats.
var fixedNow = time.Date(2026, time.June, 24, 12, 0, 0, 0, time.UTC)

// pinNow pins the package-level nowFunc to fixedNow and restores time.Now after
// the test. Day-counts are computed off fixedNow, so all timestamps in the
// hand-built Results are derived from it.
func pinNow(t *testing.T) {
	t.Helper()
	nowFunc = func() time.Time { return fixedNow }
	t.Cleanup(func() { nowFunc = time.Now })
}

// daysAgo returns a timestamp exactly n*24h before the pinned now, so that
// int(nowFunc().Sub(ts).Hours()/24) == n exactly (wall-clock arithmetic, no
// calendar/DST drift).
func daysAgo(n int) time.Time {
	return fixedNow.Add(-time.Duration(n) * 24 * time.Hour)
}

// bugfixResults builds a Results exercising every reporting branch the bug
// fixes (#55, #56) and the reusable tables depend on:
//   - a critical stale section (~400 days old) with real line history,
//   - a stale section with EMPTY Lines (LastUpdated() == nil) — the
//     reusable-only-stale shape that must render as "unknown"/"—", never 999,
//   - a file-level reusable that is fresh and one with an unknown date,
//   - a file with HistoryMissing == true (not stale) for the #55 warning,
//   - top-level AllReusables with a fresh and an unknown-date entry.
func bugfixResults(t *testing.T) (*analyzer.Results, *config.Config) {
	t.Helper()

	critTS := daysAgo(400) // >= Critical (365) => class "critical"
	critFile := daysAgo(400)

	criticalSection := parser.Chunk{
		Title:     "Installation Guide",
		Level:     2,
		StartLine: 12,
		EndLine:   20,
		IsHeader:  true,
		Lines: []git.LineInfo{
			{LineNumber: 12, Author: "Ada Lovelace", Timestamp: daysAgo(420)},
			{LineNumber: 13, Author: "Grace Hopper", Timestamp: critTS},
		},
	}

	// Reusable-only-stale shape: a stale section with no line history. Its
	// LastUpdated() is nil, so it has no resolvable date. See #56.
	unknownSection := parser.Chunk{
		Title:     "Shared Snippet",
		Level:     2,
		StartLine: 30,
		EndLine:   34,
		IsHeader:  true,
		Lines:     nil, // empty => LastUpdated() == nil
	}

	freshReusableTS := daysAgo(3)

	staleFile := analyzer.FileAnalysis{
		Path:                 "docs/setup.md",
		RelativePath:         "docs/setup.md",
		Sections:             []parser.Section{criticalSection, unknownSection},
		StaleSections:        []parser.Section{criticalSection, unknownSection},
		EffectiveLastUpdated: &critFile,
		OldestSectionDate:    &critFile,
		DaysStale:            400,
		OldestSectionDays:    400,
		Reusables: []analyzer.ReusableInfo{
			{Name: "fresh-include", LastUpdated: &freshReusableTS, IsFresh: true, LastAuthor: "Katherine Johnson"},
			{Name: "unknown-include", LastUpdated: nil},
		},
	}

	// #55: a file with no git history must NOT be treated as fresh; it is not
	// stale (no StaleSections) but must be counted in FilesMissingHistory.
	missingHistoryFile := analyzer.FileAnalysis{
		Path:           "docs/uncommitted.md",
		RelativePath:   "docs/uncommitted.md",
		HistoryMissing: true,
	}

	allReusableFreshTS := daysAgo(10)
	cfg := config.DefaultConfig()
	cfg.ContentDir = "docs"
	cfg.ShowReusables = true

	res := &analyzer.Results{
		Files:       []analyzer.FileAnalysis{staleFile, missingHistoryFile},
		Config:      cfg,
		GeneratedAt: fixedNow,
		AllReusables: []analyzer.ReusableInfo{
			{Name: "top-fresh", LastUpdated: &allReusableFreshTS, IsFresh: true, LastAuthor: "Margaret Hamilton"},
			{Name: "top-unknown", LastUpdated: nil},
		},
	}
	return res, cfg
}

func TestGenerateHTML(t *testing.T) {
	pinNow(t)
	res, cfg := bugfixResults(t)

	out := filepath.Join(t.TempDir(), "out.html")
	if err := GenerateHTML(res, cfg, out); err != nil {
		t.Fatalf("GenerateHTML: %v (template execution must not error)", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)

	if !strings.Contains(html, "Stale Documentation Report") {
		t.Error("HTML missing report title")
	}
	if !strings.Contains(html, "docs/setup.md") {
		t.Error("HTML missing stale file RelativePath")
	}
	if !strings.Contains(html, "Installation Guide") {
		t.Error("HTML missing the critical section title")
	}
	// The critical section is >= 365 days old, so it must carry the "critical"
	// staleness class on its row.
	if !strings.Contains(html, `class="critical"`) {
		t.Errorf("HTML missing critical staleness class:\n%s", html)
	}
	// 400 is the exact day-delta for the critical section under the pinned now.
	if !strings.Contains(html, "400") {
		t.Error("HTML missing the 400-day staleness value for the critical section")
	}
}

// TestUnknownDateConsistency pins #56: a stale section with no resolvable date
// must render as unknown ("—" days, "unknown" class/level) across HTML, JSON,
// and Markdown — never a fabricated 999/0 mislabeled critical.
func TestUnknownDateConsistency(t *testing.T) {
	pinNow(t)
	res, cfg := bugfixResults(t)
	dir := t.TempDir()

	// --- HTML ---
	htmlOut := filepath.Join(dir, "out.html")
	if err := GenerateHTML(res, cfg, htmlOut); err != nil {
		t.Fatalf("GenerateHTML: %v", err)
	}
	htmlData, err := os.ReadFile(htmlOut)
	if err != nil {
		t.Fatal(err)
	}
	html := string(htmlData)

	// The unknown-date section row uses the "unknown" class and renders an em
	// dash for the day count (template: {{if .DateKnown}}{{.DaysStale}}{{else}}—{{end}}).
	if !strings.Contains(html, `class="unknown"`) {
		t.Errorf("HTML: unknown-date section row missing class=\"unknown\":\n%s", html)
	}
	if !strings.Contains(html, "—") {
		t.Error("HTML: unknown-date section did not render an em dash for days")
	}
	// The unknown-date section must never be labeled critical via a fabricated
	// 999 rendered as a table cell. (A bare "999" also appears as a CSS color
	// "#999" in the template, so scope the check to a day-count <td>.)
	if strings.Contains(html, ">999<") {
		t.Error("HTML: fabricated 999 day count leaked for unknown-date section")
	}

	// --- JSON ---
	jsonOut := filepath.Join(dir, "out.json")
	if err := GenerateJSON(res, cfg, jsonOut); err != nil {
		t.Fatalf("GenerateJSON: %v", err)
	}
	jsonData, err := os.ReadFile(jsonOut)
	if err != nil {
		t.Fatal(err)
	}
	var report JSONReport
	if err := json.Unmarshal(jsonData, &report); err != nil {
		t.Fatalf("JSON output is not valid: %v", err)
	}

	var unknownLevel, criticalLevel string
	for _, f := range report.Files {
		if f.Path != "docs/setup.md" {
			continue
		}
		for _, s := range f.Sections {
			switch s.Title {
			case "Shared Snippet":
				unknownLevel = s.Level
			case "Installation Guide":
				criticalLevel = s.Level
			}
		}
	}
	if unknownLevel != "unknown" {
		t.Errorf("JSON: unknown-date section level = %q, want \"unknown\"", unknownLevel)
	}
	if criticalLevel != "critical" {
		t.Errorf("JSON: critical section level = %q, want \"critical\"", criticalLevel)
	}

	// --- Markdown ---
	mdOut := filepath.Join(dir, "out.md")
	if err := GenerateMarkdown(res, cfg, mdOut); err != nil {
		t.Fatalf("GenerateMarkdown: %v", err)
	}
	mdData, err := os.ReadFile(mdOut)
	if err != nil {
		t.Fatal(err)
	}
	md := string(mdData)

	// The unknown-date section's days cell is "—" (em dash), not a fabricated
	// "999" or "0".
	if !strings.Contains(md, "| Shared Snippet | Unknown | — |") {
		t.Errorf("Markdown: unknown-date section did not render Unknown/em-dash cells:\n%s", md)
	}
	if strings.Contains(md, "| 999 |") {
		t.Error("Markdown: fabricated | 999 | day count present for unknown-date section")
	}
	if strings.Contains(md, "| Shared Snippet | Unknown | 0 |") {
		t.Error("Markdown: fabricated 0-day count present for unknown-date section")
	}
}

// TestMissingHistoryReporting pins #55: a file with no git history is surfaced
// as "files missing history" (unknown), not silently dropped or treated as
// fresh, in every format.
func TestMissingHistoryReporting(t *testing.T) {
	pinNow(t)
	res, cfg := bugfixResults(t)
	dir := t.TempDir()

	// --- JSON summary ---
	jsonOut := filepath.Join(dir, "out.json")
	if err := GenerateJSON(res, cfg, jsonOut); err != nil {
		t.Fatalf("GenerateJSON: %v", err)
	}
	jsonData, err := os.ReadFile(jsonOut)
	if err != nil {
		t.Fatal(err)
	}
	var report JSONReport
	if err := json.Unmarshal(jsonData, &report); err != nil {
		t.Fatalf("JSON output is not valid: %v", err)
	}
	if report.Summary.FilesMissingHistory != 1 {
		t.Errorf("JSON files_missing_history = %d, want 1", report.Summary.FilesMissingHistory)
	}

	// --- Markdown ---
	mdOut := filepath.Join(dir, "out.md")
	if err := GenerateMarkdown(res, cfg, mdOut); err != nil {
		t.Fatalf("GenerateMarkdown: %v", err)
	}
	mdData, err := os.ReadFile(mdOut)
	if err != nil {
		t.Fatal(err)
	}
	md := string(mdData)
	if !strings.Contains(md, "no git history") {
		t.Errorf("Markdown missing the 'no git history' note:\n%s", md)
	}

	// --- HTML ---
	htmlOut := filepath.Join(dir, "out.html")
	if err := GenerateHTML(res, cfg, htmlOut); err != nil {
		t.Fatalf("GenerateHTML: %v", err)
	}
	htmlData, err := os.ReadFile(htmlOut)
	if err != nil {
		t.Fatal(err)
	}
	html := string(htmlData)
	// Template emits the warning under {{if .FilesMissingHistory}}.
	if !strings.Contains(html, "had no git history") {
		t.Errorf("HTML missing the missing-history warning note:\n%s", html)
	}
}

// TestReusableRendering exercises the reusable-table branches in all three
// generators, including both the fresh and unknown-date variants for file-level
// and top-level reusables.
func TestReusableRendering(t *testing.T) {
	pinNow(t)
	res, cfg := bugfixResults(t)
	dir := t.TempDir()

	// --- Markdown reusables table + per-file reusables line ---
	mdOut := filepath.Join(dir, "out.md")
	if err := GenerateMarkdown(res, cfg, mdOut); err != nil {
		t.Fatalf("GenerateMarkdown: %v", err)
	}
	mdData, err := os.ReadFile(mdOut)
	if err != nil {
		t.Fatal(err)
	}
	md := string(mdData)
	if !strings.Contains(md, "## Reusable Components") {
		t.Error("Markdown missing reusable components table header")
	}
	if !strings.Contains(md, "top-fresh") || !strings.Contains(md, "top-unknown") {
		t.Error("Markdown missing top-level reusable rows")
	}
	if !strings.Contains(md, "**Reusables:**") {
		t.Error("Markdown missing per-file reusables line")
	}
	if !strings.Contains(md, "`fresh-include`") || !strings.Contains(md, "`unknown-include` (unknown)") {
		t.Errorf("Markdown missing file-level reusable variants:\n%s", md)
	}

	// --- JSON reusables ---
	jsonOut := filepath.Join(dir, "out.json")
	if err := GenerateJSON(res, cfg, jsonOut); err != nil {
		t.Fatalf("GenerateJSON: %v", err)
	}
	jsonData, err := os.ReadFile(jsonOut)
	if err != nil {
		t.Fatal(err)
	}
	var report JSONReport
	if err := json.Unmarshal(jsonData, &report); err != nil {
		t.Fatalf("JSON output is not valid: %v", err)
	}
	levels := map[string]string{}
	for _, r := range report.Reusables {
		levels[r.Name] = r.Level
	}
	if levels["top-fresh"] == "" {
		t.Error("JSON missing top-fresh reusable")
	}
	if levels["top-unknown"] != "unknown" {
		t.Errorf("JSON top-unknown reusable level = %q, want \"unknown\"", levels["top-unknown"])
	}

	// --- HTML reusables (the data is built for both fresh and unknown) ---
	htmlOut := filepath.Join(dir, "out.html")
	if err := GenerateHTML(res, cfg, htmlOut); err != nil {
		t.Fatalf("GenerateHTML: %v", err)
	}
	if _, err := os.ReadFile(htmlOut); err != nil {
		t.Fatal(err)
	}
}

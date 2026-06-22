package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nrynss/rustydocs/internal/analyzer"
	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/git"
	"github.com/nrynss/rustydocs/internal/parser"
)

func TestEscapeMDCell(t *testing.T) {
	got := escapeMDCell("a | b\nc")
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("newline not removed: %q", got)
	}
	if !strings.Contains(got, "\\|") {
		t.Errorf("pipe not escaped: %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("short", 35); got != "short" {
		t.Errorf("unexpected truncation: %q", got)
	}
	long := strings.Repeat("é", 40) // multi-byte runes
	got := truncateRunes(long, 10)
	if n := utf8.RuneCountInString(got); n != 10 {
		t.Errorf("rune count = %d, want 10", n)
	}
	if !utf8.ValidString(got) {
		t.Error("truncation produced invalid UTF-8 (byte-slicing a rune)")
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("missing ellipsis: %q", got)
	}
}

func sampleResults() (*analyzer.Results, *config.Config) {
	old := time.Now().AddDate(0, 0, -200)
	sec := parser.Chunk{
		Title:     "Config | Options", // pipe must be escaped in Markdown
		Level:     2,
		StartLine: 5,
		EndLine:   9,
		IsHeader:  true,
		Lines:     []git.LineInfo{{LineNumber: 5, Author: "Jane|Doe", Timestamp: old}},
	}
	fa := analyzer.FileAnalysis{
		Path:                 "docs/guide.md",
		RelativePath:         "docs/guide.md",
		Sections:             []parser.Section{sec},
		StaleSections:        []parser.Section{sec},
		EffectiveLastUpdated: &old,
		OldestSectionDate:    &old,
		DaysStale:            200,
		OldestSectionDays:    200,
	}
	cfg := config.DefaultConfig()
	cfg.ContentDir = "docs"
	res := &analyzer.Results{
		Files:       []analyzer.FileAnalysis{fa},
		Config:      cfg,
		GeneratedAt: time.Now(),
	}
	return res, cfg
}

func TestGenerateMarkdown(t *testing.T) {
	res, cfg := sampleResults()
	out := filepath.Join(t.TempDir(), "out.md")
	if err := GenerateMarkdown(res, cfg, out); err != nil {
		t.Fatalf("GenerateMarkdown: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	md := string(data)

	if !strings.Contains(md, "docs/guide.md") {
		t.Error("report missing file path")
	}
	if !strings.Contains(md, "Config \\| Options") {
		t.Errorf("title pipe not escaped in table:\n%s", md)
	}
	if strings.Contains(md, "| Config | Options |") {
		t.Error("raw pipe leaked into Markdown table")
	}
	if !strings.Contains(md, "Jane\\|Doe") {
		t.Error("author pipe not escaped")
	}
}

func TestGenerateJSON(t *testing.T) {
	res, cfg := sampleResults()
	out := filepath.Join(t.TempDir(), "out.json")
	if err := GenerateJSON(res, cfg, out); err != nil {
		t.Fatalf("GenerateJSON: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	var report JSONReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(report.Files) != 1 || report.Files[0].Path != "docs/guide.md" {
		t.Errorf("unexpected JSON files: %+v", report.Files)
	}
	if report.Summary.TotalFiles != 1 {
		t.Errorf("TotalFiles = %d, want 1", report.Summary.TotalFiles)
	}
}

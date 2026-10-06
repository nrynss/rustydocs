// Package report provides portable single-run scan exports.
package report

import (
	"fmt"
	"github.com/nrynss/rustydocs/internal/analyzer"
	"github.com/nrynss/rustydocs/internal/config"
	"os"
	"path/filepath"
	"strings"
)

// GenerateMarkdown writes section findings, snippet provenance and diagnostics.
func GenerateMarkdown(results *analyzer.Results, cfg *config.Config, outputPath string) error {
	data := readableData(results, cfg)
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Stale Documentation Report\n\nGenerated: %s | Threshold: %d days\n\n", data.GeneratedDate, data.ThresholdDays)
	fmt.Fprintf(&sb, "## Summary\n\n- **Files scanned:** %d\n- **Files with stale content:** %d (%.1f%%)\n- **Sections analyzed:** %d\n- **Stale sections:** %d (%.1f%%)\n- **Fresh sections:** %d\n- **Unknown sections:** %d\n", data.Summary.TotalFiles, data.Summary.StaleFiles, data.Summary.StaleFilesPct, data.Summary.TotalSections, data.Summary.StaleSections, data.Summary.StaleSectionsPct, data.Summary.FreshSections, data.Summary.UnknownSections)
	if data.Summary.FilesMissingHistory > 0 {
		fmt.Fprintf(&sb, "- **Files with no git history (own staleness unknown):** %d\n", data.Summary.FilesMissingHistory)
	}
	sb.WriteString("\n")
	if data.FileLevelOnly {
		sb.WriteString("File-only mode: sections were not analyzed; this is not a complete section inventory.\n\n")
	}
	writeFiles := func(title string, files []FileData, empty string) {
		fmt.Fprintf(&sb, "## %s\n\n", title)
		if len(files) == 0 {
			fmt.Fprintf(&sb, "%s\n\n", empty)
		}
		for _, f := range files {
			fmt.Fprintf(&sb, "### %s\n\nHistory: %s\n\n", escapeMDCell(f.Path), f.HistoryStatus)
			if len(f.Sections) == 0 {
				sb.WriteString("No unknown section rows are available. See diagnostics for the file’s history or analysis status.\n\n")
				continue
			}
			sb.WriteString("| Line | Section | Effective Updated | Age (days) | Effective Author | Severity | Snippet evidence |\n|------|---------|-------------------|------------|------------------|----------|------------------|\n")
			for _, s := range f.Sections {
				age := "—"
				if s.DateKnown {
					age = fmt.Sprint(s.DaysStale)
				}
				evidence := s.Evidence
				if evidence == "" {
					evidence = "Own content"
				}
				fmt.Fprintf(&sb, "| L%d | %s | %s | %s | %s | %s | %s |\n", s.StartLine, escapeMDCell(s.Title), s.DateStr, age, escapeMDCell(s.Author), s.StalenessClass, escapeMDCell(evidence))
			}
			sb.WriteString("\n")
		}
	}
	writeFiles("Stale sections", data.Files, "No stale documentation found.")
	sb.WriteString("Snippet freshness describes the effective Git date, not whether surrounding prose is correct.\n\n")
	writeFiles("Sections made fresh by snippets", data.SnippetFiles, "No sections made fresh by snippets.")
	writeFiles("Unknown or partial history", data.UnknownFiles, "No files with unknown or partial history.")
	sb.WriteString("## Diagnostics\n\n")
	if len(data.Diagnostics) == 0 {
		sb.WriteString("No scan diagnostics.\n\n")
	}
	for _, d := range data.Diagnostics {
		fmt.Fprintf(&sb, "- %s\n", escapeMDCell(d))
	}
	if len(data.Reusables) > 0 {
		sb.WriteString("\n## Reusable Components\n\n| Component | Last Updated | Age (days) | Status | Author |\n|-----------|--------------|------------|--------|--------|\n")
		for _, r := range data.Reusables {
			fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s |\n", escapeMDCell(r.Name), r.DateStr, r.Age, r.Status, escapeMDCell(r.Author))
		}
	}
	if err := os.MkdirAll(filepath.Clean(filepath.Dir(outputPath)), 0750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Clean(outputPath), []byte(sb.String()), 0600)
}

// escapeMDCell makes a value safe inside a Markdown table cell: an unescaped
// pipe would start a new column and a newline would break the row.
func escapeMDCell(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	return s
}

// truncateRunes shortens s to at most maxRunes runes (rune-safe), appending an
// ellipsis when truncated. Shared by the Markdown and HTML reports.
func truncateRunes(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	if maxRunes <= 3 {
		return string(r[:maxRunes])
	}
	return string(r[:maxRunes-3]) + "..."
}

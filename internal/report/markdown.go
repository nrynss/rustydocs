// Package report provides report generators for rustydocs.
package report

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nrynss/rustydocs/internal/analyzer"
	"github.com/nrynss/rustydocs/internal/config"
)

// GenerateMarkdown generates a markdown report of stale documentation.
func GenerateMarkdown(results *analyzer.Results, cfg *config.Config, outputPath string) error {
	var sb strings.Builder

	// Header
	sb.WriteString("# Stale Documentation Report\n\n")
	sb.WriteString(fmt.Sprintf("Generated: %s | Threshold: %d days\n\n",
		results.GeneratedAt.Format("2006-01-02 15:04"), cfg.ThresholdDays))

	// Summary
	sb.WriteString("## Summary\n\n")
	sb.WriteString(fmt.Sprintf("- **Files scanned:** %d\n", results.TotalFiles()))
	sb.WriteString(fmt.Sprintf("- **Files with stale content:** %d (%.1f%%)\n",
		results.StaleFiles(), results.StaleFilesPct()))
	sb.WriteString(fmt.Sprintf("- **Sections analyzed:** %d\n", results.TotalSections()))
	sb.WriteString(fmt.Sprintf("- **Stale sections:** %d (%.1f%%)\n",
		results.StaleSections(), results.StaleSectionsPct()))

	if oldest := results.OldestFile(); oldest != nil {
		sb.WriteString(fmt.Sprintf("- **Oldest content:** %s (%d days)\n",
			oldest.RelativePath, oldest.OldestSectionDays))
	}
	sb.WriteString("\n---\n\n")

	// Sort files by oldest section (files with oldest content first)
	staleFiles := make([]analyzer.FileAnalysis, 0)
	for _, f := range results.Files {
		if f.IsStale() {
			staleFiles = append(staleFiles, f)
		}
	}
	sort.Slice(staleFiles, func(i, j int) bool {
		return staleFiles[i].OldestSectionDays > staleFiles[j].OldestSectionDays
	})

	if len(staleFiles) == 0 {
		sb.WriteString("*No stale documentation found!*\n")
	} else {
		for _, fileAnalysis := range staleFiles {
			sb.WriteString(fmt.Sprintf("## %s\n\n", fileAnalysis.RelativePath))

			if fileAnalysis.EffectiveLastUpdated != nil {
				dateStr := fileAnalysis.EffectiveLastUpdated.Format("2006-01-02")
				sb.WriteString(fmt.Sprintf("**File last updated:** %s (%d days ago)\n\n",
					dateStr, fileAnalysis.DaysStale))
			}

			// Sections table
			if len(fileAnalysis.StaleSections) > 0 {
				sb.WriteString("| Line | Section | Last Updated | Days Stale | Author |\n")
				sb.WriteString("|------|---------|--------------|------------|--------|\n")

				for _, section := range fileAnalysis.StaleSections {
					title := section.Title
					if len(title) > 35 {
						title = title[:32] + "..."
					}

					var dateStr string
					var days int
					if lastUpdated := section.LastUpdated(); lastUpdated != nil {
						dateStr = lastUpdated.Format("2006-01-02")
						days = int(time.Since(*lastUpdated).Hours() / 24)
					} else {
						dateStr = "Unknown"
						days = 0
					}

					author := section.LastAuthor()
					if author == "" {
						author = "Unknown"
					}

					sb.WriteString(fmt.Sprintf("| L%d | %s | %s | %d | %s |\n",
						section.StartLine, title, dateStr, days, author))
				}
				sb.WriteString("\n")
			}

			// Reusables info
			if len(fileAnalysis.Reusables) > 0 {
				var reusableStrs []string
				for _, r := range fileAnalysis.Reusables {
					if r.LastUpdated != nil {
						dateStr := r.LastUpdated.Format("2006-01-02")
						status := "fresh"
						if !r.IsFresh {
							status = "stale"
						}
						reusableStrs = append(reusableStrs,
							fmt.Sprintf("`%s` (updated %s - %s)", r.Name, dateStr, status))
					} else {
						reusableStrs = append(reusableStrs,
							fmt.Sprintf("`%s` (unknown)", r.Name))
					}
				}
				sb.WriteString(fmt.Sprintf("**Reusables:** %s\n\n", strings.Join(reusableStrs, ", ")))
			}

			sb.WriteString("---\n\n")
		}
	}

	// Add reusables summary section
	if len(results.AllReusables) > 0 {
		sb.WriteString("\n## Reusable Components\n\n")
		sb.WriteString("| Component | Last Updated | Status |\n")
		sb.WriteString("|-----------|--------------|--------|\n")

		for _, r := range results.AllReusables {
			var dateStr, status string
			if r.LastUpdated != nil {
				dateStr = r.LastUpdated.Format("2006-01-02")
				if r.IsFresh {
					status = "Fresh"
				} else {
					status = "Stale"
				}
			} else {
				dateStr = "Unknown"
				status = "Unknown"
			}
			sb.WriteString(fmt.Sprintf("| `%s` | %s | %s |\n", r.Name, dateStr, status))
		}
		sb.WriteString("\n")
	}

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Clean(filepath.Dir(outputPath)), 0750); err != nil {
		return err
	}

	return os.WriteFile(filepath.Clean(outputPath), []byte(sb.String()), 0600)
}

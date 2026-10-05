// Package report provides portable single-run scan exports.
package report

import (
	"embed"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nrynss/rustydocs/internal/analyzer"
	"github.com/nrynss/rustydocs/internal/config"
)

//go:embed templates/report.html
var templateFS embed.FS

// TemplateData is a read-only view of the captured scan, shared with Markdown.
type TemplateData struct {
	GeneratedDate                     string
	ThresholdDays                     int
	Summary                           JSONSummary
	Files, UnknownFiles, SnippetFiles []FileData
	Reusables                         []ReusableTemplateData
	Diagnostics                       []string
	FileLevelOnly                     bool
}

// ReusableTemplateData presents supporting-file history without review state.
type ReusableTemplateData struct {
	Name, DateStr, Status, StatusClass, Author, Age string
}

// FileData groups section findings with a stable navigation anchor.
type FileData struct {
	Path, Anchor, HistoryStatus string
	Sections                    []SectionData
}

// SectionData presents effective age and authors alongside concise dependency evidence.
type SectionData struct {
	StartLine                                        int
	Title, DateStr, Author, StalenessClass, Evidence string
	DaysStale                                        int
	DateKnown                                        bool
}

// displayDate renders absent change evidence explicitly rather than inventing freshness.
func displayDate(date *time.Time) string {
	if date == nil {
		return "Unknown"
	}
	return date.UTC().Format("2006-01-02")
}

// displayAuthors keeps effective-source authors distinct from absent author evidence.
func displayAuthors(authors []string) string {
	if len(authors) == 0 {
		return "Unknown"
	}
	return strings.Join(authors, ", ")
}

// readableData builds findings and provenance from the same snapshot used by JSON.
func readableData(results *analyzer.Results, cfg *config.Config) TemplateData {
	scan := buildJSON(results, cfg)
	data := TemplateData{GeneratedDate: scan.GeneratedAt, ThresholdDays: cfg.ThresholdDays, Summary: scan.Summary, FileLevelOnly: cfg.FileLevelOnly}
	reusables := map[string]JSONReusable{}
	for _, r := range scan.Reusables {
		reusables[r.ID] = r
		status, cls := "Unknown", "unknown"
		age := "—"
		if r.AgeDays != nil {
			age = fmt.Sprint(*r.AgeDays)
			status, cls = "Fresh", "fresh"
			if r.LastChange.Date.Before(analysisTime(results).Add(-time.Duration(cfg.ThresholdDays) * 24 * time.Hour)) {
				status, cls = "Stale", "stale"
			}
		}
		data.Reusables = append(data.Reusables, ReusableTemplateData{Name: r.Name, DateStr: displayDate(r.LastChange.Date), Status: status, StatusClass: cls, Author: displayAuthors(r.LastChange.Authors), Age: age})
	}
	fileNames, sectionNames := map[string]string{}, map[string]string{}
	for _, f := range scan.Files {
		fileNames[f.ID] = f.ContentPath
		stale := FileData{Path: f.ContentPath, Anchor: f.ID, HistoryStatus: f.HistoryStatus}
		unknown, snippets := stale, stale
		unknown.Anchor += "-unknown"
		snippets.Anchor += "-snippets"
		for _, s := range f.Sections {
			sectionNames[s.ID] = s.Title
			sourceIDs := map[string]bool{}
			for _, source := range s.FreshnessSources {
				if source.ReusableID != nil {
					sourceIDs[*source.ReusableID] = true
				}
			}
			evidence := []string{}
			partialSupport := false
			if len(s.Dependencies) > 0 {
				evidence = append(evidence, "Own content: "+displayDate(s.OwnLastChange.Date)+"; "+displayAuthors(s.OwnLastChange.Authors))
			}
			for _, d := range s.Dependencies {
				// An unimported JSX component is intentionally skipped, and supplies no
				// supporting file. Keep it in JSON without distracting readable tables.
				if d.Status == "skipped" {
					continue
				}
				if d.Status == "partial" {
					partialSupport = true
					evidence = append(evidence, d.Reference+" (partial supporting evidence; see diagnostics)")
				}
				if len(d.ReusableIDs) == 0 {
					evidence = append(evidence, fmt.Sprintf("%s (L%d: %s)", d.Reference, d.Line, d.Status))
				}
				for _, id := range d.ReusableIDs {
					r := reusables[id]
					label := fmt.Sprintf("%s (L%d; %s; %s)", r.Name, d.Line, displayDate(r.LastChange.Date), displayAuthors(r.LastChange.Authors))
					if sourceIDs[id] {
						label += " supplies freshness"
					}
					if r.HistoryStatus == "missing" {
						label += "; history unknown"
					}
					evidence = append(evidence, label)
				}
			}
			row := SectionData{StartLine: s.StartLine, Title: s.Title, DateStr: displayDate(s.EffectiveLastChange.Date), Author: displayAuthors(s.EffectiveLastChange.Authors), StalenessClass: s.Level, Evidence: strings.Join(unique(evidence), "; ")}
			if s.AgeDays != nil {
				row.DaysStale = *s.AgeDays
				row.DateKnown = true
			}
			if s.IsStale {
				stale.Sections = append(stale.Sections, row)
			}
			if s.AgeDays == nil || partialSupport {
				unknown.Sections = append(unknown.Sections, row)
			}
			ownStale := s.OwnLastChange.Date == nil || s.OwnLastChange.Date.Before(analysisTime(results).Add(-time.Duration(cfg.ThresholdDays)*24*time.Hour))
			if !s.IsStale && s.AgeDays != nil && len(sourceIDs) > 0 && ownStale {
				snippets.Sections = append(snippets.Sections, row)
			}
		}
		if len(stale.Sections) > 0 {
			data.Files = append(data.Files, stale)
		}
		if len(unknown.Sections) > 0 || f.HistoryStatus != "available" || f.AnalysisStatus == "failed" {
			data.UnknownFiles = append(data.UnknownFiles, unknown)
		}
		if len(snippets.Sections) > 0 {
			data.SnippetFiles = append(data.SnippetFiles, snippets)
		}
	}
	for _, d := range scan.Diagnostics {
		context := []string{}
		if d.FileID != nil {
			context = append(context, fileNames[*d.FileID])
		}
		if d.SectionID != nil {
			context = append(context, sectionNames[*d.SectionID])
		}
		if d.Reference != nil {
			context = append(context, *d.Reference)
		}
		if d.Line != nil {
			context = append(context, fmt.Sprintf("L%d", *d.Line))
		}
		if d.RepositoryID != nil && d.FileID == nil {
			for _, r := range scan.Repositories {
				if r.ID == *d.RepositoryID {
					context = append(context, "repository "+r.Path)
				}
			}
		}
		label := d.Code + ": " + d.Message
		if len(context) > 0 {
			label += " (" + strings.Join(context, "; ") + ")"
		}
		data.Diagnostics = append(data.Diagnostics, label)
	}
	for _, files := range [][]FileData{data.Files, data.UnknownFiles, data.SnippetFiles} {
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	}
	sort.Slice(data.Reusables, func(i, j int) bool { return data.Reusables[i].Name < data.Reusables[j].Name })
	return data
}

// GenerateHTML writes a self-contained, read-only report from captured evidence.
func GenerateHTML(results *analyzer.Results, cfg *config.Config, outputPath string) error {
	tmpl, err := template.New("report.html").ParseFS(templateFS, "templates/report.html")
	if err != nil {
		return fmt.Errorf("unable to parse template: %w", err)
	}
	if err := os.MkdirAll(filepath.Clean(filepath.Dir(outputPath)), 0750); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Clean(outputPath), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	executeErr := tmpl.Execute(f, readableData(results, cfg))
	closeErr := f.Close()
	if executeErr != nil {
		return executeErr
	}
	return closeErr
}

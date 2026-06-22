// Package report provides report generators for rustydocs.
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

// TemplateData holds data for the HTML template.
type TemplateData struct {
	GeneratedDate     string
	ThresholdDays     int
	TotalFiles        int
	StaleFiles        int
	StaleFilesPct     string
	TotalSections     int
	StaleSections     int
	StaleSectionsPct  string
	OldestFile        string
	OldestDays        int
	Files             []FileData
	Reusables         []ReusableTemplateData
	ShowReusables     bool
	WarningThreshold  int
	CautionThreshold  int
	CriticalThreshold int
}

// ReusableTemplateData holds data for a reusable component in the template.
type ReusableTemplateData struct {
	Name        string
	DateStr     string
	Status      string
	StatusClass string
	Author      string
}

// FileData holds data for a single file in the template.
type FileData struct {
	Path           string
	SidebarPath    string
	Anchor         string
	ShortName      string
	DateStr        string
	DaysStale      int
	OldestDays     int
	StalenessClass string
	Sections       []SectionData
	Reusables      []ReusableData
}

// SectionData holds data for a section in the template.
type SectionData struct {
	StartLine      int
	Title          string
	DateStr        string
	DaysStale      int
	Author         string
	StalenessClass string
}

// ReusableData holds data for a reusable in the template.
type ReusableData struct {
	Name        string
	DateStr     string
	Status      string
	StatusClass string
	Author      string
}

// GenerateHTML generates an HTML report of stale documentation.
func GenerateHTML(results *analyzer.Results, cfg *config.Config, outputPath string) error {
	// Load template
	tmpl, err := template.New("report.html").Funcs(template.FuncMap{
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s) //nolint:gosec // trusted template content
		},
	}).ParseFS(templateFS, "templates/report.html")
	if err != nil {
		return fmt.Errorf("unable to parse template: %w", err)
	}

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

	// Build template data
	var files []FileData
	for _, f := range staleFiles {
		var dateStr string
		if f.EffectiveLastUpdated != nil {
			dateStr = f.EffectiveLastUpdated.Format("2006-01-02")
		} else {
			dateStr = "Unknown"
		}

		anchor := strings.ReplaceAll(f.RelativePath, "/", "-")

		// For sidebar, show path without _index filename
		sidebarPath := f.RelativePath
		pathParts := strings.Split(sidebarPath, "/")
		filename := pathParts[len(pathParts)-1]
		if strings.HasPrefix(filename, "_index") {
			// Remove the _index.*.md filename, show parent path only
			if len(pathParts) > 1 {
				sidebarPath = strings.Join(pathParts[:len(pathParts)-1], "/")
			} else {
				sidebarPath = "(root)"
			}
		}

		shortName := pathParts[len(pathParts)-1]

		var sections []SectionData
		for _, s := range f.StaleSections {
			title := truncateRunes(s.Title, 50)

			var sDateStr string
			var sDays int
			if lastUpdated := s.LastUpdated(); lastUpdated != nil {
				sDateStr = lastUpdated.Format("2006-01-02")
				sDays = int(time.Since(*lastUpdated).Hours() / 24)
			} else {
				sDateStr = "Unknown"
				sDays = 999
			}

			author := s.LastAuthor()
			if author == "" {
				author = "Unknown"
			}

			sections = append(sections, SectionData{
				StartLine:      s.StartLine,
				Title:          title,
				DateStr:        sDateStr,
				DaysStale:      sDays,
				Author:         author,
				StalenessClass: cfg.GetStalenessClass(sDays),
			})
		}

		var reusables []ReusableData
		for _, r := range f.Reusables {
			var rDateStr, status, statusClass, author string
			if r.LastUpdated != nil {
				rDateStr = r.LastUpdated.Format("2006-01-02")
				if r.IsFresh {
					status = "fresh"
					statusClass = "fresh"
				} else {
					status = "stale"
					statusClass = "stale"
				}
				author = r.LastAuthor
			} else {
				rDateStr = "unknown"
				status = "unknown"
				statusClass = "unknown"
				author = "unknown"
			}

			reusables = append(reusables, ReusableData{
				Name:        r.Name,
				DateStr:     rDateStr,
				Status:      status,
				StatusClass: statusClass,
				Author:      author,
			})
		}

		files = append(files, FileData{
			Path:           f.RelativePath,
			SidebarPath:    sidebarPath,
			Anchor:         anchor,
			ShortName:      shortName,
			DateStr:        dateStr,
			DaysStale:      f.DaysStale,
			OldestDays:     f.OldestSectionDays,
			StalenessClass: cfg.GetStalenessClass(f.OldestSectionDays),
			Sections:       sections,
			Reusables:      reusables,
		})
	}

	oldestFile := ""
	oldestDays := 0
	if oldest := results.OldestFile(); oldest != nil {
		oldestFile = oldest.RelativePath
		oldestDays = oldest.OldestSectionDays
	}

	// Build reusables data
	var reusables []ReusableTemplateData
	for _, r := range results.AllReusables {
		var dateStr, status, statusClass, author string
		if r.LastUpdated != nil {
			dateStr = r.LastUpdated.Format("2006-01-02")
			if r.IsFresh {
				status = "Fresh"
				statusClass = "fresh"
			} else {
				status = "Stale"
				statusClass = "stale"
			}
			author = r.LastAuthor
		} else {
			dateStr = "Unknown"
			status = "Unknown"
			statusClass = "unknown"
			author = "Unknown"
		}
		reusables = append(reusables, ReusableTemplateData{
			Name:        r.Name,
			DateStr:     dateStr,
			Status:      status,
			StatusClass: statusClass,
			Author:      author,
		})
	}

	data := TemplateData{
		GeneratedDate:     results.GeneratedAt.Format("2006-01-02 15:04"),
		ThresholdDays:     cfg.ThresholdDays,
		TotalFiles:        results.TotalFiles(),
		StaleFiles:        results.StaleFiles(),
		StaleFilesPct:     fmt.Sprintf("%.1f", results.StaleFilesPct()),
		TotalSections:     results.TotalSections(),
		StaleSections:     results.StaleSections(),
		StaleSectionsPct:  fmt.Sprintf("%.1f", results.StaleSectionsPct()),
		OldestFile:        oldestFile,
		OldestDays:        oldestDays,
		Files:             files,
		Reusables:         reusables,
		ShowReusables:     cfg.ShowReusables,
		WarningThreshold:  cfg.StalenessLevels.Warning,
		CautionThreshold:  cfg.StalenessLevels.Caution,
		CriticalThreshold: cfg.StalenessLevels.Critical,
	}

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Clean(filepath.Dir(outputPath)), 0750); err != nil {
		return err
	}

	// Create output file
	f, err := os.OpenFile(filepath.Clean(outputPath), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	return tmpl.Execute(f, data)
}

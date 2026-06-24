// Package report provides report generators for rustydocs.
package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/nrynss/rustydocs/internal/analyzer"
	"github.com/nrynss/rustydocs/internal/config"
)

// JSONReport is the top-level structure for JSON output.
type JSONReport struct {
	Version     string         `json:"version"`
	GeneratedAt string         `json:"generated_at"`
	Config      JSONConfig     `json:"config"`
	Summary     JSONSummary    `json:"summary"`
	Files       []JSONFile     `json:"files"`
	Reusables   []JSONReusable `json:"reusables,omitempty"`
}

// JSONConfig contains the configuration used for this run.
type JSONConfig struct {
	ThresholdDays   int           `json:"threshold_days"`
	ContentDir      string        `json:"content_dir"`
	StalenessLevels JSONStaleness `json:"staleness_levels"`
}

// JSONStaleness contains staleness threshold configuration.
type JSONStaleness struct {
	Warning  int `json:"warning"`
	Caution  int `json:"caution"`
	Critical int `json:"critical"`
}

// JSONSummary contains aggregate statistics.
type JSONSummary struct {
	TotalFiles          int     `json:"total_files"`
	StaleFiles          int     `json:"stale_files"`
	StaleFilesPct       float64 `json:"stale_files_pct"`
	TotalSections       int     `json:"total_sections"`
	StaleSections       int     `json:"stale_sections"`
	StaleSectionsPct    float64 `json:"stale_sections_pct"`
	FilesMissingHistory int     `json:"files_missing_history,omitempty"`
	OldestFile          string  `json:"oldest_file,omitempty"`
	OldestDays          int     `json:"oldest_days,omitempty"`
}

// JSONFile contains analysis results for a single file.
type JSONFile struct {
	Path          string        `json:"path"`
	LastUpdated   string        `json:"last_updated,omitempty"`
	DaysStale     int           `json:"days_stale"`
	TotalSections int           `json:"total_sections"`
	StaleSections int           `json:"stale_sections"`
	Sections      []JSONSection `json:"sections,omitempty"`
}

// JSONSection contains analysis results for a single section.
type JSONSection struct {
	Title       string `json:"title"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	LastUpdated string `json:"last_updated,omitempty"`
	DaysStale   int    `json:"days_stale"`
	Author      string `json:"author,omitempty"`
	Level       string `json:"level"` // fresh, warning, caution, critical
}

// JSONReusable contains information about a reusable component.
type JSONReusable struct {
	Name        string `json:"name"`
	LastUpdated string `json:"last_updated,omitempty"`
	DaysStale   int    `json:"days_stale"`
	Author      string `json:"author,omitempty"`
	Level       string `json:"level"`
}

// nowFunc returns the current time. It is a package variable so report tests
// can pin "now" and assert deterministic day-deltas across all formats.
var nowFunc = time.Now

// GenerateJSON generates a JSON report of stale documentation.
func GenerateJSON(results *analyzer.Results, cfg *config.Config, outputPath string) error {
	now := nowFunc()

	report := JSONReport{
		Version:     "1.0",
		GeneratedAt: results.GeneratedAt.Format(time.RFC3339),
		Config: JSONConfig{
			ThresholdDays: cfg.ThresholdDays,
			ContentDir:    cfg.ContentDir,
			StalenessLevels: JSONStaleness{
				Warning:  cfg.StalenessLevels.Warning,
				Caution:  cfg.StalenessLevels.Caution,
				Critical: cfg.StalenessLevels.Critical,
			},
		},
		Summary: JSONSummary{
			TotalFiles:          results.TotalFiles(),
			StaleFiles:          results.StaleFiles(),
			StaleFilesPct:       results.StaleFilesPct(),
			TotalSections:       results.TotalSections(),
			StaleSections:       results.StaleSections(),
			StaleSectionsPct:    results.StaleSectionsPct(),
			FilesMissingHistory: results.FilesMissingHistory(),
		},
	}

	if oldest := results.OldestFile(); oldest != nil {
		report.Summary.OldestFile = oldest.RelativePath
		report.Summary.OldestDays = oldest.OldestSectionDays
	}

	// Process files
	for _, f := range results.Files {
		jf := JSONFile{
			Path:          f.RelativePath,
			DaysStale:     f.DaysStale,
			TotalSections: len(f.Sections),
			StaleSections: len(f.StaleSections),
		}

		if f.EffectiveLastUpdated != nil {
			jf.LastUpdated = f.EffectiveLastUpdated.Format(time.RFC3339)
		}

		// Include stale sections
		for _, s := range f.StaleSections {
			js := JSONSection{
				Title:     s.Title,
				StartLine: s.StartLine,
				EndLine:   s.EndLine,
				Author:    s.LastAuthor(),
			}

			if lastUpdated := s.LastUpdated(); lastUpdated != nil {
				js.LastUpdated = lastUpdated.Format(time.RFC3339)
				js.DaysStale = int(now.Sub(*lastUpdated).Hours() / 24)
				js.Level = cfg.GetStalenessClass(js.DaysStale)
			} else {
				// No resolvable date: render consistently as "unknown" across all
				// formats rather than fabricating a day count or severity. See #56.
				js.Level = "unknown"
			}

			jf.Sections = append(jf.Sections, js)
		}

		report.Files = append(report.Files, jf)
	}

	// Process reusables
	for _, r := range results.AllReusables {
		jr := JSONReusable{
			Name:   r.Name,
			Author: r.LastAuthor,
		}

		if r.LastUpdated != nil {
			jr.LastUpdated = r.LastUpdated.Format(time.RFC3339)
			jr.DaysStale = int(now.Sub(*r.LastUpdated).Hours() / 24)
			jr.Level = cfg.GetStalenessClass(jr.DaysStale)
		} else {
			jr.Level = "unknown"
		}

		report.Reusables = append(report.Reusables, jr)
	}

	// Write to file
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Clean(outputPath), data, 0600)
}

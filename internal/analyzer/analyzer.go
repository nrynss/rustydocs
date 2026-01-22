// Package analyzer provides the main analysis orchestrator for rustydocs.
package analyzer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/git"
	"github.com/nrynss/rustydocs/internal/parser"
)

// binaryExtensions contains file extensions to skip (images, media, archives, etc.)
var binaryExtensions = map[string]struct{}{
	// Images
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".svg": {}, ".ico": {}, ".webp": {}, ".bmp": {}, ".tiff": {},
	// Media
	".mp3": {}, ".mp4": {}, ".wav": {}, ".avi": {}, ".mov": {}, ".webm": {}, ".ogg": {},
	// Archives
	".zip": {}, ".tar": {}, ".gz": {}, ".bz2": {}, ".7z": {}, ".rar": {},
	// Documents (non-text)
	".pdf": {}, ".doc": {}, ".docx": {}, ".xls": {}, ".xlsx": {}, ".ppt": {}, ".pptx": {},
	// Fonts
	".woff": {}, ".woff2": {}, ".ttf": {}, ".otf": {}, ".eot": {},
	// Other binary
	".exe": {}, ".dll": {}, ".so": {}, ".dylib": {}, ".bin": {}, ".dat": {},
}

// isBinaryFile returns true if the file should be skipped based on extension.
func isBinaryFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, isBinary := binaryExtensions[ext]
	return isBinary
}

// ReusableInfo contains information about a reusable component.
type ReusableInfo struct {
	Name        string
	LastUpdated *time.Time
	IsFresh     bool
	LastAuthor  string
}

// FileAnalysis contains analysis results for a single file.
type FileAnalysis struct {
	Path                 string
	RelativePath         string
	FileInfo             *git.FileInfo
	Sections             []parser.Section
	StaleSections        []parser.Section
	Reusables            []ReusableInfo
	EffectiveLastUpdated *time.Time
	OldestSectionDate    *time.Time
	DaysStale            int
	OldestSectionDays    int
}

// IsStale returns true if the file has any stale content.
func (f *FileAnalysis) IsStale() bool {
	return len(f.StaleSections) > 0
}

// Results contains complete analysis results.
type Results struct {
	Files        []FileAnalysis
	AllReusables []ReusableInfo
	Config       *config.Config
	GeneratedAt  time.Time
}

// TotalFiles returns the total number of files analyzed.
func (r *Results) TotalFiles() int {
	return len(r.Files)
}

// StaleFiles returns the number of files with stale content.
func (r *Results) StaleFiles() int {
	count := 0
	for _, f := range r.Files {
		if f.IsStale() {
			count++
		}
	}
	return count
}

// StaleFilesPct returns the percentage of files with stale content.
func (r *Results) StaleFilesPct() float64 {
	if r.TotalFiles() == 0 {
		return 0
	}
	return float64(r.StaleFiles()) / float64(r.TotalFiles()) * 100
}

// TotalSections returns the total number of sections analyzed.
func (r *Results) TotalSections() int {
	count := 0
	for _, f := range r.Files {
		count += len(f.Sections)
	}
	return count
}

// StaleSections returns the number of stale sections.
func (r *Results) StaleSections() int {
	count := 0
	for _, f := range r.Files {
		count += len(f.StaleSections)
	}
	return count
}

// StaleSectionsPct returns the percentage of stale sections.
func (r *Results) StaleSectionsPct() float64 {
	if r.TotalSections() == 0 {
		return 0
	}
	return float64(r.StaleSections()) / float64(r.TotalSections()) * 100
}

// OldestFile returns the file with the oldest content (based on oldest section).
func (r *Results) OldestFile() *FileAnalysis {
	var oldest *FileAnalysis
	for i := range r.Files {
		if r.Files[i].IsStale() {
			if oldest == nil || r.Files[i].OldestSectionDays > oldest.OldestSectionDays {
				oldest = &r.Files[i]
			}
		}
	}
	return oldest
}

func shouldExclude(path string, cfg *config.Config, baseDir string) bool {
	relative, err := filepath.Rel(baseDir, path)
	if err != nil {
		relative = path
	}

	// Check exclude patterns
	for _, pattern := range cfg.ExcludePatterns {
		matched, err := filepath.Match(pattern, relative)
		if err == nil && matched {
			return true
		}
		// Also check if pattern matches any part of the path
		if strings.Contains(relative, strings.TrimSuffix(pattern, "/*")) {
			return true
		}
	}

	// Check exclude directories
	// Normalize path separators for cross-platform compatibility
	normalizedRelative := filepath.ToSlash(relative)
	for _, dir := range cfg.ExcludeDirs {
		if strings.HasPrefix(normalizedRelative, dir+"/") || normalizedRelative == dir {
			return true
		}
		// Also match directory anywhere in path
		if strings.Contains(normalizedRelative, "/"+dir+"/") {
			return true
		}
	}

	return false
}

func analyzeFile(filePath string, cfg *config.Config, baseDir string) FileAnalysis {
	now := time.Now()
	thresholdDate := now.Add(-time.Duration(cfg.ThresholdDays) * 24 * time.Hour)

	// Get file-level info
	fileInfo, _ := git.GetFileLastModified(filePath)

	// Read file content
	content, err := os.ReadFile(filepath.Clean(filePath))
	if err != nil {
		return FileAnalysis{Path: filePath}
	}

	// Get relative path for display
	relativePath, err := filepath.Rel(baseDir, filePath)
	if err != nil {
		relativePath = filePath
	}

	// Create reusable patterns from config
	reusablesDir := cfg.Reusables.Dir
	if reusablesDir == "" {
		reusablesDir = cfg.ReusablesDir // backward compatibility
	}
	if reusablesDir != "" && !filepath.IsAbs(reusablesDir) {
		if cwd, err := os.Getwd(); err == nil {
			reusablesDir = filepath.Join(cwd, reusablesDir)
		}
		// If Getwd fails, leave reusablesDir as relative (will likely fail later but won't crash)
	}

	// Determine Hugo root (for shortcode tracing)
	hugoRoot := cfg.HugoRoot
	if hugoRoot == "" {
		hugoRoot = config.DetectHugoRoot(baseDir)
	}
	if hugoRoot != "" && !filepath.IsAbs(hugoRoot) {
		if cwd, err := os.Getwd(); err == nil {
			hugoRoot = filepath.Join(cwd, hugoRoot)
		}
		// If Getwd fails, leave hugoRoot as relative
	}

	rp, err := parser.NewReusablePatterns(cfg.Reusables.Patterns, cfg.Reusables.Extensions, reusablesDir, hugoRoot)
	if err != nil {
		// Invalid pattern in config - fall back to default patterns
		rp = parser.DefaultReusablePatterns()
	}

	var sections []parser.Section
	var linesInfo []git.LineInfo

	if !cfg.FileLevelOnly {
		var blameErr error
		linesInfo, blameErr = git.GetBlameInfo(filePath)
		if blameErr != nil {
			// Git blame failed - file may not be tracked, or git error occurred
			// Continue with empty linesInfo, sections will have no line-level timestamps
			linesInfo = nil
		}
		if cfg.ParagraphLevel {
			sections = parser.ParseChunks(string(content), linesInfo, true, rp)
		} else {
			sections = parser.ParseSections(string(content), linesInfo, rp)
		}
	}

	// Analyze each section for staleness
	var staleSections []parser.Section
	allReusables := make(map[string]ReusableInfo)
	var oldestSectionDate *time.Time

	for _, section := range sections {
		// Calculate effective staleness considering reusables
		effectiveDate := parser.CalculateSectionStaleness(&section, rp)

		if effectiveDate != nil && effectiveDate.Before(thresholdDate) {
			staleSections = append(staleSections, section)
		}

		// Track the oldest section date (for sorting)
		if effectiveDate != nil {
			if oldestSectionDate == nil || effectiveDate.Before(*oldestSectionDate) {
				oldestSectionDate = effectiveDate
			}
		}

		// Track reusables
		for _, reusableName := range section.Reusables {
			if _, exists := allReusables[reusableName]; !exists {
				reusableInfo := parser.GetReusableInfo(reusableName, rp)
				var lastUpdated *time.Time
				var lastAuthor string
				if reusableInfo != nil {
					lastUpdated = &reusableInfo.LastModified
					lastAuthor = reusableInfo.LastAuthor
				}
				isFresh := lastUpdated != nil && !lastUpdated.Before(thresholdDate)
				allReusables[reusableName] = ReusableInfo{
					Name:        reusableName,
					LastUpdated: lastUpdated,
					IsFresh:     isFresh,
					LastAuthor:  lastAuthor,
				}
			}
		}
	}

	// Calculate overall file staleness (most recent update)
	var effectiveLastUpdated *time.Time
	if fileInfo != nil {
		effectiveLastUpdated = &fileInfo.LastModified
	}

	// Also consider section dates for most recent
	for _, section := range sections {
		sectionDate := parser.CalculateSectionStaleness(&section, rp)
		if sectionDate != nil {
			if effectiveLastUpdated == nil || sectionDate.After(*effectiveLastUpdated) {
				effectiveLastUpdated = sectionDate
			}
		}
	}

	// Calculate days stale (based on most recent update)
	var daysStale int
	if effectiveLastUpdated != nil {
		daysStale = int(now.Sub(*effectiveLastUpdated).Hours() / 24)
	}

	// Calculate oldest section days (for sorting - files with oldest content first)
	var oldestSectionDays int
	if oldestSectionDate != nil {
		oldestSectionDays = int(now.Sub(*oldestSectionDate).Hours() / 24)
	}

	// Convert reusables map to slice
	reusables := make([]ReusableInfo, 0, len(allReusables))
	for _, r := range allReusables {
		reusables = append(reusables, r)
	}

	return FileAnalysis{
		Path:                 filePath,
		RelativePath:         relativePath,
		FileInfo:             fileInfo,
		Sections:             sections,
		StaleSections:        staleSections,
		Reusables:            reusables,
		EffectiveLastUpdated: effectiveLastUpdated,
		OldestSectionDate:    oldestSectionDate,
		DaysStale:            daysStale,
		OldestSectionDays:    oldestSectionDays,
	}
}

// ProgressWriter is used to report analysis progress.
type ProgressWriter interface {
	io.Writer
}

// printProgress prints a progress bar to the writer.
func printProgress(w io.Writer, completed, total int) {
	if w == nil || total == 0 {
		return
	}

	pct := float64(completed) / float64(total) * 100
	barWidth := 30
	filled := int(float64(barWidth) * float64(completed) / float64(total))

	bar := strings.Repeat("=", filled)
	if filled < barWidth {
		bar += ">"
		bar += strings.Repeat(" ", barWidth-filled-1)
	}

	fmt.Fprintf(w, "\r[%s] %3.0f%% (%d/%d files)", bar, pct, completed, total)
}

// Analyze runs analysis on all markdown files in the content directory.
func Analyze(cfg *config.Config) (*Results, error) {
	return AnalyzeWithProgress(cfg, nil)
}

// AnalyzeWithProgress runs analysis with progress reporting.
func AnalyzeWithProgress(cfg *config.Config, progress ProgressWriter) (*Results, error) {
	if cfg.ContentDir == "" {
		return nil, os.ErrInvalid
	}

	baseDir := cfg.ContentDir
	if !filepath.IsAbs(baseDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		baseDir = filepath.Join(cwd, baseDir)
	}

	// Find all content files, skipping binary/non-text files
	var mdFiles []string
	err := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			if !isBinaryFile(path) && !shouldExclude(path, cfg, baseDir) {
				mdFiles = append(mdFiles, path)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Determine number of workers
	workers := cfg.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	// Analyze files in parallel
	analyses := make([]FileAnalysis, len(mdFiles))
	var wg sync.WaitGroup
	fileChan := make(chan int, len(mdFiles))
	var completed int64
	total := len(mdFiles)

	// Progress reporter
	done := make(chan struct{})
	if progress != nil {
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					printProgress(progress, int(atomic.LoadInt64(&completed)), total)
				case <-done:
					printProgress(progress, total, total)
					fmt.Fprintln(progress) // newline after progress bar
					return
				}
			}
		}()
	}

	// Start workers
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range fileChan {
				analyses[idx] = analyzeFile(mdFiles[idx], cfg, baseDir)
				atomic.AddInt64(&completed, 1)
			}
		}()
	}

	// Send files to workers
	for i := range mdFiles {
		fileChan <- i
	}
	close(fileChan)

	wg.Wait()

	// Stop progress reporter
	if progress != nil {
		close(done)
	}

	// Sort by relative path
	sort.Slice(analyses, func(i, j int) bool {
		return analyses[i].RelativePath < analyses[j].RelativePath
	})

	// Collect all unique reusables across all files
	reusableMap := make(map[string]ReusableInfo)
	for _, analysis := range analyses {
		for _, r := range analysis.Reusables {
			existing, exists := reusableMap[r.Name]
			if !exists || (existing.LastUpdated == nil && r.LastUpdated != nil) {
				reusableMap[r.Name] = r
			}
		}
	}

	// Convert to sorted slice
	allReusables := make([]ReusableInfo, 0, len(reusableMap))
	for _, r := range reusableMap {
		allReusables = append(allReusables, r)
	}
	sort.Slice(allReusables, func(i, j int) bool {
		return allReusables[i].Name < allReusables[j].Name
	})

	return &Results{
		Files:        analyses,
		AllReusables: allReusables,
		Config:       cfg,
		GeneratedAt:  time.Now(),
	}, nil
}

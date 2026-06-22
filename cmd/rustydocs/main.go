// Package main provides the CLI entry point for rustydocs.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/nrynss/rustydocs/internal/analyzer"
	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/report"
)

// Version info - set via ldflags at build time
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Define flags
	var (
		configPath     = flag.String("config", "", "Path to JSON config file")
		contentDir     = flag.String("content-dir", "", "Directory containing markdown files")
		reusablesDir   = flag.String("reusables-dir", "", "Directory containing reusable components")
		outputDir      = flag.String("output-dir", "", "Output directory for reports")
		thresholdDays  = flag.Int("threshold-days", 0, "Days before content is considered stale (default: 90)")
		fileLevelOnly  = flag.Bool("file-level-only", false, "Skip section-level analysis (faster)")
		paragraphLevel = flag.Bool("paragraph-level", false, "Analyze at paragraph level (more granular)")
		excludeDirs    = flag.String("exclude-dirs", "", "Comma-separated directories to exclude (e.g., releasenotes,images)")
		extensions     = flag.String("extensions", "", "Comma-separated documentation extensions to analyze (default: .md,.markdown,.mdx)")
		workers        = flag.Int("workers", 0, "Number of parallel workers (default: number of CPUs)")
		showVersion    = flag.Bool("version", false, "Show version and exit")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "rustydocs - Find stale documentation using git history\n\n")
		fmt.Fprintf(os.Stderr, "Usage: rustydocs [OPTIONS]\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if *showVersion {
		fmt.Printf("rustydocs %s\n", version)
		if commit != "none" {
			fmt.Printf("  commit: %s\n", commit)
		}
		if date != "unknown" {
			fmt.Printf("  built:  %s\n", date)
		}
		return nil
	}

	// Load config from file or create default
	var cfg *config.Config
	if *configPath != "" {
		var err error
		cfg, err = config.LoadConfig(*configPath)
		if err != nil {
			return fmt.Errorf("unable to load config: %w", err)
		}
	} else {
		cfg = config.DefaultConfig()
	}

	// Override config with CLI flags
	if *contentDir != "" {
		cfg.ContentDir = *contentDir
	}
	if *reusablesDir != "" {
		cfg.ReusablesDir = *reusablesDir
	}
	if *outputDir != "" {
		cfg.OutputDir = *outputDir
	}
	if *thresholdDays > 0 {
		cfg.ThresholdDays = *thresholdDays
	}
	if *fileLevelOnly {
		cfg.FileLevelOnly = true
	}
	if *paragraphLevel {
		cfg.ParagraphLevel = true
	}
	if *excludeDirs != "" {
		dirs := strings.Split(*excludeDirs, ",")
		for _, d := range dirs {
			d = strings.TrimSpace(d)
			if d != "" {
				cfg.ExcludeDirs = append(cfg.ExcludeDirs, d)
			}
		}
	}
	if *extensions != "" {
		var exts []string
		for _, e := range strings.Split(*extensions, ",") {
			if e = strings.TrimSpace(e); e != "" {
				exts = append(exts, e)
			}
		}
		if len(exts) > 0 {
			cfg.ContentExtensions = exts
		}
	}
	if *workers > 0 {
		cfg.Workers = *workers
	}

	// Validate config
	if cfg.ContentDir == "" {
		return fmt.Errorf("--content-dir is required")
	}

	// Check content directory exists
	if _, err := os.Stat(cfg.ContentDir); os.IsNotExist(err) {
		return fmt.Errorf("content directory does not exist: %s", cfg.ContentDir)
	}

	// Run analysis
	workerCount := cfg.Workers
	if workerCount <= 0 {
		workerCount = runtime.NumCPU()
	}
	fmt.Printf("Analyzing documentation in: %s\n", cfg.ContentDir)
	fmt.Printf("Threshold: %d days | Workers: %d\n\n", cfg.ThresholdDays, workerCount)

	results, err := analyzer.AnalyzeWithProgress(cfg, os.Stdout)
	if err != nil {
		return fmt.Errorf("unable to analyze: %w", err)
	}

	// Generate reports
	outputPath := cfg.OutputDir
	if err := os.MkdirAll(filepath.Clean(outputPath), 0750); err != nil {
		return fmt.Errorf("unable to create output directory: %w", err)
	}

	mdPath := filepath.Join(outputPath, "stale-docs.md")
	htmlPath := filepath.Join(outputPath, "stale-docs.html")
	jsonPath := filepath.Join(outputPath, "stale-docs.json")

	if err := report.GenerateMarkdown(results, cfg, mdPath); err != nil {
		return fmt.Errorf("unable to generate markdown report: %w", err)
	}

	if err := report.GenerateHTML(results, cfg, htmlPath); err != nil {
		return fmt.Errorf("unable to generate HTML report: %w", err)
	}

	if err := report.GenerateJSON(results, cfg, jsonPath); err != nil {
		return fmt.Errorf("unable to generate JSON report: %w", err)
	}

	fmt.Printf("\nReports generated:\n")
	fmt.Printf("  Markdown: %s\n", mdPath)
	fmt.Printf("  HTML:     %s\n", htmlPath)
	fmt.Printf("  JSON:     %s\n", jsonPath)

	// Print summary
	fmt.Printf("\nSummary:\n")
	fmt.Printf("  Files scanned: %d\n", results.TotalFiles())
	fmt.Printf("  Files with stale content: %d (%.1f%%)\n", results.StaleFiles(), results.StaleFilesPct())
	fmt.Printf("  Sections analyzed: %d\n", results.TotalSections())
	fmt.Printf("  Stale sections: %d (%.1f%%)\n", results.StaleSections(), results.StaleSectionsPct())

	return nil
}

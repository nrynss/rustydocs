// Package main provides the CLI entry point for rustydocs.
package main

import (
	"flag"
	"fmt"
	"io"
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

// run wires up the real process I/O and delegates to runArgs.
func run() error {
	return runArgs(os.Args[1:], os.Stdout, os.Stderr)
}

// runArgs parses argv and runs the full pipeline, writing normal output to
// stdout and diagnostics to stderr. It uses its own FlagSet (rather than the
// global flag.CommandLine) so it is reentrant and can be driven from tests.
func runArgs(argv []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("rustydocs", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		configPath     = fs.String("config", "", "Path to JSON config file")
		contentDir     = fs.String("content-dir", "", "Directory containing markdown files")
		reusablesDir   = fs.String("reusables-dir", "", "Directory containing reusable components")
		outputDir      = fs.String("output-dir", "", "Output directory for reports")
		thresholdDays  = fs.Int("threshold-days", 0, "Days before content is considered stale (default: 90)")
		fileLevelOnly  = fs.Bool("file-level-only", false, "Skip section-level analysis (faster)")
		paragraphLevel = fs.Bool("paragraph-level", false, "Analyze at paragraph level (more granular)")
		excludeDirs    = fs.String("exclude-dirs", "", "Comma-separated directories to exclude (e.g., releasenotes,images)")
		extensions     = fs.String("extensions", "", "Comma-separated documentation extensions to analyze (default: .md,.markdown,.mdx)")
		workers        = fs.Int("workers", 0, "Number of parallel workers (default: number of CPUs)")
		showVersion    = fs.Bool("version", false, "Show version and exit")
	)

	fs.Usage = func() {
		// Usage output is best-effort; deliberately ignore stderr write errors
		// (as the flag package itself does) so errcheck stays quiet.
		_, _ = fmt.Fprint(stderr, "rustydocs - Find stale documentation using git history\n\n"+
			"Usage: rustydocs [OPTIONS]\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(argv); err != nil {
		return err
	}

	if *showVersion {
		fmt.Fprintf(stdout, "rustydocs %s\n", version)
		if commit != "none" {
			fmt.Fprintf(stdout, "  commit: %s\n", commit)
		}
		if date != "unknown" {
			fmt.Fprintf(stdout, "  built:  %s\n", date)
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

	// Reconcile the reporting threshold with the staleness tiers so a stale
	// section can never be classified "fresh" (see #54).
	cfg.Normalize()

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
	fmt.Fprintf(stdout, "Analyzing documentation in: %s\n", cfg.ContentDir)
	fmt.Fprintf(stdout, "Threshold: %d days | Workers: %d\n\n", cfg.ThresholdDays, workerCount)

	results, err := analyzer.AnalyzeWithProgress(cfg, stdout)
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

	fmt.Fprintf(stdout, "\nReports generated:\n")
	fmt.Fprintf(stdout, "  Markdown: %s\n", mdPath)
	fmt.Fprintf(stdout, "  HTML:     %s\n", htmlPath)
	fmt.Fprintf(stdout, "  JSON:     %s\n", jsonPath)

	// Print summary
	fmt.Fprintf(stdout, "\nSummary:\n")
	fmt.Fprintf(stdout, "  Files scanned: %d\n", results.TotalFiles())
	fmt.Fprintf(stdout, "  Files with stale content: %d (%.1f%%)\n", results.StaleFiles(), results.StaleFilesPct())
	fmt.Fprintf(stdout, "  Sections analyzed: %d\n", results.TotalSections())
	fmt.Fprintf(stdout, "  Stale sections: %d (%.1f%%)\n", results.StaleSections(), results.StaleSectionsPct())

	// Surface files we could not assess so a misconfigured (shallow or partly
	// uncommitted) checkout does not silently report as clean. See #55.
	if missing := results.FilesMissingHistory(); missing > 0 {
		fmt.Fprintf(stdout, "  Files with no git history (staleness unknown): %d\n", missing)
		fmt.Fprintf(stderr, "\nWarning: %d file(s) had no git history and could not be assessed "+
			"(uncommitted files, a shallow clone, or not a git repository); "+
			"they are reported as unknown, not fresh. Ensure a full clone (fetch-depth: 0).\n", missing)
	}

	return nil
}

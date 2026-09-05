// Package main provides the CLI entry point for rustydocs.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/nrynss/rustydocs/internal/analyzer"
	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/report"
)

// Defaults used to detect that an ldflags value was not supplied.
const (
	defaultVersion = "dev"
	defaultCommit  = "none"
	defaultDate    = "unknown"
)

// Version info - set via ldflags at build time (see Makefile / release
// workflow). Constant-expression initialisers are still overridable by
// `-ldflags "-X main.version=..."`. When a binary is produced without ldflags,
// version and commit keep their defaults and resolveBuildInfo fills them in
// from the Go build info embedded by the toolchain: the module version for
// `go install github.com/nrynss/rustydocs/cmd/rustydocs@latest` (module-proxy
// builds carry no VCS settings), plus the commit for builds made from a local
// git checkout (#39). date is only ever set via ldflags; see resolveBuildInfo
// for why vcs.time is deliberately not used.
var (
	version = defaultVersion
	commit  = defaultCommit
	date    = defaultDate
)

// resolveBuildInfo returns the version and commit to report, preferring the
// explicit ldflags values (v, c) and falling back to the embedded build info
// for any that are still at their defaults. It is a pure function so the
// fallback logic can be unit-tested regardless of how the test binary was
// built.
//
//   - version: info.Main.Version when it is non-empty and not "(devel)".
//   - commit:  the vcs.revision setting, shortened to 12 characters, with a
//     "-dirty" suffix when vcs.modified is "true".
//
// The build date is intentionally not derived from build info: vcs.time is the
// timestamp of the HEAD commit, not the time the binary was built, and
// --version prints the date under a "built:" label. Issue #39 only asks for the
// module version and VCS revision, so date stays at its ldflags value.
//
// ok mirrors the second return value of debug.ReadBuildInfo; when it is false
// (or info is nil) the inputs are returned unchanged.
func resolveBuildInfo(v, c string, info *debug.BuildInfo, ok bool) (resolvedVersion, resolvedCommit string) {
	resolvedVersion, resolvedCommit = v, c
	if !ok || info == nil {
		return resolvedVersion, resolvedCommit
	}

	if resolvedVersion == defaultVersion {
		if mv := info.Main.Version; mv != "" && mv != "(devel)" {
			resolvedVersion = mv
		}
	}

	var revision, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}

	if resolvedCommit == defaultCommit && revision != "" {
		if len(revision) > 12 {
			revision = revision[:12]
		}
		if modified == "true" {
			revision += "-dirty"
		}
		resolvedCommit = revision
	}

	return resolvedVersion, resolvedCommit
}

// buildInfo resolves the reportable version, commit and date for this binary:
// ldflags values win; a version or commit left at its default is filled from
// debug.ReadBuildInfo. The date is reported exactly as set via ldflags.
func buildInfo() (string, string, string) {
	info, ok := debug.ReadBuildInfo()
	v, c := resolveBuildInfo(version, commit, info, ok)
	return v, c, date
}

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
		extensions     = fs.String("extensions", "", "Comma-separated documentation extensions to analyze (default: from profile)")
		profile        = fs.String("profile", "", "Documentation profile: "+strings.Join(config.Profiles(), ", ")+" (default: auto-detect)")
		listProfiles   = fs.Bool("list-profiles", false, "List built-in profiles and exit")
		workers        = fs.Int("workers", 0, "Number of parallel workers (default: number of CPUs)")
		showVersion    = fs.Bool("version", false, "Show version and exit")
	)

	fs.Usage = func() {
		fmt.Fprint(stderr, "rustydocs - Find stale documentation using git history\n\n"+
			"Usage: rustydocs [OPTIONS]\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(argv); err != nil {
		return err
	}

	if *showVersion {
		v, c, d := buildInfo()
		fmt.Fprintf(stdout, "rustydocs %s\n", v)
		if c != defaultCommit {
			fmt.Fprintf(stdout, "  commit: %s\n", c)
		}
		if d != defaultDate {
			fmt.Fprintf(stdout, "  built:  %s\n", d)
		}
		return nil
	}

	if *listProfiles {
		printProfiles(stdout)
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
	if *profile != "" {
		cfg.Profile = *profile
	}

	// Resolve the documentation profile (explicit --profile / "profile", else
	// auto-detected from the content dir) and fill in the profile-dependent
	// defaults the user left unset (see #11).
	if err := cfg.ApplyProfile(); err != nil {
		return err
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
	fmt.Fprintf(stdout, "Profile: %s\n", describeProfile(cfg))
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

	// Zero files scanned almost always means the extension allowlist did not
	// match the tree (e.g. an .mdx-only site under the markdown profile) or the
	// exclusions removed every match, so say which profile and extensions were
	// in force — or that exclusions did it — instead of reporting a clean run
	// in silence. The exit code is unchanged (#11).
	if results.TotalFiles() == 0 {
		fmt.Fprintf(stderr, "\nWarning: %s\n", describeNoFilesMatched(cfg, results.FilesExcluded()))
		return nil
	}

	// Partially-scanned tree: some files are documentation under another
	// built-in profile but not under this run's allowlist (the classic case is
	// an .md/.mdx tree with no Hugo marker, which the markdown profile scans
	// only half of). This is a note, not an error — the exit code is unchanged
	// (#11). It is deliberately suppressed when nothing was scanned at all:
	// the zero-files warning above already names the profile, the extensions
	// in force and the two knobs that widen them, so printing both would be
	// two overlapping messages about one cause.
	if skipped := results.FilesSkippedByExtension(); skipped > 0 {
		fmt.Fprintf(stderr, "\nNote: %s\n",
			describeSkippedExtensions(cfg, skipped, results.SkippedExtensions()))
	}

	return nil
}

// describeActiveExtensions renders the extension allowlist that was in force
// together with where it came from, plus a note naming the resolved profile
// when the list is not the profile's own. Both stderr messages below build on
// it so they stay phrased the same way.
//
// Three cases, because the active list and the resolved profile's list can
// disagree without the user having asked for it:
//
//	the "markdown" profile's extensions (.md, .markdown)   — the profile's own
//	the configured extensions (.rst)      + (profile "markdown")   — an override
//	the active extensions (.md, .markdown, .mdx) + (profile "markdown")
//
// The third case is the legacy reusables-dir flow: ApplyProfile widens the
// allowlist with the hugo profile's extensions while leaving ExtensionsFromUser
// false, so the list genuinely belongs to neither the user nor the resolved
// profile and must not be attributed to either (#11).
func describeActiveExtensions(cfg *config.Config) (phrase, profileNote string) {
	active := strings.Join(cfg.ContentExtensions, ", ")
	switch {
	case cfg.ExtensionsFromUser:
		phrase = fmt.Sprintf("the configured extensions (%s)", active)
	case !slices.Equal(cfg.ContentExtensions, cfg.ResolvedProfile.ContentExtensions):
		phrase = fmt.Sprintf("the active extensions (%s)", active)
	default:
		return fmt.Sprintf("the %q profile's extensions (%s)", cfg.ResolvedProfile.Name, active), ""
	}
	return phrase, fmt.Sprintf(" (profile %q)", cfg.ResolvedProfile.Name)
}

// describeSkippedExtensions builds the partial-scan note: how many files were
// skipped, which extensions they had, which allowlist skipped them, and how to
// include them. See the call site for why it never fires alongside the
// zero-files warning.
func describeSkippedExtensions(cfg *config.Config, skipped int, exts []string) string {
	active, profileNote := describeActiveExtensions(cfg)
	return fmt.Sprintf("%d file(s) with extension(s) %s were not analyzed: only %s are scanned%s. "+
		"Pass --extensions to widen the allowlist or --profile to pick another profile "+
		"(see --list-profiles).",
		skipped, strings.Join(exts, ", "), active, profileNote)
}

// describeNoFilesMatched builds the zero-files-scanned warning, naming the
// extensions that were in force and where they came from (see
// describeActiveExtensions). When excluded > 0 every file that matched those
// extensions was dropped by exclude_dirs / exclude_patterns, so the warning
// blames the exclusions; otherwise it points at the two knobs that change the
// extensions.
func describeNoFilesMatched(cfg *config.Config, excluded int) string {
	active, profileNote := describeActiveExtensions(cfg)
	if excluded > 0 {
		return fmt.Sprintf("all %d file(s) matching %s under %s%s were skipped by "+
			"exclude_dirs / exclude_patterns; relax the exclusions to analyze them.",
			excluded, active, cfg.ContentDir, profileNote)
	}
	return fmt.Sprintf("no files matched %s under %s%s; "+
		"use --extensions to widen the allowlist or --profile to pick another profile (see --list-profiles).",
		active, cfg.ContentDir, profileNote)
}

// describeProfile renders the resolved profile for the banner, e.g.
// "markdown (auto-detected)" or "hugo (auto-detected, root: /site)".
func describeProfile(cfg *config.Config) string {
	var notes []string
	if cfg.ProfileAuto {
		notes = append(notes, "auto-detected")
	}
	if cfg.HugoRoot != "" && len(cfg.ResolvedProfile.RootMarkers) > 0 {
		notes = append(notes, "root: "+cfg.HugoRoot)
	}
	if len(notes) == 0 {
		return cfg.ResolvedProfile.Name
	}
	return fmt.Sprintf("%s (%s)", cfg.ResolvedProfile.Name, strings.Join(notes, ", "))
}

// printProfiles writes the built-in profiles (name + description) for
// --list-profiles.
func printProfiles(w io.Writer) {
	fmt.Fprintln(w, "Built-in profiles (used with --profile; auto-detected when not set):")
	for _, p := range config.AllProfiles() {
		fmt.Fprintf(w, "  %-10s %s\n", p.Name, p.Description)
	}
}

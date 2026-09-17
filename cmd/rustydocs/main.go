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
		excludeDirs    = fs.String("exclude-dirs", "", "Comma-separated directories to exclude (e.g., releasenotes,images); "+
			"additive on top of the default exclusions")
		noDefaultExcludes = fs.Bool("no-default-excludes", false, "Scan everything: turn off the default exclusions "+
			"(dot-directories, "+strings.Join(config.DefaultExcludeDirNames(), "/")+", directories holding their own .git, "+
			"and files git ignores). --exclude-dirs / --exclude-patterns still apply. "+
			"Config-file spelling: \"no_default_excludes\"")
		extensions  = fs.String("extensions", "", "Comma-separated documentation extensions to analyze (default: from profile)")
		profile     = fs.String("profile", "", "Documentation profile: "+strings.Join(config.Profiles(), ", ")+" (default: auto-detect)")
		projectRoot = fs.String("project-root", "", "Project root that reusable references resolve against "+
			"(Hugo site root, Mintlify docs root); default: detected from the profile's markers. "+
			"It never selects a profile on its own, so pair it with --profile on a project whose "+
			"markers are absent. Config-file spelling: \"project_root\" (deprecated: \"hugo_root\")")
		listProfiles = fs.Bool("list-profiles", false, "List built-in profiles and exit")
		workers      = fs.Int("workers", 0, "Number of parallel workers (default: number of CPUs)")
		showVersion  = fs.Bool("version", false, "Show version and exit")
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
	if *noDefaultExcludes {
		cfg.NoDefaultExcludes = true
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
	if *projectRoot != "" {
		cfg.ProjectRoot = *projectRoot
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

	// Non-fatal diagnostics from profile resolution (an unreadable candidate
	// root marker, which silently falls through to another profile). The
	// config package never prints, so they surface here (#7).
	for _, w := range cfg.Warnings {
		fmt.Fprintf(stderr, "Warning: %s\n", w)
	}

	// A root the user supplied that the resolved profile never reads. Printed
	// here, beside the other configuration diagnostics, because it is about
	// the run's setup rather than its findings (#7).
	if note := describeUnusedProjectRoot(cfg); note != "" {
		fmt.Fprintf(stderr, "Note: %s\n", note)
	}

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

	// What the default exclusions removed. Printed before the zero-files
	// warning rather than after it — unlike the skipped-extensions note, which
	// is suppressed there — because when a run scans nothing the exclusions are
	// a likelier cause than the allowlist, and the reader needs to see both
	// (#69).
	if note := describeDefaultExcludes(results.DirsSkipped(), results.SkippedDirNames(),
		results.FilesGitIgnored()); note != "" {
		fmt.Fprintf(stderr, "\nNote: %s\n", note)
	}

	// Zero files scanned almost always means the extension allowlist did not
	// match the tree (e.g. an .mdx-only site under the markdown profile) or the
	// exclusions removed every match, so say which profile and extensions were
	// in force — or that exclusions did it — instead of reporting a clean run
	// in silence. The exit code is unchanged (#11).
	if results.TotalFiles() == 0 {
		fmt.Fprintf(stderr, "\nWarning: %s\n", describeNoFilesMatched(cfg,
			results.FilesExcluded(), results.DirsExcluded(), results.FilesGitIgnored(),
			results.DirsSkipped()))
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

	// Some reusable references produced no resolved history and were reported
	// unknown. Silence there looks like a clean run, so say how many, name the
	// first few, and — if the cause was a project root that was never found —
	// how to fix it (#7).
	if note := describeUnresolvedReusables(cfg,
		results.UnresolvedReusables(), results.UnresolvedReusableRefs()); note != "" {
		fmt.Fprintf(stderr, "\nNote: %s\n", note)
	}

	return nil
}

// describeUnresolvedReusables returns the stderr note for a run in which some
// reusable references produced no resolved history and were therefore reported
// *unknown*. unresolved is the count of those references
// (analyzer.Results.UnresolvedReusables) and refs the distinct captures behind
// it (analyzer.Results.UnresolvedReusableRefs); "" is returned when nothing
// failed, or when the resolved profile is not one the note can speak for.
//
// It is scoped to config.ResolverPath — Mintlify today. Under that resolver
// the capture *is* a file path, so every unresolved reference is a genuine
// defect the reader can act on. Under the hugo resolver it is not: the
// profile's second pattern captures every capitalised JSX/HTML tag on the
// page, and <Tabs>, <Card>, <Badge> and friends are simply not shortcodes, so
// on a real MDX site the note's population was overwhelmingly noise (measured
// at 1 actionable capture in 8). Those rows still appear in the report as
// level "unknown", which is where a Hugo user should look; what is removed is
// a stderr note that cried wolf on every run (#7 review pass 4).
//
// Within that scope the note is driven by the count, not by whether a project
// root was found. Gating it on a missing root (as it once was) made the far
// more common failure invisible: a Mintlify project whose root is right there
// next to docs.json, where the snippet paths simply did not resolve, exited 0
// with an empty stderr and a report full of "unknown". A root that was never
// found is only the most diagnosable *cause*, so it adds a sentence rather
// than deciding whether anything is said at all.
//
// The wording covers both ways a reference lands here, because the analyzer
// cannot tell them apart for every resolver and the reader has to check both:
// the file is missing (a typo, a moved snippet), or it exists and resolves
// fine but has never been committed, so git offers no date for it. The note
// used to claim only the first, which was wrong for an uncommitted snippet
// that the report meanwhile listed under its correct resolved path (#7 review
// pass 4).
//
// The exit code is unchanged either way: unresolved includes are a
// data-quality note, not a failure.
func describeUnresolvedReusables(cfg *config.Config, unresolved int, refs []string) string {
	if unresolved <= 0 || cfg.ResolvedProfile.Resolver != config.ResolverPath {
		return ""
	}
	var b strings.Builder
	// The count is per file per capture; refs is deduplicated across the run, so
	// the two differ whenever one broken include is referenced by several pages.
	// Say so, rather than letting a reader hunt for a name that was collapsed.
	distinct := ""
	if len(refs) > 0 && len(refs) != unresolved {
		distinct = fmt.Sprintf(" (%d distinct)", len(refs))
	}
	fmt.Fprintf(&b, "profile %q: %d reusable reference(s)%s resolved to no file with git history "+
		"(the file is missing, or it exists but has never been committed) "+
		"and are reported as unknown, never as fresh%s",
		cfg.ResolvedProfile.Name, unresolved, distinct, describeUnresolvedRefs(refs))

	if cfg.ProjectRoot != "" || len(cfg.ResolvedProfile.RootMarkers) == 0 {
		return b.String()
	}

	// No project root: for a resolver that works relative to one, that alone
	// explains every failure, and naming the markers that were looked for is
	// what turns the note into something actionable.
	fmt.Fprintf(&b, " No project root was found — no %s at or above %s (the search stops at "+
		"the enclosing git repository) — so snippet path references "+
		"(e.g. <Snippet file=\"aws-config.mdx\" />) had nothing to resolve against. "+
		"Pass --project-root PATH (or set \"project_root\" in the config file) to point at it.",
		joinOr(cfg.ResolvedProfile.RootMarkers), cfg.ContentDir)
	return b.String()
}

// joinOr renders a list of alternatives the way prose wants them ("a, b or c")
// rather than as a bare comma-joined list, which reads as if it were truncated.
func joinOr(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// unresolvedRefsShown caps how many captures describeUnresolvedRefs names
// before it summarises the rest. Three is enough to recognise the pattern
// ("they are all under /snippets/") without turning one stderr line into a
// wall of text on a project that broke a shared include.
const unresolvedRefsShown = 3

// describeUnresolvedRefs renders the tail of the unresolved-reusables note:
// the captures themselves, truncated, and always ending the sentence. Callers
// concatenate it directly, so it returns "." when there is nothing to name.
func describeUnresolvedRefs(refs []string) string {
	if len(refs) == 0 {
		return "."
	}
	shown, more := refs, ""
	if len(refs) > unresolvedRefsShown {
		shown = refs[:unresolvedRefsShown]
		more = fmt.Sprintf(" and %d more", len(refs)-unresolvedRefsShown)
	}
	return fmt.Sprintf(": %s%s.", strings.Join(shown, ", "), more)
}

// describeUnusedProjectRoot returns the stderr note for a run where the user
// supplied a project root that the resolved profile has no use for: it has no
// root markers, or it resolves no reusable references at all. The root is
// still validated (a path that does not exist is an error), and then simply
// never read — and describeProfile deliberately keeps it out of the banner in
// exactly that case, so without this note the run says nothing whatsoever
// about it.
//
// This is the likeliest migration error the project_root rename creates. A
// markerless Hugo site whose config said "hugo_root" used to get the hugo
// profile — that key selected it — and with it shortcode tracing. The same
// site migrated to "project_root" / --project-root gets "markdown" instead,
// because the current spelling never selects a profile, and the .md files
// still match, so the run looks entirely healthy while every shortcode has
// quietly stopped being traced. Naming --profile is the fix (#7 review
// pass 4).
func describeUnusedProjectRoot(cfg *config.Config) string {
	if !cfg.RootFromUser {
		return ""
	}
	p := cfg.ResolvedProfile
	if len(p.RootMarkers) > 0 && p.Resolver != config.ResolverNone {
		return ""
	}
	return fmt.Sprintf("the project root %q is not used by the %q profile, which resolves no "+
		"reusable references against one, so it was ignored. A root never selects a profile: "+
		"pass --profile to name the one this project uses (see --list-profiles).",
		cfg.ProjectRoot, p.Name)
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

// skippedDirNamesShown caps how many directory names describeDefaultExcludes
// lists before it summarises the rest, for the same reason
// unresolvedRefsShown does: a repo full of nested checkouts must not turn one
// stderr line into a wall of text.
const skippedDirNamesShown = 5

// describeDefaultExcludes builds the note naming what the default exclusions
// removed from the walk: how many directories were pruned and which names they
// had, and how many files git ignores. "" when they removed nothing — including
// every run under --no-default-excludes, which can only produce zeroes.
//
// It exists because the defaults change the headline number. A user who saw
// 4,032 files yesterday and 494 today is owed an explanation on the same
// stderr, in the same shape, as the skipped-extensions note; and the way back
// is one flag, so the note names it (#69).
func describeDefaultExcludes(dirs int, names []string, ignored int) string {
	if dirs == 0 && ignored == 0 {
		return ""
	}
	var parts []string
	if dirs > 0 {
		parts = append(parts, fmt.Sprintf("%d director(y/ies) %s",
			dirs, describeSkippedDirNames(names)))
	}
	if ignored > 0 {
		parts = append(parts, fmt.Sprintf("%d file(s) ignored by git", ignored))
	}
	return fmt.Sprintf("default exclusions skipped %s. "+
		"Dot-directories, %s, nested standalone repositories (a clone or a linked worktree, "+
		"whose history is a different project's; a submodule is not one and is scanned) and "+
		"git-ignored files are excluded by default; pass --no-default-excludes to scan them "+
		"anyway.",
		strings.Join(parts, " and "), strings.Join(config.DefaultExcludeDirNames(), ", "))
}

// describeSkippedDirNames renders the pruned directories' distinct base names,
// truncated. Callers embed it mid-sentence, so it never ends one.
func describeSkippedDirNames(names []string) string {
	if len(names) == 0 {
		return ""
	}
	shown, more := names, ""
	if len(names) > skippedDirNamesShown {
		shown = names[:skippedDirNamesShown]
		more = fmt.Sprintf(" and %d more", len(names)-skippedDirNamesShown)
	}
	return fmt.Sprintf("(%s%s)", strings.Join(shown, ", "), more)
}

// describeNoFilesMatched builds the zero-files-scanned warning, naming the
// extensions that were in force and where they came from (see
// describeActiveExtensions). It has to say which of three things emptied the
// list, because only the last of them is fixed by changing the extensions:
//
//   - excluded > 0: every match was dropped by exclude_dirs / exclude_patterns;
//   - dirsExcluded > 0: exclude_dirs pruned whole subtrees, so the matches were
//     never counted as files at all (see analyzer.Results.DirsExcluded);
//   - gitIgnored > 0: the matches exist but git ignores them. Claiming "no
//     files matched the extensions" there was simply untrue — the extensions
//     matched fine. The note printed just above this one already gives the
//     detail, so this stays one clause (#69 review).
//   - dirsSkipped > 0: the *default* exclusions pruned every directory that
//     could have held content — a docs tree that lives entirely under, say,
//     node_modules or a nested clone. Nothing was ever visited, so all the
//     other counters are zero and the plain "no files matched the extensions"
//     text both contradicted the note printed above it and recommended the one
//     knob that cannot help. It defers to that note rather than repeating its
//     detail, and names the flag that restores the subtrees (PR #71 review).
//
// With none of those, the allowlist really did match nothing, and the warning
// points at the two knobs that widen it.
func describeNoFilesMatched(cfg *config.Config, excluded, dirsExcluded, gitIgnored, dirsSkipped int) string {
	active, profileNote := describeActiveExtensions(cfg)
	switch {
	case excluded > 0:
		return fmt.Sprintf("all %d file(s) matching %s under %s%s were skipped by "+
			"exclude_dirs / exclude_patterns; relax the exclusions to analyze them.",
			excluded, active, cfg.ContentDir, profileNote)
	case dirsExcluded > 0:
		return fmt.Sprintf("every directory holding %s under %s%s was pruned by "+
			"exclude_dirs (%d pruned); relax the exclusions to analyze them.",
			active, cfg.ContentDir, profileNote, dirsExcluded)
	case gitIgnored > 0:
		return fmt.Sprintf("all %d file(s) matching %s under %s%s are ignored by git, "+
			"so none was analyzed; untrack the ignore rule, or pass --no-default-excludes "+
			"to analyze them anyway.",
			gitIgnored, active, cfg.ContentDir, profileNote)
	case dirsSkipped > 0:
		return fmt.Sprintf("nothing was scanned under %s%s: the default exclusions pruned "+
			"every directory that could hold %s (see the note above); "+
			"pass --no-default-excludes to scan them anyway.",
			cfg.ContentDir, profileNote, active)
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
	if cfg.ProjectRoot != "" && len(cfg.ResolvedProfile.RootMarkers) > 0 {
		notes = append(notes, "root: "+cfg.ProjectRoot)
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

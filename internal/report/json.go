// Package report provides portable single-run scan exports.
package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nrynss/rustydocs/internal/analyzer"
	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/parser"
)

// nowFunc is retained for callers constructing legacy in-memory results without
// a timestamp. Normal scans use Results.GeneratedAt exclusively in all formats.
var nowFunc = time.Now

// JSONReport is the complete, versioned single-run scan snapshot.
type JSONReport struct {
	Version      string                `json:"version"`
	GeneratedAt  string                `json:"generated_at"`
	Tool         JSONTool              `json:"tool"`
	Repositories []analyzer.Repository `json:"repositories"`
	Config       JSONConfig            `json:"config"`
	Coverage     JSONCoverage          `json:"coverage"`
	Summary      JSONSummary           `json:"summary"`
	Files        []JSONFile            `json:"files"`
	Reusables    []JSONReusable        `json:"reusables"`
	Diagnostics  []analyzer.Diagnostic `json:"diagnostics"`
}

// JSONTool contains known build metadata; unavailable fields serialize as null.
type JSONTool struct {
	Version       *string `json:"version"`
	BuildRevision *string `json:"build_revision"`
}

// JSONConfig records effective scan settings with portable directory locations.
type JSONConfig struct {
	ThresholdDays     int                    `json:"threshold_days"`
	ContentDir        analyzer.Location      `json:"content_dir"`
	ProjectRoot       *analyzer.Location     `json:"project_root"`
	Profile           string                 `json:"profile"`
	ProfileAuto       bool                   `json:"profile_auto"`
	ContentExtensions []string               `json:"content_extensions"`
	StalenessLevels   config.StalenessLevels `json:"staleness_levels"`
	ExcludePatterns   []string               `json:"exclude_patterns"`
	ExcludeDirs       []string               `json:"exclude_dirs"`
	NoDefaultExcludes bool                   `json:"no_default_excludes"`
	FileLevelOnly     bool                   `json:"file_level_only"`
	ParagraphLevel    bool                   `json:"paragraph_level"`
	Reusables         JSONReusableConfig     `json:"reusables"`
}

// JSONReusableConfig describes the resolver and capture rules used by the scan.
type JSONReusableConfig struct {
	Dir        *analyzer.Location `json:"dir"`
	Patterns   []string           `json:"patterns"`
	Extensions []string           `json:"extensions"`
	Resolver   config.Resolver    `json:"resolver"`
	ImportMap  bool               `json:"import_map"`
}

// JSONCoverage counts visited-file decisions and whole-directory pruning separately.
type JSONCoverage struct {
	AnalyzedFiles            int      `json:"analyzed_files"`
	FailedFiles              int      `json:"failed_files"`
	ExcludedFiles            int      `json:"excluded_files"`
	GitIgnoredFiles          int      `json:"git_ignored_files"`
	ExtensionSkippedFiles    int      `json:"extension_skipped_files"`
	DefaultPrunedDirectories int      `json:"default_pruned_directories"`
	ExcludedDirectories      int      `json:"excluded_directories"`
	SkippedExtensions        []string `json:"skipped_extensions"`
	PrunedDirectoryNames     []string `json:"pruned_directory_names"`
}

// JSONSummary reconciles section classifications and file inventory totals.
type JSONSummary struct {
	TotalFiles          int     `json:"total_files"`
	StaleFiles          int     `json:"stale_files"`
	StaleFilesPct       float64 `json:"stale_files_pct"`
	TotalSections       int     `json:"total_sections"`
	StaleSections       int     `json:"stale_sections"`
	StaleSectionsPct    float64 `json:"stale_sections_pct"`
	FreshSections       int     `json:"fresh_sections"`
	UnknownSections     int     `json:"unknown_sections"`
	FilesMissingHistory int     `json:"files_missing_history"`
	TotalReusables      int     `json:"total_reusables"`
}

// JSONFile groups sections and history status without scoring page age.
type JSONFile struct {
	ID             string        `json:"id"`
	RepositoryID   *string       `json:"repository_id"`
	Path           string        `json:"path"`
	ContentPath    string        `json:"content_path"`
	AnalysisStatus string        `json:"analysis_status"`
	HistoryStatus  string        `json:"history_status"`
	TotalSections  int           `json:"total_sections"`
	StaleSections  int           `json:"stale_sections"`
	Sections       []JSONSection `json:"sections"`
}

// JSONChange retains all authors and commits tied at a source change instant.
type JSONChange struct {
	Date    *time.Time `json:"date"`
	Authors []string   `json:"authors"`
	Commits []string   `json:"commits"`
}

// JSONSource identifies own content or a reusable that supplies effective freshness.
type JSONSource struct {
	Kind       string  `json:"kind"`
	ReusableID *string `json:"reusable_id"`
}

// JSONDependency retains an original reference occurrence and its supporting-file IDs.
type JSONDependency struct {
	Reference   string   `json:"reference"`
	Line        int      `json:"line"`
	Column      int      `json:"column"`
	Status      string   `json:"status"`
	ReusableIDs []string `json:"reusable_ids"`
}

// JSONSection separates owned content, effective evidence and dependency fingerprints.
type JSONSection struct {
	ID                    string           `json:"id"`
	Title                 string           `json:"title"`
	HeadingPath           []string         `json:"heading_path"`
	StartLine             int              `json:"start_line"`
	EndLine               int              `json:"end_line"`
	OwnLastChange         JSONChange       `json:"own_last_change"`
	EffectiveLastChange   JSONChange       `json:"effective_last_change"`
	AgeDays               *int             `json:"age_days"`
	IsStale               bool             `json:"is_stale"`
	Level                 string           `json:"severity"`
	ContentFingerprint    string           `json:"content_fingerprint"`
	DependencyFingerprint string           `json:"dependency_fingerprint"`
	FreshnessSources      []JSONSource     `json:"freshness_sources"`
	Dependencies          []JSONDependency `json:"dependencies"`
}

// JSONReusable is a deduplicated supporting file with nullable history evidence.
type JSONReusable struct {
	ID                 string     `json:"id"`
	RepositoryID       *string    `json:"repository_id"`
	Name               string     `json:"path"`
	LastChange         JSONChange `json:"last_change"`
	AgeDays            *int       `json:"age_days"`
	Level              string     `json:"severity"`
	ContentFingerprint string     `json:"content_fingerprint"`
	ResolutionStatus   string     `json:"resolution_status"`
	HistoryStatus      string     `json:"history_status"`
}

// analysisTime uses the captured scan instant, with a fallback for legacy in-memory callers.
func analysisTime(r *analyzer.Results) time.Time {
	if !r.GeneratedAt.IsZero() {
		return r.GeneratedAt
	}
	return nowFunc()
}

// nonnil ensures empty JSON collections are arrays rather than null.
func nonnil[T any](a []T) []T { return append([]T{}, a...) }

// stringPointer represents unavailable optional text as null in JSON.
func stringPointer(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// identity hashes portable logical values without line numbers or checkout directories.
func identity(kind string, values ...any) string {
	data, _ := json.Marshal(values)
	return kind + ":" + strings.TrimPrefix(parser.Fingerprint(string(data)), "sha256:")
}

// unique sorts and deduplicates evidence strings, omitting empty values.
func unique(a []string) []string {
	sort.Strings(a)
	out := []string{}
	for _, s := range a {
		if s != "" && (len(out) == 0 || out[len(out)-1] != s) {
			out = append(out, s)
		}
	}
	return out
}

// change normalizes dated Git evidence to UTC with deterministic authors and commits.
func change(date *time.Time, authors, commits []string) JSONChange {
	if date != nil {
		d := date.UTC()
		date = &d
	}
	return JSONChange{Date: date, Authors: unique(authors), Commits: unique(commits)}
}

// classification keeps threshold staleness separate from inclusive severity tiers.
func classification(date *time.Time, now time.Time, cfg *config.Config) (*int, bool, string) {
	if date == nil {
		return nil, false, "unknown"
	}
	days := int(now.Sub(*date).Hours() / 24)
	return &days, date.Before(now.Add(-time.Duration(cfg.ThresholdDays) * 24 * time.Hour)), cfg.GetStalenessClass(days)
}

// location uses captured portable paths with a fallback for manually constructed results.
func location(r *analyzer.Results, path, fallback string) analyzer.Location {
	if loc, ok := r.Locations[path]; ok {
		return loc
	}
	if fallback == "" {
		fallback = "."
	}
	return analyzer.Location{Path: filepath.ToSlash(fallback)}
}

// replaceWarningRoot replaces complete root prefixes without rewriting sibling
// paths or relative prose that happens to contain the same substring.
func replaceWarningRoot(message, root, portable string) string {
	for offset := 0; offset < len(message); {
		match := strings.Index(message[offset:], root)
		if match < 0 {
			break
		}
		start := offset + match
		end := start + len(root)
		before := start == 0 || strings.ContainsRune(" \t\n\"'(=:[]", rune(message[start-1]))
		after := end == len(message) || strings.ContainsRune("/\\ \t\n\"'):;,]", rune(message[end]))
		if !before || !after {
			offset = end
			continue
		}
		suffix := ""
		if end < len(message) && (message[end] == '/' || message[end] == '\\') {
			suffix = "/"
			end++
		}
		message = message[:start] + portable + suffix + message[end:]
		offset = start + len(portable) + len(suffix)
	}
	return message
}

// portableConfigWarning retains the actionable warning while replacing known
// roots with portable spelling and redacting unmatched absolute paths. Profile
// detection can report parent markers outside ContentDir, including when no
// repository locations are available to map them.
func portableConfigWarning(message string, results *analyzer.Results, cfg *config.Config) string {
	roots := map[string]string{}
	addRoot := func(path, portable string) {
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		abs = filepath.Clean(abs)
		// A filesystem root is not a safe substring replacement: every slash
		// in the warning could otherwise become a portable label. Marker and
		// quoted-path fallbacks below handle paths rooted here.
		if filepath.Dir(abs) != abs {
			roots[abs] = portable
		}
	}
	addRoot(cfg.ContentDir, "content")
	addRoot(cfg.ProjectRoot, "project-root")
	addRoot(cfg.Reusables.Dir, "reusables")
	addRoot(cfg.ReusablesDir, "reusables")
	for path, loc := range results.Locations {
		if loc.RepositoryID == nil {
			continue
		}
		root, err := filepath.Abs(path)
		if err != nil || path == "" {
			continue
		}
		for _, part := range strings.Split(filepath.ToSlash(loc.Path), "/") {
			if part != "" && part != "." {
				root = filepath.Dir(root)
			}
		}
		addRoot(root, ".")
	}
	ordered := make([]string, 0, len(roots))
	for root := range roots {
		ordered = append(ordered, root)
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, root := range ordered {
		portable := roots[root]
		message = replaceWarningRoot(message, root, portable)
		if slashRoot := filepath.ToSlash(root); slashRoot != root {
			message = replaceWarningRoot(message, slashRoot, portable)
		}
	}
	// A marker may be in a parent directory outside every known root (or the
	// scan may have no Git roots at all). Preserve the actionable basename and
	// replace the same path in the wrapped OS error.
	if start := strings.Index(message, " at "); start >= 0 {
		start += len(" at ")
		if end := strings.Index(message[start:], " could not be read ("); end >= 0 {
			end += start
			markerPath := message[start:end]
			if filepath.IsAbs(markerPath) {
				portable := filepath.Base(markerPath)
				message = message[:start] + portable + message[end:]
				message = strings.ReplaceAll(message, markerPath, portable)
			}
		}
	}
	// Deprecated-root warnings quote paths with spaces, which cannot be safely
	// tokenized as bare paths. Redact any quoted absolute path left after known
	// roots were replaced.
	for offset := 0; offset < len(message); {
		open := strings.IndexByte(message[offset:], '"')
		if open < 0 {
			break
		}
		open += offset
		close := strings.IndexByte(message[open+1:], '"')
		if close < 0 {
			break
		}
		close += open + 1
		quoted := message[open : close+1]
		value, err := strconv.Unquote(quoted)
		if err == nil && filepath.IsAbs(value) {
			message = message[:open] + strconv.Quote("<path>") + message[close+1:]
			offset = open + len(`"<path>"`)
		} else {
			offset = close + 1
		}
	}
	return message
}

// directory represents an unset directory as null rather than a fabricated location.
func directory(r *analyzer.Results, path string) *analyzer.Location {
	if path == "" {
		return nil
	}
	loc := location(r, path, ".")
	return &loc
}

// buildJSON is pure over the captured analysis. It also supplies the common
// diagnostics and snippet evidence used by readable reports.
func buildJSON(results *analyzer.Results, cfg *config.Config) JSONReport {
	now := analysisTime(results)
	reusableDir := cfg.Reusables.Dir
	if reusableDir == "" {
		reusableDir = cfg.ReusablesDir
	}
	out := JSONReport{Version: "2.0", GeneratedAt: now.UTC().Format(time.RFC3339Nano),
		Tool:         JSONTool{Version: stringPointer(results.ToolVersion), BuildRevision: stringPointer(results.BuildRevision)},
		Repositories: nonnil(results.Repositories),
		Config:       JSONConfig{ThresholdDays: cfg.ThresholdDays, ContentDir: location(results, cfg.ContentDir, "."), ProjectRoot: directory(results, cfg.ProjectRoot), Profile: cfg.ResolvedProfile.Name, ProfileAuto: cfg.ProfileAuto, ContentExtensions: nonnil(cfg.ContentExtensions), StalenessLevels: cfg.StalenessLevels, ExcludePatterns: nonnil(cfg.ExcludePatterns), ExcludeDirs: nonnil(cfg.ExcludeDirs), NoDefaultExcludes: cfg.NoDefaultExcludes, FileLevelOnly: cfg.FileLevelOnly, ParagraphLevel: cfg.ParagraphLevel, Reusables: JSONReusableConfig{Dir: directory(results, reusableDir), Patterns: nonnil(cfg.Reusables.Patterns), Extensions: nonnil(cfg.Reusables.Extensions), Resolver: cfg.ResolvedProfile.Resolver, ImportMap: cfg.ResolvedProfile.ImportMap}},
		Coverage:     JSONCoverage{ExcludedFiles: results.FilesExcluded(), GitIgnoredFiles: results.FilesGitIgnored(), ExtensionSkippedFiles: results.FilesSkippedByExtension(), DefaultPrunedDirectories: results.DirsSkipped(), ExcludedDirectories: results.DirsExcluded(), SkippedExtensions: results.SkippedExtensions(), PrunedDirectoryNames: results.SkippedDirNames()},
		Files:        []JSONFile{}, Reusables: []JSONReusable{}, Diagnostics: nonnil(results.Diagnostics),
	}
	addDiagnostic := func(code, message string, repo, file, section, ref *string, line *int) {
		out.Diagnostics = append(out.Diagnostics, analyzer.Diagnostic{Code: code, Severity: "warning", Message: message, RepositoryID: repo, FileID: file, SectionID: section, Reference: ref, Line: line})
	}
	for i := range out.Repositories {
		repo := &out.Repositories[i]
		if repo.Shallow != nil && *repo.Shallow {
			addDiagnostic("shallow_history", "Shallow repository: blame may not represent original changes", &repo.ID, nil, nil, nil, nil)
		}
		if repo.Status != "available" {
			addDiagnostic("repository_metadata_unavailable", "Some repository metadata is unavailable", &repo.ID, nil, nil, nil, nil)
		}
	}
	inventory := map[string]JSONReusable{}
	for _, f := range results.Files {
		loc := location(results, f.Path, f.RelativePath)
		jf := JSONFile{ID: identity("file", loc.RepositoryID, loc.Path), RepositoryID: loc.RepositoryID, Path: loc.Path, ContentPath: f.RelativePath, AnalysisStatus: f.AnalysisStatus, HistoryStatus: "available", Sections: []JSONSection{}}
		if jf.AnalysisStatus == "" {
			jf.AnalysisStatus = "analyzed"
		}
		if jf.AnalysisStatus == "failed" {
			out.Coverage.FailedFiles++
		} else {
			out.Coverage.AnalyzedFiles++
		}
		if f.HistoryMissing {
			jf.HistoryStatus = "missing"
			out.Summary.FilesMissingHistory++
			addDiagnostic("history_missing", "File has no resolvable Git history", loc.RepositoryID, &jf.ID, nil, nil, nil)
		}
		if loc.RepositoryID == nil {
			addDiagnostic("repository_unavailable", "File has no repository association", nil, &jf.ID, nil, nil, nil)
		}
		for _, d := range f.Diagnostics {
			d.FileID = &jf.ID
			d.RepositoryID = loc.RepositoryID
			out.Diagnostics = append(out.Diagnostics, d)
			if d.Code == "blame_failed" || d.Code == "uncommitted_content" {
				if !f.HistoryMissing {
					jf.HistoryStatus = "partial"
				}
			}
		}
		fallbackOccurrences := map[string]int{}
		for _, s := range f.Sections {
			key := s.LogicalKey
			if key == "" {
				fallbackOccurrences[s.Title]++
				key = identity("legacy", s.Title, fallbackOccurrences[s.Title])
			}
			js := JSONSection{ID: identity("section", jf.ID, key), Title: s.Title, HeadingPath: nonnil(s.HeadingPath), StartLine: s.StartLine, EndLine: s.EndLine, ContentFingerprint: s.Fingerprint, FreshnessSources: []JSONSource{}, Dependencies: []JSONDependency{}}
			ownDate := s.LastUpdated()
			authors, commits := []string{}, []string{}
			if ownDate != nil {
				for _, line := range s.Lines {
					if line.Timestamp.Equal(*ownDate) {
						authors = append(authors, line.Author)
						commits = append(commits, line.CommitHash)
					}
				}
			}
			js.OwnLastChange = change(ownDate, authors, commits)
			effective := s.DisplayDate()
			js.EffectiveLastChange = change(effective, nil, nil)
			if ownDate != nil && effective != nil && ownDate.Equal(*effective) {
				js.FreshnessSources = append(js.FreshnessSources, JSONSource{Kind: "own"})
				js.EffectiveLastChange.Authors = append(js.EffectiveLastChange.Authors, authors...)
				js.EffectiveLastChange.Commits = append(js.EffectiveLastChange.Commits, commits...)
			}
			depFingerprints := map[string]string{}
			sourceSeen := map[string]bool{}
			for _, d := range s.Dependencies {
				jd := JSONDependency{Reference: d.Reference, Line: d.Line, Column: d.Column, Status: d.Status, ReusableIDs: []string{}}
				for _, dep := range d.Files {
					depLoc := location(results, dep.Path, filepath.ToSlash(filepath.Base(dep.Path)))
					id := identity("reusable", depLoc.RepositoryID, depLoc.Path)
					jr := JSONReusable{ID: id, RepositoryID: depLoc.RepositoryID, Name: depLoc.Path, LastChange: change(nil, nil, nil), ContentFingerprint: dep.Fingerprint, ResolutionStatus: "resolved", HistoryStatus: "missing", Level: "unknown"}
					if dep.Info != nil {
						jr.LastChange = change(&dep.Info.LastModified, []string{dep.Info.LastAuthor}, []string{dep.Info.LastCommit})
						jr.HistoryStatus = "available"
						jr.AgeDays, _, jr.Level = classification(jr.LastChange.Date, now, cfg)
					}
					inventory[id] = jr
					jd.ReusableIDs = append(jd.ReusableIDs, id)
					depFingerprints[id] = dep.Fingerprint
					if dep.Info == nil {
						addDiagnostic("reusable_history_missing", "Resolved supporting file has no Git history", depLoc.RepositoryID, &jf.ID, &js.ID, &jd.Reference, &jd.Line)
					}
					if dep.Info != nil && effective != nil && dep.Info.LastModified.Equal(*effective) && !sourceSeen[id] {
						sourceSeen[id] = true
						js.FreshnessSources = append(js.FreshnessSources, JSONSource{Kind: "reusable", ReusableID: &id})
						js.EffectiveLastChange.Authors = append(js.EffectiveLastChange.Authors, dep.Info.LastAuthor)
						js.EffectiveLastChange.Commits = append(js.EffectiveLastChange.Commits, dep.Info.LastCommit)
					}
				}
				for _, issue := range d.Issues {
					message := "Supporting reference " + strconv.Quote(issue.Reference) + " is unavailable"
					if issue.Code == "reusable_support_rejected" {
						message = "Supporting reference " + strconv.Quote(issue.Reference) + " is outside its authorized root"
					}
					addDiagnostic(issue.Code, message, loc.RepositoryID, &jf.ID, &js.ID, &jd.Reference, &jd.Line)
					if jf.HistoryStatus == "available" {
						jf.HistoryStatus = "partial"
					}
				}
				jd.ReusableIDs = unique(jd.ReusableIDs)
				if d.Status == "unresolved" {
					addDiagnostic("reusable_unresolved", "Reusable reference has no resolved history", loc.RepositoryID, &jf.ID, &js.ID, &jd.Reference, &jd.Line)
				}
				js.Dependencies = append(js.Dependencies, jd)
			}
			pairs := [][2]string{}
			for id, hash := range depFingerprints {
				pairs = append(pairs, [2]string{id, hash})
			}
			sort.Slice(pairs, func(i, j int) bool { return pairs[i][0] < pairs[j][0] })
			data, _ := json.Marshal(pairs)
			js.DependencyFingerprint = parser.Fingerprint(string(data))
			js.EffectiveLastChange.Authors = unique(js.EffectiveLastChange.Authors)
			js.EffectiveLastChange.Commits = unique(js.EffectiveLastChange.Commits)
			sort.Slice(js.FreshnessSources, func(i, j int) bool {
				a, b := js.FreshnessSources[i], js.FreshnessSources[j]
				if a.Kind != b.Kind {
					return a.Kind < b.Kind
				}
				return *a.ReusableID < *b.ReusableID
			})
			js.AgeDays, js.IsStale, js.Level = classification(effective, now, cfg)
			if js.IsStale {
				jf.StaleSections++
				out.Summary.StaleSections++
			} else if effective == nil {
				out.Summary.UnknownSections++
			} else {
				out.Summary.FreshSections++
			}
			jf.Sections = append(jf.Sections, js)
		}
		jf.TotalSections = len(jf.Sections)
		out.Summary.TotalSections += jf.TotalSections
		if jf.StaleSections > 0 {
			out.Summary.StaleFiles++
		}
		out.Files = append(out.Files, jf)
	}
	// Compatibility for package callers that supply hand-built results rather
	// than analyzer snapshots. Real scans always use the file inventory above.
	if results.Locations == nil {
		for _, r := range results.AllReusables {
			id := identity("reusable", nil, r.Name)
			jr := JSONReusable{ID: id, Name: r.Name, LastChange: change(r.LastUpdated, []string{r.LastAuthor}, nil), ResolutionStatus: "unresolved", HistoryStatus: "missing"}
			if r.LastUpdated != nil {
				jr.HistoryStatus = "available"
				jr.ResolutionStatus = "resolved"
			}
			jr.AgeDays, _, jr.Level = classification(r.LastUpdated, now, cfg)
			inventory[id] = jr
		}
	}
	for _, r := range inventory {
		out.Reusables = append(out.Reusables, r)
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].ID < out.Files[j].ID })
	sort.Slice(out.Reusables, func(i, j int) bool { return out.Reusables[i].ID < out.Reusables[j].ID })
	out.Summary.TotalFiles = len(out.Files)
	out.Summary.TotalReusables = len(out.Reusables)
	if out.Summary.TotalFiles > 0 {
		out.Summary.StaleFilesPct = 100 * float64(out.Summary.StaleFiles) / float64(out.Summary.TotalFiles)
	}
	if out.Summary.TotalSections > 0 {
		out.Summary.StaleSectionsPct = 100 * float64(out.Summary.StaleSections) / float64(out.Summary.TotalSections)
	}
	if len(out.Files) == 0 {
		addDiagnostic("zero_matched_files", "No files matched the effective scan configuration", nil, nil, nil, nil, nil)
	}
	if out.Coverage.ExtensionSkippedFiles > 0 {
		addDiagnostic("extension_skipped", "Documentation extensions outside the allowlist were skipped", nil, nil, nil, nil, nil)
	}
	if out.Coverage.ExcludedFiles+out.Coverage.GitIgnoredFiles+out.Coverage.DefaultPrunedDirectories+out.Coverage.ExcludedDirectories > 0 {
		addDiagnostic("scan_exclusions", "Coverage excludes files or pruned directories; see coverage counters", nil, nil, nil, nil, nil)
	}
	for _, warning := range cfg.Warnings {
		addDiagnostic("config_warning", portableConfigWarning(warning, results, cfg), nil, nil, nil, nil, nil)
	}
	if len(out.Files) == 0 && out.Config.ContentDir.RepositoryID == nil {
		addDiagnostic("repository_unavailable", "Content directory has no repository association", nil, nil, nil, nil, nil)
	}
	sort.Slice(out.Diagnostics, func(i, j int) bool {
		a, _ := json.Marshal(out.Diagnostics[i])
		b, _ := json.Marshal(out.Diagnostics[j])
		return string(a) < string(b)
	})
	return out
}

// GenerateJSON writes all analyzed sections, including fresh and unknown.
func GenerateJSON(results *analyzer.Results, cfg *config.Config, outputPath string) error {
	data, err := json.MarshalIndent(buildJSON(results, cfg), "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Clean(outputPath)), 0750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Clean(outputPath), append(data, '\n'), 0600)
}

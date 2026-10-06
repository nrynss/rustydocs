package report

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/analyzer"
	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/git"
	"github.com/nrynss/rustydocs/internal/parser"
	"github.com/nrynss/rustydocs/internal/testutil"
)

// scanJSON builds a reproducible export from a real repository snapshot.
func scanJSON(t *testing.T, content string, profile string) (*analyzer.Results, JSONReport) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ContentDir, cfg.Profile = content, profile
	r, err := analyzer.Analyze(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.GeneratedAt = fixedNow
	r.ToolVersion, r.BuildRevision = "example", "example-revision"
	return r, buildJSON(r, cfg)
}

// snapshotFixture models mixed-age pages, snippet freshness, ties, code examples and missing history.
func snapshotFixture(t *testing.T) (*testutil.Repo, *analyzer.Results, JSONReport) {
	t.Helper()
	repo := testutil.NewRepo(t)
	repo.Commit(daysAgo(200), "old pages", map[string]string{
		"docs.json":           `{"name":"Fixture","theme":"mint"}`,
		"docs/mixed.mdx":      "# Old\nold prose\n\n# Included\n<Snippet file=\"../snippets/shared.mdx\" />\n\n# Recent\nold recent\n",
		"docs/imported.mdx":   "import Shared from '../snippets/shared.mdx';\n\n# Import use\n<Shared />\n",
		"docs/fence.mdx":      "# Fence\n```sh\n# shell comment\n```\n",
		"docs/tie.mdx":        "# Tie\n<Snippet file=\"../snippets/tied.mdx\" />\n",
		"docs/unresolved.mdx": "# Broken\n<Snippet file=\"../snippets/missing.mdx\" />\n",
		"docs/boundary.mdx":   "# Boundary\nexact\n",
		"snippets/shared.mdx": "old snippet\n",
		"snippets/tied.mdx":   "tied content\n",
	})
	repo.Commit(daysAgo(90), "boundary", map[string]string{"docs/boundary.mdx": "# Boundary\nexact reporting boundary\n"})
	repo.Commit(daysAgo(10), "snippet update", map[string]string{"snippets/shared.mdx": "new snippet\n"})
	repo.Commit(daysAgo(5), "recent", map[string]string{"docs/mixed.mdx": "# Old\nold prose\n\n# Included\n<Snippet file=\"../snippets/shared.mdx\" />\n\n# Recent\nnew recent\n"})
	repo.Write("docs/untracked.mdx", "# Unknown\nuncommitted\n")
	r, out := scanJSON(t, repo.Path("docs"), "mintlify")
	return repo, r, out
}

// fileByPath locates a content-relative file in a portable export.
func fileByPath(t *testing.T, r JSONReport, path string) JSONFile {
	t.Helper()
	for _, f := range r.Files {
		if f.ContentPath == path {
			return f
		}
	}
	t.Fatalf("file %s missing", path)
	return JSONFile{}
}

// TestJSONCompleteSnapshotAndGolden pins the complete portable contract and its public example artifact.
func TestJSONCompleteSnapshotAndGolden(t *testing.T) {
	repo, r, out := snapshotFixture(t)
	if out.Version != "2.0" || out.Summary.TotalFiles != 7 || out.Summary.TotalSections != 9 || out.Summary.StaleSections != 4 || out.Summary.FreshSections != 4 || out.Summary.UnknownSections != 1 {
		t.Fatalf("incorrect inventory: %+v", out.Summary)
	}
	if out.Coverage.AnalyzedFiles != 7 || out.Coverage.FailedFiles != 0 {
		t.Fatal(out.Coverage)
	}
	if len(out.Repositories) != 1 || out.Repositories[0].Dirty == nil || !*out.Repositories[0].Dirty || out.Repositories[0].Shallow == nil || *out.Repositories[0].Shallow {
		t.Fatalf("repository context: %+v", out.Repositories)
	}
	mixed := fileByPath(t, out, "mixed.mdx")
	included := mixed.Sections[1]
	if *included.AgeDays != 10 || included.IsStale || included.OwnLastChange.Date.Equal(*included.EffectiveLastChange.Date) || len(included.Dependencies) != 2 || len(included.FreshnessSources) != 1 || included.FreshnessSources[0].Kind != "reusable" {
		t.Fatalf("included: %+v", included)
	}
	tie := fileByPath(t, out, "tie.mdx").Sections[0]
	if len(tie.FreshnessSources) != 2 || tie.FreshnessSources[0].Kind != "own" {
		t.Fatalf("tie sources: %+v", tie.FreshnessSources)
	}
	unknown := fileByPath(t, out, "untracked.mdx").Sections[0]
	if unknown.AgeDays != nil || unknown.OwnLastChange.Date != nil || unknown.EffectiveLastChange.Date != nil || unknown.IsStale || unknown.Level != "unknown" {
		t.Fatal(unknown)
	}
	boundary := fileByPath(t, out, "boundary.mdx").Sections[0]
	if boundary.IsStale || *boundary.AgeDays != 90 || boundary.Level != "warning" {
		t.Fatal(boundary)
	}
	if len(fileByPath(t, out, "fence.mdx").Sections) != 1 || len(fileByPath(t, out, "imported.mdx").Sections) != 1 {
		t.Fatal("false sections")
	}
	raw, _ := json.Marshal(out)
	if bytes.Contains(raw, []byte(repo.Dir)) || bytes.Contains(raw, []byte(`"days_stale"`)) || bytes.Contains(raw, []byte(`"last_updated"`)) {
		t.Fatal("nonportable or legacy page metrics")
	}
	// The exported snapshot remains unchanged after the checkout changes.
	repo.Write("snippets/shared.mdx", "later working tree content")
	again, _ := json.Marshal(buildJSON(r, r.Config))
	if !bytes.Equal(raw, again) {
		t.Fatal("export reread or nondeterministic output")
	}
	// Git object hashes vary with platform/configuration; canonicalize only those
	// incidental values in the public example, retaining the complete contract.
	for i := range out.Repositories {
		out.Repositories[i].Revision = stringPointer("example-revision")
	}
	for i := range out.Files {
		for j := range out.Files[i].Sections {
			s := &out.Files[i].Sections[j]
			for _, c := range []*JSONChange{&s.OwnLastChange, &s.EffectiveLastChange} {
				if len(c.Commits) > 0 {
					c.Commits = []string{"example-commit"}
				}
			}
		}
	}
	for i := range out.Reusables {
		if len(out.Reusables[i].LastChange.Commits) > 0 {
			out.Reusables[i].LastChange.Commits = []string{"example-commit"}
		}
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	data = append(data, '\n')
	golden := filepath.Join("..", "..", "docs", "examples", "scan-v2.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(golden), 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	// Git may check out the example with CRLF (core.autocrlf on Windows).
	// Normalize only physical line endings; retain strict bytes for JSON values,
	// escaped string content, whitespace, field order and array order. Exercise
	// both checkout forms on every platform so Windows compatibility is tested.
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	for _, checkout := range []struct {
		name string
		data []byte
	}{
		{"LF", want},
		{"CRLF", bytes.ReplaceAll(want, []byte("\n"), []byte("\r\n"))},
	} {
		t.Run(checkout.name, func(t *testing.T) {
			checkedOut := bytes.ReplaceAll(checkout.data, []byte("\r\n"), []byte("\n"))
			if !bytes.Equal(data, checkedOut) {
				t.Fatal("JSON contract example changed; inspect diff then UPDATE_GOLDEN=1 go test ./internal/report -run TestJSONCompleteSnapshotAndGolden")
			}
		})
	}
}

// TestJSONIdentityAcrossCheckoutsAndLineShifts keeps identities independent of runner paths and unrelated line insertions.
func TestJSONIdentityAcrossCheckoutsAndLineShifts(t *testing.T) {
	var baseline JSONSection
	for i := 0; i < 2; i++ {
		repo := testutil.NewRepo(t)
		body := "# Stable\ncontent\n\n# Duplicate\nfirst\n\n# Duplicate\nsecond\n"
		if i == 1 {
			body = "\n\n" + body
		}
		repo.Commit(daysAgo(200), "content", map[string]string{"docs/page.md": body})
		_, out := scanJSON(t, repo.Path("docs"), "markdown")
		sections := out.Files[0].Sections
		if len(sections) != 3 || sections[1].ID == sections[2].ID {
			t.Fatal(sections)
		}
		if i == 0 {
			baseline = sections[0]
		} else if baseline.ID != sections[0].ID || baseline.ContentFingerprint != sections[0].ContentFingerprint || baseline.StartLine == sections[0].StartLine {
			t.Fatal("identity depends on checkout/line location")
		}
	}
}

// gitCommand executes fixture Git operations and reports useful failure context.
func gitCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, data)
	}
	return strings.TrimSpace(string(data))
}

// TestJSONRepositoryAndCoverageCases reconciles exclusions and retains nested, shallow and unavailable repository context.
func TestJSONRepositoryAndCoverageCases(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(daysAgo(5), "fresh", map[string]string{"docs/fresh.md": "# Fresh\nbody\n", "docs/skip.mdx": "# Skip\n", "docs/exclude.md": "# Exclude\n", ".gitignore": "docs/ignored.md\n"})
	repo.Write("docs/ignored.md", "# Ignored\n")
	repo.Write("docs/node_modules/deep.md", "# Pruned\n")
	cfg := config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")
	cfg.ExcludePatterns = []string{"exclude.md"}
	r, err := analyzer.Analyze(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.GeneratedAt = fixedNow
	out := buildJSON(r, cfg)
	if out.Summary.TotalFiles != 1 || out.Summary.FreshSections != 1 || out.Coverage.ExcludedFiles != 1 || out.Coverage.GitIgnoredFiles != 1 || out.Coverage.ExtensionSkippedFiles != 1 || out.Coverage.DefaultPrunedDirectories != 1 {
		t.Fatalf("coverage: %+v summary: %+v", out.Coverage, out.Summary)
	}
	// A real nested Git root retains its own revision and identity.
	nested := repo.Path("docs/nested")
	if err := os.MkdirAll(nested, 0750); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, nested, "init", "-q")
	gitCommand(t, nested, "config", "user.email", "test@example.com")
	gitCommand(t, nested, "config", "user.name", "Nested Author")
	if err := os.WriteFile(filepath.Join(nested, "page.md"), []byte("# Nested\nbody\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, nested, "add", ".")
	gitCommand(t, nested, "-c", "commit.gpgsign=false", "commit", "-qm", "nested")
	cfg.NoDefaultExcludes = true
	cfg.ExcludePatterns = nil
	multiResult, err := analyzer.Analyze(cfg)
	if err != nil {
		t.Fatal(err)
	}
	multiResult.GeneratedAt = fixedNow
	multi := buildJSON(multiResult, cfg)
	if len(multi.Repositories) != 2 || fileByPath(t, multi, "nested/page.md").RepositoryID == nil {
		t.Fatalf("repos %+v files %+v", multi.Repositories, multi.Files)
	}
	// Cloning via file:// activates --depth semantics even for local repositories.
	shallow := filepath.Join(t.TempDir(), "shallow")
	gitCommand(t, t.TempDir(), "clone", "-q", "--depth=1", "file://"+filepath.ToSlash(repo.Dir), shallow)
	_, thin := scanJSON(t, filepath.Join(shallow, "docs"), "markdown")
	if len(thin.Repositories) != 1 || thin.Repositories[0].Shallow == nil || !*thin.Repositories[0].Shallow {
		t.Fatal(thin.Repositories)
	}
	found := false
	for _, d := range thin.Diagnostics {
		found = found || d.Code == "shallow_history"
	}
	if !found {
		t.Fatal("missing shallow diagnostic")
	}
	empty := t.TempDir()
	_, none := scanJSON(t, empty, "markdown")
	if none.Summary.TotalFiles != 0 || none.Files == nil || none.Reusables == nil || none.Repositories == nil || none.Diagnostics == nil {
		t.Fatal(none)
	}
	found = false
	for _, d := range none.Diagnostics {
		found = found || d.Code == "zero_matched_files"
	}
	if !found {
		t.Fatal("missing zero matched diagnostic")
	}
}

// TestJSONOutputErrors ensures export failures are returned and unknown dates stay unassessed.
func TestJSONOutputErrors(t *testing.T) {
	res, cfg := sampleResults()
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if GenerateJSON(res, cfg, filepath.Join(blocker, "out.json")) == nil {
		t.Fatal("expected mkdir error")
	}
	if GenerateJSON(res, cfg, t.TempDir()) == nil {
		t.Fatal("expected write error")
	}
	if d, stale, severity := classification(nil, time.Now(), cfg); d != nil || stale || severity != "unknown" {
		t.Fatal("unknown classification")
	}
}

// TestJSONGeneratedAtPreservesFractionalBoundary keeps subsecond threshold decisions consistent with exported time.
func TestJSONGeneratedAtPreservesFractionalBoundary(t *testing.T) {
	base := fixedNow
	generated := base.Add(500 * time.Millisecond)
	change := base.Add(-90*24*time.Hour + 250*time.Millisecond)
	section := parser.Chunk{Title: "Boundary", StartLine: 1, EndLine: 2, Lines: []git.LineInfo{{LineNumber: 1, Timestamp: change}}}
	cfg := config.DefaultConfig()
	cfg.ThresholdDays = 90
	cfg.ContentDir = t.TempDir()
	if err := cfg.ApplyProfile(); err != nil {
		t.Fatal(err)
	}
	results := &analyzer.Results{GeneratedAt: generated, Config: cfg, Files: []analyzer.FileAnalysis{{Path: filepath.Join(cfg.ContentDir, "page.md"), RelativePath: "page.md", Sections: []parser.Section{section}}}}
	out := buildJSON(results, cfg)
	serialized, err := time.Parse(time.RFC3339Nano, out.GeneratedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !serialized.Equal(generated) {
		t.Fatalf("generated_at = %s, want full-precision %s", serialized, generated)
	}
	_, recomputedStale, _ := classification(&change, serialized, cfg)
	if !recomputedStale || len(out.Files) != 1 || len(out.Files[0].Sections) != 1 || !out.Files[0].Sections[0].IsStale {
		t.Fatalf("serialized classification disagrees: exported stale=%v, recomputed stale=%v", out.Files[0].Sections[0].IsStale, recomputedStale)
	}
}

// TestJSONConfigWarningsRetainPortableDetails retains actionable warning text without exposing the checkout directory.
func TestJSONConfigWarningsRetainPortableDetails(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(daysAgo(1), "page", map[string]string{"docs/page.md": "# Page\nbody\n"})
	configPath := repo.Path("rustydocs.json")
	configBody := `{"content_dir":` + `"` + filepath.ToSlash(repo.Path("docs")) + `","hugo_root":"` + filepath.ToSlash(repo.Dir) + `"}`
	if err := os.WriteFile(configPath, []byte(configBody), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	results, err := analyzer.Analyze(cfg)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(repo.Dir, "docs.json")
	cfg.Warnings = append(cfg.Warnings, `candidate root marker "docs.json" (profile "mintlify") at `+marker+` could not be read (open `+marker+`: permission denied); that marker was skipped, so it could not contribute to profile detection`)
	out := buildJSON(results, cfg)
	var warnings []string
	for _, d := range out.Diagnostics {
		if d.Code == "config_warning" {
			warnings = append(warnings, d.Message)
		}
	}
	if len(warnings) != 2 {
		t.Fatalf("config warnings = %v, want both individual warnings", warnings)
	}
	joined := strings.Join(warnings, "\n")
	for _, expected := range []string{"hugo_root", "deprecated", "rename it", "docs.json", "mintlify", "could not be read", "marker was skipped"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("portable warnings omitted %q: %s", expected, joined)
		}
	}
	if strings.Contains(joined, repo.Dir) {
		t.Fatalf("warning leaked checkout path %q: %s", repo.Dir, joined)
	}
}

// TestJSONConfigWarningRedactsNonGitParentMarkerPath redacts parent marker locations even without captured Git roots.
func TestJSONConfigWarningRedactsNonGitParentMarkerPath(t *testing.T) {
	root := t.TempDir()
	contentDir := filepath.Join(root, "content")
	if err := os.MkdirAll(contentDir, 0750); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.ContentDir = contentDir
	if err := cfg.ApplyProfile(); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "docs.json")
	cfg.Warnings = []string{`candidate root marker "docs.json" (profile "mintlify") at ` + marker + ` could not be read (open ` + marker + `: permission denied); that marker was skipped, so it could not contribute to profile detection`}
	results := &analyzer.Results{Config: cfg, GeneratedAt: fixedNow, Locations: map[string]analyzer.Location{}}
	out := buildJSON(results, cfg)
	var warning string
	for _, d := range out.Diagnostics {
		if d.Code == "config_warning" {
			warning = d.Message
		}
	}
	if !strings.Contains(warning, "docs.json") || !strings.Contains(warning, "could not be read") || !strings.Contains(warning, "permission denied") {
		t.Fatalf("warning lost actionable marker details: %q", warning)
	}
	if strings.Contains(warning, root) {
		t.Fatalf("non-Git parent marker leaked absolute path %q: %s", root, warning)
	}
}

// TestPortableConfigWarningExactRoots checks that file, directory and relative
// location keys infer the repository itself and preserve actionable wording.
func TestPortableConfigWarningExactRoots(t *testing.T) {
	repositoryID := "repo:test"
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, checkout := range []string{"runner-alpha", "runner-beta"} {
		root := filepath.Join(t.TempDir(), checkout)
		for _, kind := range []string{"file", "directory", "root"} {
			t.Run(checkout+"/"+kind, func(t *testing.T) {
				key, relative := root, "."
				if kind == "file" {
					key = filepath.Join(root, "docs", "page.md")
					relative = "docs/page.md"
				}
				if kind == "directory" {
					key = filepath.Join(root, "docs")
					relative = "docs"
				}
				results := &analyzer.Results{Locations: map[string]analyzer.Location{key: {RepositoryID: &repositoryID, Path: relative}}}
				marker := filepath.Join(root, "docs.json")
				message := "candidate marker at " + marker + " could not be read (open " + marker + ": permission denied); mdx/jsx v1.2 was skipped"
				want := "candidate marker at ./docs.json could not be read (open ./docs.json: permission denied); mdx/jsx v1.2 was skipped"
				if got := portableConfigWarning(message, results, config.DefaultConfig()); got != want {
					t.Fatalf("message=%q, want %q", got, want)
				}
			})
		}
	}
	for _, key := range []string{filepath.Join("docs", "page.md"), "docs", "."} {
		t.Run("relative/"+key, func(t *testing.T) {
			rel := filepath.ToSlash(key)
			results := &analyzer.Results{Locations: map[string]analyzer.Location{key: {RepositoryID: &repositoryID, Path: rel}}}
			marker := filepath.Join(current, "docs.json")
			message := "candidate marker at " + marker + " could not be read (open " + marker + ": permission denied); ./mdx/jsx v1.2"
			want := "candidate marker at ./docs.json could not be read (open ./docs.json: permission denied); ./mdx/jsx v1.2"
			if got := portableConfigWarning(message, results, config.DefaultConfig()); got != want {
				t.Fatalf("message=%q, want %q", got, want)
			}
		})
	}
}

// TestPortableConfigWarningRootAndPrefixBoundaries prevents filesystem roots
// and sibling or relative path substrings from rewriting unrelated warning text.
func TestPortableConfigWarningRootAndPrefixBoundaries(t *testing.T) {
	cfg := config.DefaultConfig()
	abs, err := filepath.Abs(string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ContentDir = abs
	marker := filepath.Join(abs, "docs.json")
	message := "candidate marker at " + marker + " could not be read (open " + marker + ": permission denied); ./mdx/jsx v1.2"
	want := "candidate marker at docs.json could not be read (open docs.json: permission denied); ./mdx/jsx v1.2"
	if got := portableConfigWarning(message, &analyzer.Results{}, cfg); got != want {
		t.Fatalf("root message=%q, want %q", got, want)
	}
	cfg.ContentDir = filepath.Join(t.TempDir(), "project")
	sibling := cfg.ContentDir + "-other"
	text := "sibling " + sibling + "; relative prose examples" + cfg.ContentDir + "; ordinary mdx/jsx v1.2"
	if got := portableConfigWarning(text, &analyzer.Results{}, cfg); got != text {
		t.Fatalf("unrelated message changed: %q, want %q", got, text)
	}
}

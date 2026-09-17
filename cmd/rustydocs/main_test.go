package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/testutil"
)

func TestRunArgs_Version(t *testing.T) {
	var out, errb bytes.Buffer
	if err := runArgs([]string{"--version"}, &out, &errb); err != nil {
		t.Fatalf("runArgs --version: %v", err)
	}
	if !strings.Contains(out.String(), "rustydocs ") {
		t.Errorf("version output = %q", out.String())
	}
}

func TestRunArgs_RequiresContentDir(t *testing.T) {
	var out, errb bytes.Buffer
	if err := runArgs([]string{}, &out, &errb); err == nil {
		t.Error("expected error when --content-dir is missing")
	}
}

func TestRunArgs_ContentDirMustExist(t *testing.T) {
	var out, errb bytes.Buffer
	missing := filepath.Join(t.TempDir(), "nope")
	if err := runArgs([]string{"--content-dir", missing}, &out, &errb); err == nil {
		t.Error("expected error for nonexistent content dir")
	}
}

// minimal shape for asserting on the JSON report.
type jsonReport struct {
	Config struct {
		Profile           string   `json:"profile"`
		ProfileAuto       bool     `json:"profile_auto"`
		ContentExtensions []string `json:"content_extensions"`
	} `json:"config"`
	Summary struct {
		FilesMissingHistory int `json:"files_missing_history"`
	} `json:"summary"`
	Files []struct {
		Path     string `json:"path"`
		Sections []struct {
			Title string `json:"title"`
			Level string `json:"level"`
		} `json:"sections"`
	} `json:"files"`
	Reusables []jsonReusable `json:"reusables"`
}

// jsonReusable is one row of the report's cross-file reusables table.
type jsonReusable struct {
	Name        string `json:"name"`
	LastUpdated string `json:"last_updated"`
	Level       string `json:"level"`
}

func readJSONReport(t *testing.T, dir string) jsonReport {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "stale-docs.json"))
	if err != nil {
		t.Fatal(err)
	}
	var r jsonReport
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("invalid JSON report: %v", err)
	}
	return r
}

func TestRunArgs_FullPipeline(t *testing.T) {
	now := time.Now()
	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -200), "old", map[string]string{
		"docs/old.md": "# Old\n\nbody\n",
	})
	outDir := filepath.Join(t.TempDir(), "reports")

	var out, errb bytes.Buffer
	err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", outDir,
		"--threshold-days", "90",
	}, &out, &errb)
	if err != nil {
		t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
	}

	for _, name := range []string{"stale-docs.md", "stale-docs.html", "stale-docs.json"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Errorf("expected report %s: %v", name, err)
		}
	}
	if !strings.Contains(out.String(), "Stale sections:") {
		t.Errorf("summary not printed:\n%s", out.String())
	}
}

// TestRunArgs_ThresholdClampsStalenessClass pins #54 end-to-end: with a tighter
// --threshold-days than the default warning tier, a stale section must be
// classified at least "warning", never "fresh".
func TestRunArgs_ThresholdClampsStalenessClass(t *testing.T) {
	now := time.Now()
	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -45), "v", map[string]string{
		"docs/page.md": "# Section\n\nbody\n",
	})
	outDir := filepath.Join(t.TempDir(), "reports")

	var out, errb bytes.Buffer
	err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", outDir,
		"--threshold-days", "30",
	}, &out, &errb)
	if err != nil {
		t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
	}

	rep := readJSONReport(t, outDir)
	var levels []string
	for _, f := range rep.Files {
		for _, s := range f.Sections {
			levels = append(levels, s.Level)
		}
	}
	if len(levels) == 0 {
		t.Fatalf("expected a stale section in the report")
	}
	for _, lvl := range levels {
		if lvl == "fresh" || lvl == "" {
			t.Errorf("stale section classified %q; must be at least 'warning' (#54)", lvl)
		}
	}
}

// TestRunArgs_WarnsOnMissingHistory pins #55 end-to-end: an uncommitted file is
// surfaced (stderr warning + JSON summary), not silently treated as fresh.
func TestRunArgs_WarnsOnMissingHistory(t *testing.T) {
	now := time.Now()
	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -5), "init", map[string]string{
		"docs/tracked.md": "# T\n\nbody\n",
	})
	repo.Write("docs/untracked.md", "# U\n\nbody\n")
	outDir := filepath.Join(t.TempDir(), "reports")

	var out, errb bytes.Buffer
	err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", outDir,
	}, &out, &errb)
	if err != nil {
		t.Fatalf("runArgs: %v", err)
	}
	if !strings.Contains(errb.String(), "no git history") {
		t.Errorf("expected a missing-history warning on stderr, got: %q", errb.String())
	}
	if rep := readJSONReport(t, outDir); rep.Summary.FilesMissingHistory != 1 {
		t.Errorf("files_missing_history = %d, want 1", rep.Summary.FilesMissingHistory)
	}
}

func TestRunArgs_ListProfiles(t *testing.T) {
	var out, errb bytes.Buffer
	if err := runArgs([]string{"--list-profiles"}, &out, &errb); err != nil {
		t.Fatalf("runArgs --list-profiles: %v", err)
	}
	for _, name := range []string{"markdown", "hugo", "mintlify"} {
		if !strings.Contains(out.String(), name) {
			t.Errorf("--list-profiles output missing %q:\n%s", name, out.String())
		}
	}
	if errb.Len() != 0 {
		t.Errorf("--list-profiles wrote to stderr: %q", errb.String())
	}
}

func TestRunArgs_UnknownProfile(t *testing.T) {
	var out, errb bytes.Buffer
	err := runArgs([]string{"--content-dir", t.TempDir(), "--profile", "bogus"}, &out, &errb)
	if err == nil || !strings.Contains(err.Error(), "bogus") || !strings.Contains(err.Error(), "markdown") {
		t.Fatalf("expected unknown-profile error naming valid profiles, got %v", err)
	}
}

// TestRunArgs_ProfileBanner pins #11 end-to-end: a plain repo reports the
// auto-detected markdown profile and does not treat `<Foo />` / `{{< bar >}}`
// as reusables; --profile hugo is reported as explicit and detects them.
func TestRunArgs_ProfileBanner(t *testing.T) {
	now := time.Now()
	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -200), "v", map[string]string{
		"docs/page.md": "# A\n\nSee <Foo /> and {{< bar >}}.\n",
	})

	type jsonWithReusables struct {
		Reusables []struct {
			Name string `json:"name"`
		} `json:"reusables"`
	}
	readReusables := func(dir string) []string {
		data, err := os.ReadFile(filepath.Join(dir, "stale-docs.json"))
		if err != nil {
			t.Fatal(err)
		}
		var r jsonWithReusables
		if err := json.Unmarshal(data, &r); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, x := range r.Reusables {
			names = append(names, x.Name)
		}
		return names
	}

	outDir := filepath.Join(t.TempDir(), "auto")
	var out, errb bytes.Buffer
	if err := runArgs([]string{"--content-dir", repo.Path("docs"), "--output-dir", outDir}, &out, &errb); err != nil {
		t.Fatalf("runArgs(auto): %v\nstderr: %s", err, errb.String())
	}
	if !strings.Contains(out.String(), "Profile: markdown (auto-detected)") {
		t.Errorf("banner missing auto-detected markdown profile:\n%s", out.String())
	}
	if names := readReusables(outDir); len(names) != 0 {
		t.Errorf("markdown profile must not report reusables, got %v", names)
	}

	outDir2 := filepath.Join(t.TempDir(), "hugo")
	out.Reset()
	errb.Reset()
	if err := runArgs([]string{"--content-dir", repo.Path("docs"), "--output-dir", outDir2, "--profile", "hugo"}, &out, &errb); err != nil {
		t.Fatalf("runArgs(hugo): %v\nstderr: %s", err, errb.String())
	}
	if !strings.Contains(out.String(), "Profile: hugo\n") {
		t.Errorf("banner missing explicit hugo profile:\n%s", out.String())
	}
	names := readReusables(outDir2)
	if !strings.Contains(strings.Join(names, ","), "Foo") || !strings.Contains(strings.Join(names, ","), "bar") {
		t.Errorf("hugo profile should report Foo and bar reusables, got %v", names)
	}

	// Root variants of describeProfile: once a hugo marker exists at the repo
	// root, the banner names it as "root: <path>", after "auto-detected" when
	// the profile was detected and alone when --profile hugo was explicit.
	// An absolute --content-dir yields an absolute root.
	repo.Commit(now.AddDate(0, 0, -100), "hugo", map[string]string{
		"layouts/shortcodes/bar.html": "<b>bar</b>\n",
	})
	root := repo.Path("")
	if got, err := filepath.Abs(root); err == nil {
		root = filepath.Clean(got)
	}

	outDir3 := filepath.Join(t.TempDir(), "auto-hugo")
	out.Reset()
	errb.Reset()
	if err := runArgs([]string{"--content-dir", repo.Path("docs"), "--output-dir", outDir3}, &out, &errb); err != nil {
		t.Fatalf("runArgs(auto hugo): %v\nstderr: %s", err, errb.String())
	}
	if want := "Profile: hugo (auto-detected, root: " + root + ")\n"; !strings.Contains(out.String(), want) {
		t.Errorf("banner missing %q:\n%s", want, out.String())
	}

	outDir4 := filepath.Join(t.TempDir(), "explicit-hugo")
	out.Reset()
	errb.Reset()
	if err := runArgs([]string{"--content-dir", repo.Path("docs"), "--output-dir", outDir4, "--profile", "hugo"}, &out, &errb); err != nil {
		t.Fatalf("runArgs(explicit hugo with marker): %v\nstderr: %s", err, errb.String())
	}
	if want := "Profile: hugo (root: " + root + ")\n"; !strings.Contains(out.String(), want) {
		t.Errorf("banner missing %q:\n%s", want, out.String())
	}
}

func TestRunArgs_BadConfig(t *testing.T) {
	var out, errb bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing.json")
	if err := runArgs([]string{"--config", missing}, &out, &errb); err == nil {
		t.Error("expected error for a missing --config file")
	}
}

// TestRunArgs_ConfigAndFlagMerge exercises the --config load path plus the
// CLI-override branches (exclude-dirs, extensions, workers, paragraph-level,
// reusables-dir, file-level-only).
func TestRunArgs_ConfigAndFlagMerge(t *testing.T) {
	now := time.Now()
	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -200), "v", map[string]string{
		"docs/page.md":     "# A\n\npara one\n\npara two\n",
		"docs/images/x.md": "# X\n\nbody\n",
	})
	cfgPath := repo.Path("config.json")
	if err := os.WriteFile(cfgPath,
		[]byte(`{"threshold_days":120,"staleness_levels":{"warning":120,"caution":180,"critical":365}}`),
		0o600); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(t.TempDir(), "r1")
	var out, errb bytes.Buffer
	err := runArgs([]string{
		"--config", cfgPath,
		"--content-dir", repo.Path("docs"),
		"--output-dir", outDir,
		"--exclude-dirs", "images",
		"--extensions", ".md",
		"--workers", "2",
		"--paragraph-level",
		"--threshold-days", "90",
	}, &out, &errb)
	if err != nil {
		t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
	}
	for _, f := range readJSONReport(t, outDir).Files {
		if strings.Contains(f.Path, "images/") {
			t.Errorf("excluded dir leaked into report: %s", f.Path)
		}
	}

	// Second run: file-level-only + reusables-dir branches.
	outDir2 := filepath.Join(t.TempDir(), "r2")
	out.Reset()
	errb.Reset()
	if err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", outDir2,
		"--reusables-dir", repo.Path("shared"),
		"--file-level-only",
	}, &out, &errb); err != nil {
		t.Fatalf("file-level-only run: %v\nstderr: %s", err, errb.String())
	}
}

// TestResolveBuildInfo pins the go-install fallback (#39): ldflags values win,
// and a version or commit left at its default is filled from the embedded
// build info. The build date is never derived from build info (vcs.time is the
// HEAD commit time, not the build time), which TestBuildInfo_DateIgnoresVCSTime
// covers.
func TestResolveBuildInfo(t *testing.T) {
	const fullRev = "0123456789abcdef0123456789abcdef01234567"
	settings := func(kv ...string) []debug.BuildSetting {
		var out []debug.BuildSetting
		for i := 0; i+1 < len(kv); i += 2 {
			out = append(out, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
		}
		return out
	}
	full := &debug.BuildInfo{
		Main:     debug.Module{Path: "github.com/nrynss/rustydocs", Version: "v0.4.0"},
		Settings: settings("vcs.revision", fullRev, "vcs.modified", "false", "vcs.time", "2026-06-24T10:00:00Z"),
	}

	tests := []struct {
		name  string
		v, c  string
		info  *debug.BuildInfo
		ok    bool
		wantV string
		wantC string
	}{
		{
			name: "ldflags set win over build info",
			v:    "v1.2.3", c: "abc1234",
			info: full, ok: true,
			wantV: "v1.2.3", wantC: "abc1234",
		},
		{
			name: "ldflags unset, module version and vcs settings present",
			v:    "dev", c: "none",
			info: full, ok: true,
			wantV: "v0.4.0", wantC: fullRev[:12],
		},
		{
			name: "(devel) module version is ignored",
			v:    "dev", c: "none",
			info: &debug.BuildInfo{
				Main:     debug.Module{Version: "(devel)"},
				Settings: settings("vcs.revision", fullRev, "vcs.time", "2026-06-24T10:00:00Z"),
			},
			ok:    true,
			wantV: "dev", wantC: fullRev[:12],
		},
		{
			name: "empty module version is ignored",
			v:    "dev", c: "none",
			info:  &debug.BuildInfo{Main: debug.Module{Version: ""}},
			ok:    true,
			wantV: "dev", wantC: "none",
		},
		{
			name: "vcs settings absent leaves commit at default",
			v:    "dev", c: "none",
			info:  &debug.BuildInfo{Main: debug.Module{Version: "v0.5.0"}},
			ok:    true,
			wantV: "v0.5.0", wantC: "none",
		},
		{
			name: "vcs.modified true appends -dirty",
			v:    "dev", c: "none",
			info: &debug.BuildInfo{
				Main:     debug.Module{Version: "v0.5.0"},
				Settings: settings("vcs.revision", fullRev, "vcs.modified", "true"),
			},
			ok:    true,
			wantV: "v0.5.0", wantC: fullRev[:12] + "-dirty",
		},
		{
			name: "short revision is kept as-is",
			v:    "dev", c: "none",
			info:  &debug.BuildInfo{Settings: settings("vcs.revision", "abc1234")},
			ok:    true,
			wantV: "dev", wantC: "abc1234",
		},
		{
			name: "partial ldflags: only unset fields are filled",
			v:    "v9.9.9", c: "none",
			info: full, ok: true,
			wantV: "v9.9.9", wantC: fullRev[:12],
		},
		{
			name: "ok false returns inputs unchanged",
			v:    "dev", c: "none",
			info: full, ok: false,
			wantV: "dev", wantC: "none",
		},
		{
			name: "nil info returns inputs unchanged",
			v:    "dev", c: "none",
			info: nil, ok: true,
			wantV: "dev", wantC: "none",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotV, gotC := resolveBuildInfo(tc.v, tc.c, tc.info, tc.ok)
			if gotV != tc.wantV || gotC != tc.wantC {
				t.Errorf("resolveBuildInfo() = (%q, %q), want (%q, %q)",
					gotV, gotC, tc.wantV, tc.wantC)
			}
		})
	}
}

// TestBuildInfo_DateIgnoresVCSTime asserts that the reported build date stays
// at its ldflags value ("unknown" by default) even when the embedded build
// info carries a vcs.time setting: vcs.time is the HEAD commit timestamp, not
// the build time, and must not be printed under the "built:" label (#39).
func TestBuildInfo_DateIgnoresVCSTime(t *testing.T) {
	oldV, oldC, oldD := version, commit, date
	t.Cleanup(func() { version, commit, date = oldV, oldC, oldD })
	version, commit, date = defaultVersion, defaultCommit, defaultDate

	if _, ok := debug.ReadBuildInfo(); !ok {
		t.Skip("no embedded build info in test binary")
	}
	_, _, gotD := buildInfo()
	if gotD != defaultDate {
		t.Errorf("buildInfo() date = %q, want %q (vcs.time must not be used)", gotD, defaultDate)
	}

	// resolveBuildInfo itself has no date output at all, so a vcs.time setting
	// cannot influence it; pin that it still fills version and commit.
	info := &debug.BuildInfo{
		Main:     debug.Module{Version: "v0.5.0"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abcdef123456"}, {Key: "vcs.time", Value: "2026-06-24T10:00:00Z"}},
	}
	v, c := resolveBuildInfo(defaultVersion, defaultCommit, info, true)
	if v != "v0.5.0" || c != "abcdef123456" {
		t.Errorf("resolveBuildInfo() = (%q, %q), want (\"v0.5.0\", \"abcdef123456\")", v, c)
	}
}

// TestRunArgs_VersionOutputFormat pins the --version output shape: the first
// line is always "rustydocs <version>", and commit/built lines are only
// printed when known.
func TestRunArgs_VersionOutputFormat(t *testing.T) {
	var out, errb bytes.Buffer
	if err := runArgs([]string{"--version"}, &out, &errb); err != nil {
		t.Fatalf("runArgs --version: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if !strings.HasPrefix(lines[0], "rustydocs ") || lines[0] == "rustydocs " {
		t.Errorf("first line = %q, want \"rustydocs <version>\"", lines[0])
	}
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, "  commit: ") && !strings.HasPrefix(l, "  built:  ") {
			t.Errorf("unexpected --version line %q", l)
		}
		if strings.HasSuffix(l, ": none") || strings.HasSuffix(l, ": unknown") {
			t.Errorf("placeholder leaked into --version output: %q", l)
		}
	}
	if errb.Len() != 0 {
		t.Errorf("--version wrote to stderr: %q", errb.String())
	}
}

// TestRunArgs_NoFilesWarningAttributesLegacyReusablesWidening: the legacy
// --reusables-dir flow makes ApplyProfile widen the extensions itself (it adds
// the hugo profile's list on top of the markdown profile), so the resolved
// allowlist differs from the profile's without the user having overridden
// anything. The warning must attribute the list to neither: saying it is the
// markdown profile's extensions would contradict --list-profiles, and saying it
// is configured would blame the user. It says "the active extensions" and names
// the profile separately; only a real --extensions override says "the
// configured extensions". See #11.
func TestRunArgs_NoFilesWarningAttributesLegacyReusablesWidening(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "only.rst"), []byte("nothing to analyze\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reusables := filepath.Join(dir, "_reusables")
	if err := os.MkdirAll(reusables, 0o755); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "reports")

	var out, errb bytes.Buffer
	if err := runArgs([]string{
		"--content-dir", dir, "--output-dir", outDir, "--reusables-dir", reusables,
	}, &out, &errb); err != nil {
		t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
	}
	stderr := errb.String()
	want := `no files matched the active extensions (.md, .markdown, .mdx) under ` + dir + ` (profile "markdown")`
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr missing %q, got:\n%s", want, stderr)
	}
	if strings.Contains(stderr, "the configured extensions") {
		t.Errorf("legacy reusables-dir widening must not be reported as a user override, got:\n%s", stderr)
	}
	// The widened list is not the markdown profile's own, so it must not be
	// attributed to the profile — --list-profiles says markdown is .md/.markdown.
	if strings.Contains(stderr, `the "markdown" profile's extensions`) {
		t.Errorf("widened list must not be attributed to the profile, got:\n%s", stderr)
	}

	// A real override on the same run does say "the configured extensions".
	out.Reset()
	errb.Reset()
	if err := runArgs([]string{
		"--content-dir", dir, "--output-dir", outDir, "--reusables-dir", reusables,
		"--extensions", ".adoc",
	}, &out, &errb); err != nil {
		t.Fatalf("runArgs(--extensions): %v\nstderr: %s", err, errb.String())
	}
	if !strings.Contains(errb.String(), `no files matched the configured extensions (.adoc) under `+dir+` (profile "markdown")`) {
		t.Errorf("stderr should describe the overridden extensions, got:\n%s", errb.String())
	}
}

// TestRunArgs_WarnsWhenNoFilesMatch: an .mdx-only tree under the default
// markdown profile scans zero files. That must not pass silently — stderr names
// the profile, its extensions and the knobs to change them — but the run still
// succeeds (exit code semantics are unchanged). When exclusions (not the
// extensions) removed every file, the warning says so instead. See #11.
func TestRunArgs_WarnsWhenNoFilesMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "only.mdx"), []byte("# MDX\n\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "reports")

	var out, errb bytes.Buffer
	if err := runArgs([]string{"--content-dir", dir, "--output-dir", outDir}, &out, &errb); err != nil {
		t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
	}

	if !strings.Contains(out.String(), "Files scanned: 0") {
		t.Errorf("stdout should report zero files scanned, got:\n%s", out.String())
	}
	stderr := errb.String()
	for _, want := range []string{
		`Warning: no files matched the "markdown" profile's extensions (.md, .markdown) under ` + dir,
		"--extensions",
		"--profile",
		"--list-profiles",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q, got:\n%s", want, stderr)
		}
	}

	// With a user override the warning attributes the extensions to the
	// configuration rather than the profile, and still names the profile.
	out.Reset()
	errb.Reset()
	if err := runArgs([]string{"--content-dir", dir, "--output-dir", outDir, "--extensions", ".rst"}, &out, &errb); err != nil {
		t.Fatalf("runArgs(--extensions): %v\nstderr: %s", err, errb.String())
	}
	if !strings.Contains(errb.String(), `no files matched the configured extensions (.rst) under `+dir+` (profile "markdown")`) {
		t.Errorf("stderr should describe the overridden extensions, got:\n%s", errb.String())
	}

	// When the extensions matched but exclusions dropped every file, the
	// warning blames the exclusions rather than the extension allowlist.
	exclDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(exclDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exclDir, "docs", "a.md"), []byte("# A\n\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if err := runArgs([]string{"--content-dir", exclDir, "--output-dir", outDir, "--exclude-dirs", "docs"}, &out, &errb); err != nil {
		t.Fatalf("runArgs(--exclude-dirs): %v\nstderr: %s", err, errb.String())
	}
	if !strings.Contains(out.String(), "Files scanned: 0") {
		t.Errorf("stdout should report zero files scanned, got:\n%s", out.String())
	}
	stderr = errb.String()
	want := `Warning: every directory holding the "markdown" profile's extensions (.md, .markdown) under ` + exclDir +
		" was pruned by exclude_dirs (1 pruned); relax the exclusions to analyze them."
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr missing %q, got:\n%s", want, stderr)
	}
	for _, absent := range []string{"no files matched", "--extensions", "--list-profiles"} {
		if strings.Contains(stderr, absent) {
			t.Errorf("exclusion warning must not carry the extension hint %q, got:\n%s", absent, stderr)
		}
	}

	// And when files do match, no such warning is printed.
	if err := os.WriteFile(filepath.Join(dir, "real.md"), []byte("# MD\n\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if err := runArgs([]string{"--content-dir", dir, "--output-dir", outDir}, &out, &errb); err != nil {
		t.Fatalf("runArgs(with .md): %v\nstderr: %s", err, errb.String())
	}
	if strings.Contains(errb.String(), "no files matched") || strings.Contains(errb.String(), "were skipped by exclude_dirs") {
		t.Errorf("no zero-files warning expected once a file matches, got:\n%s", errb.String())
	}
}

// TestRunArgs_SkippedExtensionsNote covers the partial-scan note (#11): a
// mixed .md/.mdx tree with no Hugo marker runs under the markdown profile and
// silently drops every .mdx, so the CLI says so on stderr — but only when
// something actually was skipped, and only when the run is not already
// covered by the zero-files warning.
func TestRunArgs_SkippedExtensionsNote(t *testing.T) {
	now := time.Now()
	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -200), "v", map[string]string{
		"docs/a.md":      "# A\n\nbody\n",
		"docs/b.mdx":     "# B\n\nbody\n",
		"docs/notes.txt": "not content\n",
		"plain/only.md":  "# Only\n\nbody\n",
		"plain/logo.png": "not really a png\n",
	})

	var out, errb bytes.Buffer
	if err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", filepath.Join(t.TempDir(), "mixed"),
	}, &out, &errb); err != nil {
		t.Fatalf("runArgs(mixed): %v\nstderr: %s", err, errb.String())
	}
	if !strings.Contains(out.String(), "Files scanned: 1") {
		t.Errorf("mixed tree should scan only a.md:\n%s", out.String())
	}
	stderr := errb.String()
	for _, want := range []string{
		"Note: 1 file(s) with extension(s) .mdx were not analyzed",
		`only the "markdown" profile's extensions (.md, .markdown) are scanned`,
		"--extensions",
		"--profile",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}

	// All-.md tree: nothing is skipped, so no note.
	out.Reset()
	errb.Reset()
	if err := runArgs([]string{
		"--content-dir", repo.Path("plain"),
		"--output-dir", filepath.Join(t.TempDir(), "plain"),
	}, &out, &errb); err != nil {
		t.Fatalf("runArgs(plain): %v\nstderr: %s", err, errb.String())
	}
	if strings.Contains(errb.String(), "were not analyzed") {
		t.Errorf("all-.md tree must not print the skipped-extensions note:\n%s", errb.String())
	}

	// Widening the allowlist analyzes both files and silences the note.
	out.Reset()
	errb.Reset()
	if err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", filepath.Join(t.TempDir(), "widened"),
		"--extensions", ".md,.mdx",
	}, &out, &errb); err != nil {
		t.Fatalf("runArgs(widened): %v\nstderr: %s", err, errb.String())
	}
	if !strings.Contains(out.String(), "Files scanned: 2") {
		t.Errorf("widened allowlist should scan both files:\n%s", out.String())
	}
	if strings.Contains(errb.String(), "were not analyzed") {
		t.Errorf("widened allowlist must not print the note:\n%s", errb.String())
	}
}

// TestRunArgs_ZeroFilesSuppressesSkippedNote pins the overlap rule: when
// nothing was scanned, only the zero-files warning is printed — it already
// names the profile, the extensions and both remedies.
func TestRunArgs_ZeroFilesSuppressesSkippedNote(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(time.Now().AddDate(0, 0, -200), "v", map[string]string{
		"docs/only.mdx": "# Only\n\nbody\n",
	})

	var out, errb bytes.Buffer
	if err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", filepath.Join(t.TempDir(), "mdx-only"),
	}, &out, &errb); err != nil {
		t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
	}
	stderr := errb.String()
	if !strings.Contains(stderr, "no files matched") {
		t.Errorf("stderr missing zero-files warning:\n%s", stderr)
	}
	if strings.Contains(stderr, "were not analyzed") {
		t.Errorf("zero-files run must not also print the skipped-extensions note:\n%s", stderr)
	}
}

// TestRunArgs_ExtensionsCanonicalisedInJSONReport pins #11: --extensions takes
// whatever the user typed ("mdx", ".MD"), but the walk matches on lowercase,
// dot-prefixed extensions. ApplyProfile canonicalises the list in place, so the
// JSON report's config.content_extensions echoes the allowlist that was
// actually scanned rather than the raw input.
func TestRunArgs_ExtensionsCanonicalisedInJSONReport(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(time.Now().AddDate(0, 0, -200), "v", map[string]string{
		"docs/page.mdx": "# MDX\n\nbody\n",
		"docs/page.md":  "# MD\n\nbody\n",
	})
	outDir := filepath.Join(t.TempDir(), "reports")

	var out, errb bytes.Buffer
	if err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", outDir,
		"--extensions", "mdx",
	}, &out, &errb); err != nil {
		t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
	}

	// The bare "mdx" must still match page.mdx (and only it).
	if !strings.Contains(out.String(), "Files scanned: 1") {
		t.Errorf("expected exactly the .mdx file to be scanned:\n%s", out.String())
	}

	data, err := os.ReadFile(filepath.Join(outDir, "stale-docs.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Config struct {
			ContentExtensions []string `json:"content_extensions"`
		} `json:"config"`
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("invalid JSON report: %v", err)
	}
	if got := rep.Config.ContentExtensions; len(got) != 1 || got[0] != ".mdx" {
		t.Errorf("config.content_extensions = %v, want [.mdx]", got)
	}
	if len(rep.Files) != 1 || !strings.HasSuffix(rep.Files[0].Path, ".mdx") {
		t.Errorf("report files = %+v, want just the .mdx file", rep.Files)
	}

	// An upper-case, dotted spelling canonicalises the same way.
	out.Reset()
	errb.Reset()
	outDir2 := filepath.Join(t.TempDir(), "reports2")
	if err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", outDir2,
		"--extensions", ".MD",
	}, &out, &errb); err != nil {
		t.Fatalf("runArgs(.MD): %v\nstderr: %s", err, errb.String())
	}
	if !strings.Contains(out.String(), "Files scanned: 1") {
		t.Errorf("expected exactly the .md file to be scanned:\n%s", out.String())
	}
	data, err = os.ReadFile(filepath.Join(outDir2, "stale-docs.json"))
	if err != nil {
		t.Fatal(err)
	}
	rep.Config.ContentExtensions = nil
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("invalid JSON report: %v", err)
	}
	if got := rep.Config.ContentExtensions; len(got) != 1 || got[0] != ".md" {
		t.Errorf("config.content_extensions = %v, want [.md]", got)
	}
}

// TestRunArgs_MintlifyEndToEnd runs the CLI over the committed
// testdata/mintlify-docs fixture (#7): docs.json is auto-detected, the banner
// names the profile and its root, and the JSON report carries the snippet as a
// reusable resolved to a real date — with <Card /> and {{< not-a-reusable >}}
// on the same page deliberately absent.
func TestRunArgs_MintlifyEndToEnd(t *testing.T) {
	now := time.Now()
	repo := testutil.NewRepo(t)
	repo.CommitTree(now.AddDate(0, 0, -300), "import mintlify docs", "mintlify-docs", ".")
	snippetDate := now.AddDate(0, 0, -10)
	repo.Commit(snippetDate, "refresh snippet", map[string]string{
		"snippets/foo.mdx": "Shared snippet body, refreshed.\n",
	})
	outDir := filepath.Join(t.TempDir(), "reports")

	var out, errb bytes.Buffer
	if err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", outDir,
		"--threshold-days", "90",
	}, &out, &errb); err != nil {
		t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
	}

	if want := "Profile: mintlify (auto-detected, root: " + repo.Dir + ")\n"; !strings.Contains(out.String(), want) {
		t.Errorf("banner missing %q:\n%s", want, out.String())
	}

	rep := readJSONReport(t, outDir)
	if rep.Config.Profile != "mintlify" || !rep.Config.ProfileAuto {
		t.Errorf("config profile = %q auto=%v, want mintlify auto-detected", rep.Config.Profile, rep.Config.ProfileAuto)
	}
	if got := rep.Config.ContentExtensions; len(got) != 2 || got[0] != ".md" || got[1] != ".mdx" {
		t.Errorf("config.content_extensions = %v, want [.md .mdx]", got)
	}
	// Two snippet references, both reported under the *resolved* path relative
	// to the project root rather than under the raw capture: that is what makes
	// two pages' same-named relative snippets distinct rows (#7 review). One is
	// written root-absolute, the other as the bare filename real Mintlify
	// projects use, which resolves out of snippets/.
	byName := map[string]jsonReusable{}
	for _, r := range rep.Reusables {
		byName[r.Name] = r
	}
	if len(byName) != 2 {
		t.Fatalf("reusables = %+v, want the two resolved snippet paths", rep.Reusables)
	}
	bare, ok := byName["snippets/aws-access-key-config.mdx"]
	if !ok {
		t.Fatalf("the bare <Snippet file=\"aws-access-key-config.mdx\" /> did not resolve "+
			"out of snippets/: %+v", rep.Reusables)
	}
	if bare.LastUpdated == "" || bare.Level == "unknown" {
		t.Errorf("bare snippet was not resolved to a date: %+v", bare)
	}
	snippet, ok := byName["snippets/foo.mdx"]
	if !ok {
		t.Fatalf("reusables = %+v, want snippets/foo.mdx", rep.Reusables)
	}
	if snippet.LastUpdated == "" || snippet.Level == "unknown" {
		t.Errorf("snippet was not resolved to a date: %+v", snippet)
	}
	if want := snippetDate.Format("2006-01-02"); !strings.HasPrefix(snippet.LastUpdated, want) {
		t.Errorf("snippet last_updated = %q, want it to start with %q", snippet.LastUpdated, want)
	}
	// Everything resolved, so the unresolved-reusables note must stay quiet.
	if strings.Contains(errb.String(), "could not be resolved") {
		t.Errorf("unexpected unresolved-reusables note:\n%s", errb.String())
	}
}

// TestRunArgs_MintlifyExplicitProfile pins --profile mintlify: the banner
// reports it without the "auto-detected" note, and the narrow snippet pattern
// replaces the hugo component pattern even in a repo with no docs.json.
func TestRunArgs_MintlifyExplicitProfile(t *testing.T) {
	now := time.Now()
	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -200), "v", map[string]string{
		"docs/page.mdx": "# Page\n\n<Card />\n\n{{< bar >}}\n",
	})
	outDir := filepath.Join(t.TempDir(), "reports")

	var out, errb bytes.Buffer
	if err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", outDir,
		"--profile", "mintlify",
	}, &out, &errb); err != nil {
		t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
	}
	if !strings.Contains(out.String(), "Profile: mintlify\n") {
		t.Errorf("banner missing explicit mintlify profile:\n%s", out.String())
	}
	rep := readJSONReport(t, outDir)
	if len(rep.Reusables) != 0 {
		t.Errorf("reusables = %+v, want none: <Card /> and {{< bar >}} are not Mintlify snippets", rep.Reusables)
	}
}

// mintlifyConfigJSON is a minimal but realistic Mintlify config: the docs.json
// marker is validated by content as well as by name (#7 review).
const mintlifyConfigJSON = `{"$schema":"https://mintlify.com/docs.json",` +
	`"name":"Docs","theme":"mint","colors":{"primary":"#000"},` +
	`"navigation":{"pages":["docs/page"]}}`

// TestRunArgs_MintlifyNoRootNote covers the diagnostic for a run whose profile
// resolves includes against a project root that was never found: --profile
// mintlify on a tree with no docs.json/mint.json used to report every snippet
// as unknown in silence (#7 review). The note must name the profile and the
// remedy, and must not fire once a root exists.
func TestRunArgs_MintlifyNoRootNote(t *testing.T) {
	now := time.Now()

	run := func(t *testing.T, repo *testutil.Repo, extraArgs ...string) (string, string) {
		t.Helper()
		outDir := filepath.Join(t.TempDir(), "reports")
		args := append([]string{
			"--content-dir", repo.Path("docs"),
			"--output-dir", outDir,
			"--profile", "mintlify",
		}, extraArgs...)
		var out, errb bytes.Buffer
		if err := runArgs(args, &out, &errb); err != nil {
			t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
		}
		return out.String(), errb.String()
	}

	const noteFragment = `profile "mintlify": 1 reusable reference(s) resolved to no file with git history`

	t.Run("no marker: the note fires and snippets stay unknown", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs/page.mdx":    "# Page\n\n<Snippet file=\"/snippets/foo.mdx\" />\n",
			"snippets/foo.mdx": "shared\n",
		})
		_, stderr := run(t, repo)
		if !strings.Contains(stderr, noteFragment) {
			t.Errorf("stderr missing the unresolved-root note:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--project-root") {
			t.Errorf("the note should name --project-root:\n%s", stderr)
		}
		if !strings.Contains(stderr, "docs.json or mint.json") {
			t.Errorf("the note should name the markers it looked for:\n%s", stderr)
		}
	})

	t.Run("a docs.json is present: no note", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs.json":        mintlifyConfigJSON,
			"docs/page.mdx":    "# Page\n\n<Snippet file=\"/snippets/foo.mdx\" />\n",
			"snippets/foo.mdx": "shared\n",
		})
		_, stderr := run(t, repo)
		if strings.Contains(stderr, noteFragment) {
			t.Errorf("the note fired even though a root was found:\n%s", stderr)
		}
	})

	t.Run("--project-root supplies the missing root", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		snippetDate := now.AddDate(0, 0, -3)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs/page.mdx": "# Page\n\n<Snippet file=\"/snippets/foo.mdx\" />\n",
		})
		repo.Commit(snippetDate, "snippet", map[string]string{
			"snippets/foo.mdx": "shared\n",
		})

		outDir := filepath.Join(t.TempDir(), "reports")
		var out, errb bytes.Buffer
		if err := runArgs([]string{
			"--content-dir", repo.Path("docs"),
			"--output-dir", outDir,
			"--profile", "mintlify",
			"--project-root", repo.Dir,
		}, &out, &errb); err != nil {
			t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
		}
		if strings.Contains(errb.String(), noteFragment) {
			t.Errorf("the note fired even though --project-root was given:\n%s", errb.String())
		}
		if want := "Profile: mintlify (root: " + repo.Dir + ")\n"; !strings.Contains(out.String(), want) {
			t.Errorf("banner missing %q:\n%s", want, out.String())
		}
		rep := readJSONReport(t, outDir)
		if len(rep.Reusables) != 1 {
			t.Fatalf("reusables = %+v, want the resolved snippet", rep.Reusables)
		}
		if rep.Reusables[0].Name != "snippets/foo.mdx" || rep.Reusables[0].Level == "unknown" {
			t.Errorf("snippet = %+v, want it resolved under its root-relative path", rep.Reusables[0])
		}
	})
}

// TestRunArgs_ProjectRootConfigAlias pins the config-file spellings of the
// project root: "project_root" is the current name and wins over the legacy
// "hugo_root", which keeps working (#7 review).
func TestRunArgs_ProjectRootConfigAlias(t *testing.T) {
	now := time.Now()
	snippetDate := now.AddDate(0, 0, -3)

	newRepo := func(t *testing.T) *testutil.Repo {
		t.Helper()
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs/page.mdx": "# Page\n\n<Snippet file=\"/snippets/foo.mdx\" />\n",
		})
		repo.Commit(snippetDate, "snippet", map[string]string{
			"snippets/foo.mdx": "shared\n",
		})
		return repo
	}

	runWithConfig := func(t *testing.T, repo *testutil.Repo, cfg map[string]any) jsonReport {
		t.Helper()
		outDir := filepath.Join(t.TempDir(), "reports")
		cfg["content_dir"] = repo.Path("docs")
		cfg["output_dir"] = outDir
		cfg["profile"] = "mintlify"
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		if err := runArgs([]string{"--config", cfgPath}, &out, &errb); err != nil {
			t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
		}
		return readJSONReport(t, outDir)
	}

	t.Run("project_root", func(t *testing.T) {
		repo := newRepo(t)
		rep := runWithConfig(t, repo, map[string]any{"project_root": repo.Dir})
		if len(rep.Reusables) != 1 || rep.Reusables[0].Level == "unknown" {
			t.Errorf("reusables = %+v, want the snippet resolved", rep.Reusables)
		}
	})

	t.Run("legacy hugo_root still works", func(t *testing.T) {
		repo := newRepo(t)
		rep := runWithConfig(t, repo, map[string]any{"hugo_root": repo.Dir})
		if len(rep.Reusables) != 1 || rep.Reusables[0].Level == "unknown" {
			t.Errorf("reusables = %+v, want the snippet resolved", rep.Reusables)
		}
	})

	t.Run("project_root wins over hugo_root", func(t *testing.T) {
		repo := newRepo(t)
		rep := runWithConfig(t, repo, map[string]any{
			"project_root": repo.Dir,
			"hugo_root":    filepath.Join(repo.Dir, "docs"),
		})
		if len(rep.Reusables) != 1 || rep.Reusables[0].Name != "snippets/foo.mdx" {
			t.Errorf("reusables = %+v, want the snippet resolved against the repo root", rep.Reusables)
		}
	})
}

// TestRunArgs_LegacyHugoRootDeprecation covers what the renamed root key does
// for configs written against the old spelling: "hugo_root" still supplies the
// project root, it now says on stderr that the key is deprecated, and it keeps
// the one behaviour the rename would otherwise have broken — a hugo_root
// pointing at a tree with no Hugo marker still selects the hugo profile
// (#7 review pass 3).
func TestRunArgs_LegacyHugoRootDeprecation(t *testing.T) {
	now := time.Now()

	runWithConfig := func(t *testing.T, contentDir string, cfg map[string]any) (string, string) {
		t.Helper()
		cfg["content_dir"] = contentDir
		cfg["output_dir"] = filepath.Join(t.TempDir(), "reports")
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		if err := runArgs([]string{"--config", cfgPath}, &out, &errb); err != nil {
			t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
		}
		return out.String(), errb.String()
	}

	t.Run("a tree with no marker still gets the hugo profile", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs/page.md": "# Page\n\nbody\n",
		})
		stdout, stderr := runWithConfig(t, repo.Path("docs"), map[string]any{"hugo_root": repo.Dir})
		if !strings.Contains(stdout, "Profile: hugo (auto-detected, root: "+repo.Dir+")") {
			t.Errorf("banner = %q, want the legacy hugo fallback", stdout)
		}
		if !strings.Contains(stderr, `"hugo_root" is deprecated`) ||
			!strings.Contains(stderr, `"project_root"`) {
			t.Errorf("stderr should carry the rename notice:\n%s", stderr)
		}
	})

	t.Run("a real Hugo site is detected from its markers, not from the key", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "site", map[string]string{
			"hugo.toml":                    "baseURL = '/'\n",
			"layouts/shortcodes/note.html": "<p>note</p>\n",
			"content/docs/page.md":         "# Page\n\n{{< note >}}\n",
		})
		stdout, stderr := runWithConfig(t, repo.Path("content/docs"), map[string]any{"hugo_root": repo.Dir})
		if !strings.Contains(stdout, "Profile: hugo (auto-detected, root: "+repo.Dir+")") {
			t.Errorf("banner = %q, want hugo at the site root", stdout)
		}
		if !strings.Contains(stderr, `"hugo_root" is deprecated`) {
			t.Errorf("stderr should carry the rename notice:\n%s", stderr)
		}
	})

	// The whole point of the rename: the current spelling says where to
	// resolve from, never what the project is. On a Mintlify tree it used to
	// force the hugo profile, whose component pattern captured "Snippet" as a
	// name and resolved nothing.
	t.Run("project_root does not force hugo on a Mintlify tree", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs.json":           mintlifyConfigJSON,
			"docs/page.mdx":       "# Page\n\n<Snippet file=\"shared.mdx\" />\n",
			"snippets/shared.mdx": "shared\n",
		})
		outDir := filepath.Join(t.TempDir(), "reports")
		var out, errb bytes.Buffer
		if err := runArgs([]string{
			"--content-dir", repo.Path("docs"),
			"--output-dir", outDir,
			"--project-root", repo.Dir,
		}, &out, &errb); err != nil {
			t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
		}
		if !strings.Contains(out.String(), "Profile: mintlify (auto-detected, root: "+repo.Dir+")") {
			t.Errorf("banner = %q, want mintlify", out.String())
		}
		rep := readJSONReport(t, outDir)
		if len(rep.Reusables) != 1 || rep.Reusables[0].Name != "snippets/shared.mdx" ||
			rep.Reusables[0].Level == "unknown" {
			t.Errorf("reusables = %+v, want the bare snippet resolved out of snippets/", rep.Reusables)
		}
		if strings.Contains(errb.String(), "deprecated") {
			t.Errorf("the current spelling must not be deprecated:\n%s", errb.String())
		}
	})

	t.Run("an explicit profile wins over a supplied root", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs/page.mdx": "# Page\n\n<Card />\n",
		})
		outDir := filepath.Join(t.TempDir(), "reports")
		var out, errb bytes.Buffer
		if err := runArgs([]string{
			"--content-dir", repo.Path("docs"),
			"--output-dir", outDir,
			"--project-root", repo.Dir,
			"--profile", "mintlify",
		}, &out, &errb); err != nil {
			t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
		}
		if !strings.Contains(out.String(), "Profile: mintlify (root: "+repo.Dir+")") {
			t.Errorf("banner = %q, want the explicit mintlify profile", out.String())
		}
	})
}

// TestRunArgs_UnresolvedReusablesNote pins what the unresolved-includes note
// means. It used to fire on the resolver alone: a hugo-profile run whose
// includes all resolved through a legacy reusables directory got told its
// references "cannot be resolved" while the very same run's JSON reported them
// resolved, and a tree with no reusable references at all got the note too
// (#7 review pass 2). It then still went silent whenever a root *was* found,
// which hid the far more common failure — a Mintlify project whose root is
// right there and whose snippet paths simply do not resolve. It is now driven
// by the count of unresolved references; a missing root only adds a sentence
// (#7 review pass 3).
func TestRunArgs_UnresolvedReusablesNote(t *testing.T) {
	now := time.Now()

	runIn := func(t *testing.T, args ...string) (string, string, string) {
		t.Helper()
		outDir := filepath.Join(t.TempDir(), "reports")
		var out, errb bytes.Buffer
		if err := runArgs(append(args, "--output-dir", outDir), &out, &errb); err != nil {
			t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
		}
		return out.String(), errb.String(), outDir
	}

	const hugoNote = `profile "hugo": `
	const mintNote = `profile "mintlify": `

	t.Run("legacy reusables-dir resolves everything: no note", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs/page.md":   "# Page\n\n{{< note >}}\n",
			"shared/note.md": "shared\n",
		})
		_, stderr, outDir := runIn(t,
			"--content-dir", repo.Path("docs"),
			"--profile", "hugo",
			"--reusables-dir", repo.Path("shared"),
		)
		if strings.Contains(stderr, hugoNote) {
			t.Errorf("the note fired even though every reference resolved:\n%s", stderr)
		}
		rep := readJSONReport(t, outDir)
		if len(rep.Reusables) != 1 || rep.Reusables[0].Level == "unknown" {
			t.Errorf("reusables = %+v, want the shortcode resolved through the reusables dir", rep.Reusables)
		}
	})

	t.Run("no reusable references at all: no note", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs/page.mdx": "# Page\n\nplain prose, nothing included\n",
		})
		_, stderr, _ := runIn(t,
			"--content-dir", repo.Path("docs"),
			"--profile", "mintlify",
		)
		if strings.Contains(stderr, mintNote) {
			t.Errorf("the note fired on a tree with no reusable references:\n%s", stderr)
		}
	})

	t.Run("no root and references really do fail: the note fires, counts them and names the remedy", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs/page.mdx":    "# Page\n\n<Snippet file=\"/snippets/foo.mdx\" />\n",
			"snippets/foo.mdx": "shared\n",
		})
		_, stderr, _ := runIn(t,
			"--content-dir", repo.Path("docs"),
			"--profile", "mintlify",
		)
		if !strings.Contains(stderr, mintNote) {
			t.Errorf("stderr missing the unresolved-reusables note:\n%s", stderr)
		}
		if !strings.Contains(stderr, "1 reusable reference(s) resolved to no file with git history") {
			t.Errorf("the note should count the failures:\n%s", stderr)
		}
		if !strings.Contains(stderr, ": /snippets/foo.mdx.") {
			t.Errorf("the note should name the capture that failed:\n%s", stderr)
		}
		if !strings.Contains(stderr, "No project root was found") ||
			!strings.Contains(stderr, "--project-root") {
			t.Errorf("with no root the note should say so and name the remedy:\n%s", stderr)
		}
	})

	// The case the root-gated version could not see at all: the root is right
	// there next to docs.json, and the snippets simply do not resolve.
	t.Run("a root was found but references still fail: the note fires without the root sentence", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs.json":     mintlifyConfigJSON,
			"docs/page.mdx": "# Page\n\n<Snippet file=\"typo.mdx\" />\n",
		})
		_, stderr, outDir := runIn(t, "--content-dir", repo.Path("docs"))
		if !strings.Contains(stderr, mintNote) ||
			!strings.Contains(stderr, "1 reusable reference(s) resolved to no file with git history") {
			t.Errorf("stderr missing the unresolved-reusables note:\n%s", stderr)
		}
		if strings.Contains(stderr, "No project root was found") {
			t.Errorf("a root was found; the note must not claim otherwise:\n%s", stderr)
		}
		rep := readJSONReport(t, outDir)
		if len(rep.Reusables) != 1 || rep.Reusables[0].Level != "unknown" {
			t.Errorf("reusables = %+v, want the unresolved snippet reported unknown", rep.Reusables)
		}
	})

	t.Run("everything resolves: no note", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs.json":           mintlifyConfigJSON,
			"docs/page.mdx":       "# Page\n\n<Snippet file=\"shared.mdx\" />\n",
			"snippets/shared.mdx": "shared\n",
		})
		_, stderr, _ := runIn(t, "--content-dir", repo.Path("docs"))
		if strings.Contains(stderr, mintNote) {
			t.Errorf("the note fired even though every reference resolved:\n%s", stderr)
		}
	})

	// The note is scoped to the direct-path resolver. The hugo profile's
	// second pattern captures every capitalised JSX/HTML tag, and none of
	// <Tabs>, <Card>, <Badge> is a shortcode, so on a real MDX site the note
	// counted almost nothing but noise: measured 8 unresolved captures of
	// which exactly 1 was actionable (#7 review pass 4). Those rows are still
	// in the report as level "unknown" — that is where a Hugo user looks.
	t.Run("hugo: unresolvable JSX components print no note", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "site", map[string]string{
			"layouts/shortcodes/note.html": "<div>note</div>\n",
			"content/page.mdx": "# Page\n\n{{< note >}}\n\n" +
				"<Tabs>\n<TabItem>x</TabItem>\n</Tabs>\n\n<Card />\n<Badge />\n",
		})
		_, stderr, outDir := runIn(t, "--content-dir", repo.Path("content"))
		if strings.Contains(stderr, hugoNote) || strings.Contains(stderr, "resolved to no file") {
			t.Errorf("the hugo run must not print the unresolved-reusables note:\n%s", stderr)
		}
		rep := readJSONReport(t, outDir)
		var unknown int
		for _, r := range rep.Reusables {
			if r.Level == "unknown" {
				unknown++
			}
		}
		if unknown == 0 {
			t.Errorf("reusables = %+v, want the unresolvable components still reported unknown",
				rep.Reusables)
		}
	})

	t.Run("mintlify: a broken snippet is named", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs.json":     mintlifyConfigJSON,
			"docs/page.mdx": "# Page\n\n<Snippet file=\"nope.mdx\" />\n",
		})
		_, stderr, _ := runIn(t, "--content-dir", repo.Path("docs"))
		if !strings.Contains(stderr, mintNote) {
			t.Fatalf("stderr missing the unresolved-reusables note:\n%s", stderr)
		}
		if !strings.Contains(stderr, ": nope.mdx.") {
			t.Errorf("the note should name the capture, not only count it:\n%s", stderr)
		}
		if strings.Contains(stderr, "and 0 more") || strings.Contains(stderr, "more.") {
			t.Errorf("a single capture must not be truncated:\n%s", stderr)
		}
	})

	t.Run("mintlify: more than three broken snippets truncate", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs.json": mintlifyConfigJSON,
			"docs/page.mdx": "# Page\n\n<Snippet file=\"a.mdx\" />\n<Snippet file=\"b.mdx\" />\n" +
				"<Snippet file=\"c.mdx\" />\n<Snippet file=\"d.mdx\" />\n<Snippet file=\"e.mdx\" />\n",
		})
		_, stderr, _ := runIn(t, "--content-dir", repo.Path("docs"))
		if !strings.Contains(stderr, "5 reusable reference(s) resolved to no file with git history") {
			t.Errorf("the note should count all five:\n%s", stderr)
		}
		if !strings.Contains(stderr, ": a.mdx, b.mdx, c.mdx and 2 more.") {
			t.Errorf("the note should name three captures and summarise the rest:\n%s", stderr)
		}
		if strings.Contains(stderr, "d.mdx") {
			t.Errorf("the note should not spell out the truncated captures:\n%s", stderr)
		}
	})

	// The second cause the note has to cover: the snippet resolves perfectly
	// well — the report even names it by its resolved path — it has simply
	// never been committed, so git offers no date. The note used to claim it
	// "could not be resolved to a file", which sent the reader looking for a
	// typo that was not there (#7 review pass 4).
	t.Run("mintlify: an uncommitted snippet is described accurately", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs.json":     mintlifyConfigJSON,
			"docs/page.mdx": "# Page\n\n<Snippet file=\"fresh.mdx\" />\n",
		})
		repo.Write("snippets/fresh.mdx", "brand new, never committed\n")

		_, stderr, outDir := runIn(t, "--content-dir", repo.Path("docs"))
		if !strings.Contains(stderr, "resolved to no file with git history") ||
			!strings.Contains(stderr, "has never been committed") {
			t.Errorf("the note must cover the uncommitted-file cause:\n%s", stderr)
		}
		if strings.Contains(stderr, "could not be resolved to a file") {
			t.Errorf("the snippet did resolve; the note must not claim otherwise:\n%s", stderr)
		}
		rep := readJSONReport(t, outDir)
		if len(rep.Reusables) != 1 || rep.Reusables[0].Name != "snippets/fresh.mdx" {
			t.Errorf("reusables = %+v, want the snippet under its resolved path", rep.Reusables)
		}
	})
}

// TestRunArgs_UnusedProjectRootNote: a project root the user supplied that the
// resolved profile never reads used to be validated and then discarded in
// total silence — describeProfile keeps it out of the banner when the profile
// has no root markers, so nothing was printed at all. That is the likeliest
// migration error the project_root rename creates: a markerless Hugo site
// whose config said "hugo_root" got the hugo profile and shortcode tracing,
// and the same site on --project-root gets markdown, no tracing, and a run
// that looks healthy because the .md files still match (#7 review pass 4).
func TestRunArgs_UnusedProjectRootNote(t *testing.T) {
	now := time.Now()

	runIn := func(t *testing.T, args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		args = append(args, "--output-dir", filepath.Join(t.TempDir(), "reports"))
		if err := runArgs(args, &out, &errb); err != nil {
			t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
		}
		return errb.String()
	}

	const noteFragment = "is not used by the"

	t.Run("markdown profile: the note fires and names --profile", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs/page.md": "# Page\n\nprose\n",
		})
		stderr := runIn(t, "--content-dir", repo.Path("docs"), "--project-root", repo.Dir)
		if !strings.Contains(stderr, noteFragment) ||
			!strings.Contains(stderr, `"markdown" profile`) {
			t.Errorf("stderr missing the unused-root note:\n%s", stderr)
		}
		if !strings.Contains(stderr, "--profile") {
			t.Errorf("the note should point at --profile:\n%s", stderr)
		}
	})

	t.Run("a profile that uses the root: no note", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs.json":           mintlifyConfigJSON,
			"docs/page.mdx":       "# Page\n\n<Snippet file=\"shared.mdx\" />\n",
			"snippets/shared.mdx": "shared\n",
		})
		stderr := runIn(t, "--content-dir", repo.Path("docs"), "--project-root", repo.Dir)
		if strings.Contains(stderr, noteFragment) {
			t.Errorf("the root is used by mintlify; the note must stay quiet:\n%s", stderr)
		}
	})

	t.Run("no root supplied: no note", func(t *testing.T) {
		repo := testutil.NewRepo(t)
		repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
			"docs/page.md": "# Page\n\nprose\n",
		})
		stderr := runIn(t, "--content-dir", repo.Path("docs"))
		if strings.Contains(stderr, noteFragment) {
			t.Errorf("no root was supplied; the note must stay quiet:\n%s", stderr)
		}
	})
}

// TestRunArgs_ExplicitRootMustExist: a project root the user named but that is
// not there is a configuration error, not a silently useless run. It used to
// print "Profile: mintlify (root: /does/not/exist)", resolve nothing, report
// every snippet unknown and exit 0 (#7 review pass 2).
func TestRunArgs_ExplicitRootMustExist(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(time.Now().AddDate(0, 0, -200), "docs", map[string]string{
		"docs/page.mdx": "# Page\n\n<Snippet file=\"/snippets/foo.mdx\" />\n",
	})
	missing := filepath.Join(t.TempDir(), "nope")

	var out, errb bytes.Buffer
	err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", filepath.Join(t.TempDir(), "reports"),
		"--profile", "mintlify",
		"--project-root", missing,
	}, &out, &errb)
	if err == nil {
		t.Fatal("runArgs() = nil, want an error for a nonexistent --project-root")
	}
	if !strings.Contains(err.Error(), strconv.Quote(missing)) || !strings.Contains(err.Error(), "--project-root") {
		t.Errorf("error = %v, want it to name the path and the flag", err)
	}
	if strings.Contains(out.String(), "Analyzing documentation") {
		t.Errorf("the run should fail before analysis starts:\n%s", out.String())
	}
}

// TestRunArgs_UnreadableMarkerWarning: a docs.json the process cannot read
// still does not select the mintlify profile, but the user is now told why
// instead of silently getting a markdown run (#7 review pass 2).
func TestRunArgs_UnreadableMarkerWarning(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(time.Now().AddDate(0, 0, -200), "docs", map[string]string{
		"docs.json":     mintlifyConfigJSON,
		"docs/page.mdx": "# Page\n\nbody\n",
	})
	marker := repo.Path("docs.json")
	makeUnreadable(t, marker)

	outDir := filepath.Join(t.TempDir(), "reports")
	var out, errb bytes.Buffer
	if err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", outDir,
	}, &out, &errb); err != nil {
		t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
	}
	// This warning renders the path with %s, not %q, so match it unquoted.
	if !strings.Contains(errb.String(), marker) || !strings.Contains(errb.String(), "mintlify") {
		t.Errorf("stderr should name the unreadable marker and the profile it would have selected:\n%s", errb.String())
	}
	if !strings.Contains(out.String(), "Profile: markdown") {
		t.Errorf("detection must be unchanged (markdown):\n%s", out.String())
	}
}

// TestRunArgs_UncommittedSnippetsStayDistinct: two different files that git
// knows nothing about, referenced by the same relative capture from their own
// pages, must stay two rows. The display name used to fall back to the raw
// capture whenever there was no git info, collapsing them into one "new.mdx"
// row (#7 review pass 2).
func TestRunArgs_UncommittedSnippetsStayDistinct(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(time.Now().AddDate(0, 0, -200), "docs", map[string]string{
		"docs.json":       mintlifyConfigJSON,
		"docs/g/page.mdx": "# G\n\n<Snippet file=\"new.mdx\" />\n",
		"docs/a/page.mdx": "# A\n\n<Snippet file=\"new.mdx\" />\n",
	})
	repo.Write("docs/g/new.mdx", "g snippet\n")
	repo.Write("docs/a/new.mdx", "a snippet\n")

	outDir := filepath.Join(t.TempDir(), "reports")
	var out, errb bytes.Buffer
	if err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", outDir,
	}, &out, &errb); err != nil {
		t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
	}

	rep := readJSONReport(t, outDir)
	names := make([]string, 0, len(rep.Reusables))
	for _, r := range rep.Reusables {
		names = append(names, r.Name)
	}
	sort.Strings(names)
	want := []string{"docs/a/new.mdx", "docs/g/new.mdx"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("reusables = %v, want two distinct rows %v", names, want)
	}
}

// makeUnreadable chmods path so it cannot be read, and skips the test when the
// platform does not honour that. os.Chmod on Windows only toggles the
// read-only attribute, so a 0o000 file is still perfectly readable there, and
// root ignores the mode entirely — in both cases the scenario under test
// cannot be set up at all, which is not a failure of the code.
func makeUnreadable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o000); err != nil {
		t.Skipf("chmod unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skipf("%s is still readable after chmod 000 (Windows, or running as root)", path)
	}
}

// TestRunArgs_ExtensionlessBrokenSnippetNote checks that a broken extensionless
// <Snippet file="…" /> reaches the user. Its capture is capitalised and
// separator-free, so the import map's component rule swallowed it whole: no
// report row, no unresolved count, and — the part a CI user actually sees —
// empty stderr on a run with a genuinely broken include (#68 review).
func TestRunArgs_ExtensionlessBrokenSnippetNote(t *testing.T) {
	now := time.Now()
	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -200), "docs", map[string]string{
		"docs.json":           `{"name":"docs","navigation":[]}`,
		"snippets/shared.mdx": "shared\n",
		"docs/page.mdx": `# Page

<Snippet file="shared" />
<Snippet file="AlsoMissing" />
<Card title="x" />
`,
	})

	outDir := filepath.Join(t.TempDir(), "reports")
	var out, errb bytes.Buffer
	if err := runArgs([]string{
		"--content-dir", repo.Path("docs"), "--output-dir", outDir,
	}, &out, &errb); err != nil {
		t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
	}

	stderr := errb.String()
	if !strings.Contains(stderr, `profile "mintlify": `) {
		t.Fatalf("stderr missing the unresolved-reusables note:\n%s", stderr)
	}
	if !strings.Contains(stderr, "1 reusable reference(s) resolved to no file with git history") {
		t.Errorf("the note should count the one broken include:\n%s", stderr)
	}
	if !strings.Contains(stderr, "AlsoMissing") {
		t.Errorf("the note should name the broken capture:\n%s", stderr)
	}

	rep := readJSONReport(t, outDir)
	var names []string
	for _, r := range rep.Reusables {
		names = append(names, r.Name)
	}
	if !slices.Contains(names, "AlsoMissing") {
		t.Errorf("reusables = %v, want a row for the broken include", names)
	}
	if slices.Contains(names, "Card") {
		t.Errorf("reusables = %v, a built-in component must not earn a row", names)
	}
}

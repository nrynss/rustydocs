package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
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
	for _, name := range []string{"markdown", "hugo"} {
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
	want := `Warning: all 1 file(s) matching the "markdown" profile's extensions (.md, .markdown) under ` + exclDir +
		" were skipped by exclude_dirs / exclude_patterns; relax the exclusions to analyze them."
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

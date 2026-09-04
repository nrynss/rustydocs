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

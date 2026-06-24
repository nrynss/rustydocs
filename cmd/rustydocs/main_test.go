package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

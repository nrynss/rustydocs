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

// TestDescribeDefaultExcludes is the pure half of the #69 note: what it says
// for each combination of counts, and that it says nothing when the defaults
// removed nothing.
func TestDescribeDefaultExcludes(t *testing.T) {
	tests := []struct {
		name    string
		dirs    int
		names   []string
		ignored int
		want    []string // substrings the note must contain
		empty   bool
	}{
		{name: "nothing skipped", empty: true},
		{
			name: "dirs only", dirs: 3, names: []string{".claude", ".git", "node_modules"},
			want: []string{"3 director", "(.claude, .git, node_modules)", "--no-default-excludes"},
		},
		{
			name: "ignored only", ignored: 7,
			want: []string{"7 file(s) ignored by git", "--no-default-excludes"},
		},
		{
			name: "both", dirs: 2, names: []string{".claude", "dist"}, ignored: 5,
			want: []string{"2 director", "(.claude, dist)", "and 5 file(s) ignored by git"},
		},
		{
			// More names than the cap: the rest are summarised, never dumped.
			name: "many names", dirs: 9,
			names: []string{"a", "b", "c", "d", "e", "f", "g"},
			want:  []string{"(a, b, c, d, e and 2 more)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := describeDefaultExcludes(tt.dirs, tt.names, tt.ignored)
			if tt.empty {
				if got != "" {
					t.Fatalf("describeDefaultExcludes() = %q, want \"\"", got)
				}
				return
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("note missing %q:\n%s", w, got)
				}
			}
			// The vendored names are always named so a reader can tell which
			// list is in force.
			if !strings.Contains(got, "node_modules") {
				t.Errorf("note should name the default directory list:\n%s", got)
			}
		})
	}
}

// TestRunArgs_DefaultExcludesNote runs the CLI over a tree that is mostly not
// documentation and checks the headline: the file count is the documentation
// count, the note explains the difference, and --no-default-excludes puts
// everything back.
func TestRunArgs_DefaultExcludesNote(t *testing.T) {
	now := time.Now()
	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -200), "v", map[string]string{
		".gitignore":             "docs/generated.md\n",
		"docs/real.md":           "# Real\n\nbody\n",
		"docs/.claude/notes.md":  "# Notes\n\nbody\n",
		"docs/node_modules/n.md": "# Vendored\n\nbody\n",
		"docs/dist/d.md":         "# Built\n\nbody\n",
	})
	repo.Write("docs/generated.md", "# Generated\n\nbody\n")

	var out, errb bytes.Buffer
	if err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", filepath.Join(t.TempDir(), "default"),
	}, &out, &errb); err != nil {
		t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
	}
	if !strings.Contains(out.String(), "Files scanned: 1") {
		t.Errorf("only real.md should be scanned:\n%s", out.String())
	}
	stderr := errb.String()
	for _, want := range []string{
		"Note: default exclusions skipped 3 director",
		"(.claude, dist, node_modules)",
		"1 file(s) ignored by git",
		"--no-default-excludes",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
	// Nothing survived the walk without history, so no unknown-files warning.
	if strings.Contains(stderr, "had no git history") {
		t.Errorf("default run should produce no unknown files:\n%s", stderr)
	}

	// The override scans everything and prints no exclusion note, because the
	// defaults removed nothing.
	out.Reset()
	errb.Reset()
	if err := runArgs([]string{
		"--content-dir", repo.Path("docs"),
		"--output-dir", filepath.Join(t.TempDir(), "all"),
		"--no-default-excludes",
	}, &out, &errb); err != nil {
		t.Fatalf("runArgs(--no-default-excludes): %v\nstderr: %s", err, errb.String())
	}
	if !strings.Contains(out.String(), "Files scanned: 5") {
		t.Errorf("--no-default-excludes should scan every .md file:\n%s", out.String())
	}
	if strings.Contains(errb.String(), "default exclusions skipped") {
		t.Errorf("--no-default-excludes must print no exclusion note:\n%s", errb.String())
	}
	// And the ignored, uncommitted file is back as an unknown row — the cost
	// the defaults exist to avoid.
	if !strings.Contains(out.String(), "Files with no git history (staleness unknown): 1") {
		t.Errorf("--no-default-excludes should surface the uncommitted file:\n%s", out.String())
	}
}

// TestRunArgs_NoDefaultExcludesConfigKey checks the config-file spelling, and
// that the flag and the key agree.
func TestRunArgs_NoDefaultExcludesConfigKey(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(time.Now().AddDate(0, 0, -200), "v", map[string]string{
		"docs/real.md":         "# Real\n\nbody\n",
		"docs/.cursor/tool.md": "# Tool\n\nbody\n",
	})
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	// json.Marshal so a Windows path's backslashes are escaped correctly.
	doc, err := json.Marshal(map[string]any{
		"content_dir":         repo.Path("docs"),
		"output_dir":          filepath.Join(t.TempDir(), "reports"),
		"no_default_excludes": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, doc, 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if err := runArgs([]string{"--config", cfgPath}, &out, &errb); err != nil {
		t.Fatalf("runArgs: %v\nstderr: %s", err, errb.String())
	}
	if !strings.Contains(out.String(), "Files scanned: 2") {
		t.Errorf("no_default_excludes in config should scan the dot-directory too:\n%s", out.String())
	}
}

package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nrynss/rustydocs/internal/analyzer"
	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/parser"
	"github.com/nrynss/rustydocs/internal/testutil"
)

// TestHugoSupportingFilesStayAuthorized prevents template-derived references
// from exporting external content hashes or freshness, including cached uses.
func TestHugoSupportingFilesStayAuthorized(t *testing.T) {
	for _, kind := range []string{"traversal", "file symlink", "directory symlink", "template symlink", "theme symlink", "partial traversal", "partial symlink", "data symlink", "legacy root is not Hugo permission"} {
		t.Run(kind, func(t *testing.T) {
			repo := testutil.NewRepo(t)
			external := t.TempDir()
			if physical, err := filepath.EvalSymlinks(external); err == nil {
				external = physical
			}
			secret := "outside secret with no Git history\n"
			filename := "secret.txt"
			if strings.HasPrefix(kind, "partial") {
				filename = "secret.html"
			}
			externalFile := filepath.Join(external, filename)
			if err := os.WriteFile(externalFile, []byte(secret), 0600); err != nil {
				t.Fatal(err)
			}
			relative, err := filepath.Rel(repo.Dir, externalFile)
			if err != nil {
				t.Fatal(err)
			}
			template := `{{ readFile "` + filepath.ToSlash(relative) + `" }}`
			if kind == "file symlink" {
				template = `{{ readFile "escaped.txt" }}`
			}
			if kind == "directory symlink" {
				template = `{{ readFile "escaped/secret.txt" }}`
			}
			if kind == "partial traversal" {
				relative, err := filepath.Rel(repo.Path("layouts/partials"), externalFile)
				if err != nil {
					t.Fatal(err)
				}
				template = `{{ partial "` + filepath.ToSlash(relative) + `" }}`
			}
			if kind == "partial symlink" {
				template = `{{ partial "escaped" }}`
			}
			if kind == "data symlink" {
				template = `{{ .Site.Data.escaped }}`
			}
			repo.Commit(daysAgo(200), "old docs", map[string]string{"hugo.toml": "title='test'\n", "content/page.md": "# First\n{{< danger >}}\n\n# Repeat\n{{< danger >}}\n", "layouts/shortcodes/danger.html": template})
			symlink := func(target, path string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("symlinks unsupported: %v", err)
				}
			}
			switch kind {
			case "partial symlink":
				symlink(externalFile, repo.Path("layouts/partials/escaped.html"))
			case "data symlink":
				symlink(externalFile, repo.Path("data/escaped.json"))
			case "file symlink":
				symlink(externalFile, repo.Path("escaped.txt"))
			case "directory symlink":
				symlink(external, repo.Path("escaped"))
			case "template symlink":
				if err := os.Remove(repo.Path("layouts/shortcodes/danger.html")); err != nil {
					t.Fatal(err)
				}
				symlink(externalFile, repo.Path("layouts/shortcodes/danger.html"))
			case "theme symlink":
				if err := os.Remove(repo.Path("layouts/shortcodes/danger.html")); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(external, "layouts", "shortcodes"), 0750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(external, "layouts", "shortcodes", "danger.html"), []byte(secret), 0600); err != nil {
					t.Fatal(err)
				}
				symlink(external, repo.Path("themes/escaped"))
			}
			cfg := config.DefaultConfig()
			cfg.ContentDir = repo.Path("content")
			cfg.Profile = "hugo"
			cfg.ProjectRoot = repo.Dir
			if kind == "legacy root is not Hugo permission" {
				cfg.Reusables.Dir = external
			}
			result, err := analyzer.Analyze(cfg)
			if err != nil {
				t.Fatal(err)
			}
			result.GeneratedAt = fixedNow
			snapshot := buildJSON(result, cfg)
			raw, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), parser.Fingerprint(secret)) || strings.Contains(string(raw), external) {
				t.Fatalf("external content/path exported: %s", raw)
			}
			rejected := 0
			for _, d := range snapshot.Diagnostics {
				if d.Code == "reusable_support_rejected" {
					rejected++
					if d.FileID == nil || d.SectionID == nil || d.Reference == nil || *d.Reference != "danger" || d.Line == nil {
						t.Fatalf("rejection has no consumer context: %+v", d)
					}
				}
			}
			if rejected != 2 {
				t.Fatalf("cached uses lost rejection diagnostics: %d; %+v", rejected, snapshot.Diagnostics)
			}
			for _, s := range snapshot.Files[0].Sections {
				if s.EffectiveLastChange.Date == nil || !s.IsStale || *s.AgeDays != 200 {
					t.Fatalf("outside evidence changed section freshness: %+v", s)
				}
				if s.Dependencies[0].Status == "resolved" {
					t.Fatal("incomplete dependency reported resolved")
				}
			}
		})
	}
}

// TestHugoAuthorizedSupportCoverage retains valid physical in-root symlinks,
// readFile, partial and data fingerprints while qualifying missing evidence.
func TestHugoAuthorizedSupportCoverage(t *testing.T) {
	repo := testutil.NewRepo(t)
	template := `{{ readFile "linked.txt" }} {{ partial "summary" }} {{ .Site.Data.info }} {{ readFile "untracked.txt" }} {{ readFile "missing.txt" }}`
	repo.Commit(daysAgo(200), "old", map[string]string{"hugo.toml": "title='test'\n", "content/page.md": "# First\n{{< safe >}}\n\n# Repeat\n{{< safe >}}\n", "layouts/shortcodes/safe.html": template, "layouts/partials/summary.html": "valid partial\n", "data/info.json": "{}\n", "value.txt": "old value\n"})
	repo.Commit(daysAgo(5), "update data", map[string]string{"value.txt": "fresh value\n"})
	repo.Write("untracked.txt", "authorized unknown history\n")
	if err := os.Symlink(repo.Path("value.txt"), repo.Path("linked.txt")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	result, snapshot := scanJSON(t, repo.Path("content"), "hugo")
	if len(snapshot.Reusables) != 5 {
		t.Fatalf("authorized inventory: %+v", snapshot.Reusables)
	}
	found := map[string]bool{}
	for _, r := range snapshot.Reusables {
		found[r.Name] = true
		if r.ContentFingerprint == "" {
			t.Fatal("valid supporting content lost fingerprint")
		}
	}
	for _, name := range []string{"value.txt", "layouts/partials/summary.html", "data/info.json", "untracked.txt"} {
		if !found[name] {
			t.Fatalf("missing %s: %v", name, found)
		}
	}
	missing, unknown := 0, 0
	for _, d := range snapshot.Diagnostics {
		switch d.Code {
		case "reusable_support_missing":
			missing++
		case "reusable_history_missing":
			unknown++
		}
	}
	if missing != 2 || unknown != 2 {
		t.Fatalf("cached evidence warnings: missing=%d unknown=%d", missing, unknown)
	}
	if snapshot.Coverage.AnalyzedFiles != 1 || snapshot.Coverage.FailedFiles != 0 || snapshot.Summary.FreshSections != 2 {
		t.Fatal(snapshot.Summary, snapshot.Coverage)
	}
	for _, s := range snapshot.Files[0].Sections {
		if *s.AgeDays != 5 || s.Dependencies[0].Status != "partial" {
			t.Fatalf("valid freshness with incomplete support: %+v", s)
		}
	}
	cfg := config.DefaultConfig()
	cfg.ContentDir, cfg.Profile = repo.Path("content"), "hugo"
	for _, format := range []struct {
		name     string
		generate func(*analyzer.Results, *config.Config, string) error
	}{{"html", GenerateHTML}, {"md", GenerateMarkdown}} {
		path := filepath.Join(t.TempDir(), "report."+format.name)
		if err := format.generate(result, cfg, path); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"partial supporting evidence", "reusable_support_missing", "reusable_history_missing"} {
			if !strings.Contains(string(body), want) {
				t.Fatalf("%s hides incomplete evidence %q", format.name, want)
			}
		}
	}
}

func TestHugoAbsoluteSupportReferencesArePortableInJSON(t *testing.T) {
	repo := testutil.NewRepo(t)
	native := filepath.Join(t.TempDir(), "secret.txt")
	references := []string{
		`C:\Users\alice\secret.txt`,
		"C:/Users/alice/secret.txt",
		`\\server\share\secret.txt`,
		`\Users\alice\secret.txt`,
		native,
		`relative\secret.txt`,
	}
	var template strings.Builder
	for _, reference := range references {
		template.WriteString(`{{ readFile "`)
		template.WriteString(reference)
		template.WriteString(`" }}` + "\n")
	}
	repo.Commit(daysAgo(200), "Hugo shortcode references", map[string]string{
		"hugo.toml":                       "title='test'\n",
		"content/page.md":                 "# Security\n{{< inspect >}}\n",
		"layouts/shortcodes/inspect.html": template.String(),
	})
	cfg := config.DefaultConfig()
	cfg.ContentDir, cfg.Profile, cfg.ProjectRoot = repo.Path("content"), "hugo", repo.Dir
	result, err := analyzer.Analyze(cfg)
	if err != nil {
		t.Fatal(err)
	}
	result.GeneratedAt = fixedNow
	snapshot := buildJSON(result, cfg)
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range references[:5] {
		if strings.Contains(string(raw), reference) {
			t.Fatalf("JSON leaked absolute supporting reference %q: %s", reference, raw)
		}
	}
	wantRelative := strconv.Quote(filepath.ToSlash(references[5]))
	absoluteIssues, relativeIssues := 0, 0
	for _, diagnostic := range snapshot.Diagnostics {
		if diagnostic.Code != "reusable_support_missing" && diagnostic.Code != "reusable_support_rejected" {
			continue
		}
		if diagnostic.FileID == nil || diagnostic.SectionID == nil || diagnostic.Reference == nil || *diagnostic.Reference != "inspect" || diagnostic.Line == nil {
			t.Fatalf("support warning lost consumer context: %+v", diagnostic)
		}
		if strings.Contains(diagnostic.Message, "<absolute reference>") {
			absoluteIssues++
		} else if strings.Contains(diagnostic.Message, wantRelative) {
			relativeIssues++
		}
	}
	if absoluteIssues != 1 || relativeIssues != 1 {
		t.Fatalf("portable support issues: absolute=%d relative=%d diagnostics=%+v", absoluteIssues, relativeIssues, snapshot.Diagnostics)
	}
	if len(snapshot.Files) != 1 || len(snapshot.Files[0].Sections) != 1 || len(snapshot.Files[0].Sections[0].Dependencies) != 1 || snapshot.Files[0].Sections[0].Dependencies[0].Status != "partial" {
		t.Fatalf("absolute references changed partial support status: %+v", snapshot.Files)
	}
}

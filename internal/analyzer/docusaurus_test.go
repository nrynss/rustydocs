package analyzer

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/testutil"
)

func TestAnalyze_Docusaurus(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)
	old, recent, newest := now.AddDate(0, 0, -300), now.AddDate(0, 0, -10), now.AddDate(0, 0, -1)
	page := `---
title: Guide
---
import Foo from '../partials/_foo.mdx';
import { Text as Renamed } from '../partials/_text.md';
import Unused from '../partials/_unused.mdx';
import Code from '../src/Card.jsx';
import { Tabs, TabItem } from '@docusaurus/theme-common';
import Site from '@site/partials/_unused.mdx';
import Gone from '../partials/_gone.mdx';
import New from '../partials/_new.mdx';

Welcome before the first heading.
<Foo />

# Uses partials

<Foo />
<Renamed />

## Only components and examples

<Code /><Tabs><TabItem>text</TabItem></Tabs><Site /><Builtin />
` + "\n```mdx\nimport Foo from '../partials/_unused.mdx';\n# Example heading\n<Foo /><Unused />\n```\n\n" + `# Unknown dependencies

<Gone />
<New />
`
	repo := testutil.NewRepo(t)
	repo.Commit(old, "docs", map[string]string{
		"docusaurus.config.js":  "export default {};\n",
		"docs/page.mdx":         page,
		"docs/plain.md":         "import Foo from '../partials/_foo.mdx';\n\n# Plain Markdown\n\n<Foo />\n",
		"docs/ignored.markdown": "# Not in the default extension list\n",
		"partials/_foo.mdx":     "partial body\n",
		"partials/_text.md":     "text body\n",
		"partials/_unused.mdx":  "unused body\n",
		"src/Card.jsx":          "export default () => null;\n",
	})
	repo.Commit(recent, "refresh consumed partials", map[string]string{
		"partials/_foo.mdx": "updated partial\n",
		"partials/_text.md": "updated text\n",
	})
	repo.Commit(newest, "unused and code changes", map[string]string{
		"partials/_unused.mdx": "updated unused partial\n",
		"src/Card.jsx":         "export default () => <div />;\n",
	})
	repo.Write("partials/_new.mdx", "no history yet\n")
	repo.Write("docs/uncommitted.mdx", "# Unknown page\n\nNo history yet.\n")
	for _, selection := range []string{"", config.ProfileDocusaurus} {
		t.Run("profile="+selection, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.ContentDir, cfg.Profile, cfg.ThresholdDays = repo.Path("docs"), selection, 90
			res, err := Analyze(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ResolvedProfile.Name != config.ProfileDocusaurus || res.TotalFiles() != 3 || res.UnresolvedReusables() != 2 {
				t.Fatalf("profile=%q files=%d unresolved=%v", cfg.ResolvedProfile.Name, res.TotalFiles(), res.UnresolvedReusableRefs())
			}
			var sawPage, sawMarkdown, sawUnknown bool
			for _, file := range res.Files {
				if file.RelativePath == "uncommitted.mdx" {
					sawUnknown = true
					if !file.HistoryMissing || file.EffectiveLastUpdated != nil {
						t.Fatalf("historyless page gained freshness: %+v", file)
					}
				}
				if file.RelativePath == "plain.md" {
					sawMarkdown = true
					if len(file.Sections) != 1 || file.Sections[0].IsStale || file.Sections[0].EffectiveLastUpdated == nil ||
						!file.Sections[0].EffectiveLastUpdated.Equal(recent) {
						t.Fatalf("Markdown page did not consume its partial: %+v", file)
					}
				}
				if file.RelativePath != "page.mdx" {
					continue
				}
				sawPage = true
				if len(file.Sections) != 4 {
					t.Fatalf("sections = %+v; want rendered preamble and three headings", file.Sections)
				}
				for i, section := range file.Sections {
					wantDate := old
					if i < 2 {
						wantDate = recent
					}
					if section.EffectiveLastUpdated == nil || !section.EffectiveLastUpdated.Equal(wantDate) || section.IsStale != (i >= 2) {
						t.Errorf("section %q date=%v stale=%v; want %v", section.Title, section.EffectiveLastUpdated, section.IsStale, wantDate)
					}
					for _, d := range section.Dependencies {
						if d.Reference != "Foo" && d.Reference != "Renamed" {
							continue
						}
						wantPath := repo.Path("partials/_foo.mdx")
						if d.Reference == "Renamed" {
							wantPath = repo.Path("partials/_text.md")
						}
						lines := strings.Split(page, "\n")
						if d.Status != "resolved" || len(d.Files) != 1 || d.Files[0].Path != wantPath ||
							d.Files[0].Info == nil || !d.Files[0].Info.LastModified.Equal(recent) ||
							d.Files[0].Fingerprint == "" || d.Column != 2 || !strings.Contains(lines[d.Line-1], "<"+d.Reference) {
							t.Errorf("incorrect consuming-section provenance: %+v", d)
						}
					}
				}
				names := make([]string, 0, len(file.Reusables))
				for _, reusable := range file.Reusables {
					names = append(names, reusable.Name)
					if strings.Contains(reusable.Name, "_gone") || strings.Contains(reusable.Name, "_new") {
						if reusable.LastUpdated != nil || reusable.IsFresh {
							t.Errorf("unknown dependency looks fresh: %+v", reusable)
						}
					}
				}
				want := []string{"partials/_foo.mdx", "partials/_gone.mdx", "partials/_new.mdx", "partials/_text.md"}
				if !reflect.DeepEqual(names, want) {
					t.Errorf("reusables = %v, want %v", names, want)
				}
			}
			if !sawPage || !sawMarkdown || !sawUnknown {
				t.Fatalf("missing expected files: page=%v markdown=%v unknown=%v", sawPage, sawMarkdown, sawUnknown)
			}
		})
	}
}

func TestAnalyze_DocusaurusCommentedImportDoesNotRefresh(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)
	old, recent := now.AddDate(0, 0, -300), now.AddDate(0, 0, -10)
	repo := testutil.NewRepo(t)
	repo.Commit(old, "page and partial", map[string]string{
		"docusaurus.config.js": "export default {};\n",
		"docs/page.mdx": `{/*
import Foo from './_foo.mdx';
*/}

# Guide

<Foo />
`,
		"docs/_foo.mdx": "old partial body\n",
	})
	repo.Commit(recent, "update partial", map[string]string{
		"docs/_foo.mdx": "recent partial body\n",
	})

	cfg := config.DefaultConfig()
	cfg.ContentDir, cfg.Profile, cfg.ThresholdDays = repo.Path("docs"), config.ProfileDocusaurus, 90
	res, err := Analyze(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("analyzed %d files, want page and partial", len(res.Files))
	}
	var pageFound bool
	for _, file := range res.Files {
		if file.RelativePath != "page.mdx" {
			continue
		}
		pageFound = true
		var guideFound bool
		for i := range file.Sections {
			if file.Sections[i].Title == "Guide" {
				guideFound = true
				section := file.Sections[i]
				if section.EffectiveLastUpdated == nil || !section.EffectiveLastUpdated.Equal(old) || !section.IsStale {
					t.Errorf("commented import refreshed section: effective=%v stale=%v; want %v and stale", section.EffectiveLastUpdated, section.IsStale, old)
				}
				for _, reusable := range file.Reusables {
					if reusable.Name == "_foo.mdx" || reusable.Name == "docs/_foo.mdx" {
						t.Errorf("commented import became reusable provenance: %+v", reusable)
					}
				}
				break
			}
		}
		if !guideFound {
			t.Fatalf("Guide section missing from %+v", file.Sections)
		}
	}
	if !pageFound {
		t.Fatal("page.mdx missing from analysis")
	}
}

func TestAnalyze_DocusaurusFrontmatterCommentOpenerPreservesImport(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)
	old, recent := now.AddDate(0, 0, -300), now.AddDate(0, 0, -10)
	repo := testutil.NewRepo(t)
	repo.Commit(old, "page and partial", map[string]string{
		"docusaurus.config.js": "export default {};\n",
		"docs/page.mdx": `---
title: "{/*"
---
import Foo from './_foo.mdx';

# Guide

<Foo />
`,
		"docs/_foo.mdx": "old partial body\n",
	})
	repo.Commit(recent, "update partial", map[string]string{
		"docs/_foo.mdx": "recent partial body\n",
	})

	cfg := config.DefaultConfig()
	cfg.ContentDir, cfg.Profile, cfg.ThresholdDays = repo.Path("docs"), config.ProfileDocusaurus, 90
	res, err := Analyze(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range res.Files {
		if file.RelativePath != "page.mdx" {
			continue
		}
		for _, section := range file.Sections {
			if section.Title == "Guide" {
				if section.EffectiveLastUpdated == nil || !section.EffectiveLastUpdated.Equal(recent) || section.IsStale {
					t.Fatalf("real import lost freshness: effective=%v stale=%v; want %v and fresh", section.EffectiveLastUpdated, section.IsStale, recent)
				}
				for _, dependency := range section.Dependencies {
					if dependency.Reference == "Foo" {
						if dependency.Line != 8 {
							t.Errorf("Foo usage line = %d, want original source line 8", dependency.Line)
						}
						return
					}
				}
				t.Fatal("Guide section did not retain Foo dependency provenance")
			}
		}
		t.Fatalf("Guide section missing from %+v", file.Sections)
	}
	t.Fatal("page.mdx missing from analysis")
}

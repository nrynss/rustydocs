package parser

import (
	"os"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/git"
	"github.com/nrynss/rustydocs/internal/testutil"
)

// starlightRepo builds an Astro Starlight-shaped repository: pages under
// src/content/docs/, local components under src/components/, and distinct
// commit dates per file so a folded date is attributable to exactly one of
// them. The ReusablePatterns are built from the starlight profile's own
// settings rather than hand-copied ones, so a profile change that would break
// resolution shows up here (#18).
type starlightRepo struct {
	repo       *testutil.Repo
	root       string
	rp         *ReusablePatterns
	stepsDate  time.Time
	footerDate time.Time
	astroDate  time.Time
}

func newStarlightRepo(t *testing.T) *starlightRepo {
	t.Helper()
	repo := testutil.NewRepo(t)
	sr := &starlightRepo{
		repo:      repo,
		root:      repo.Dir,
		stepsDate: time.Date(2024, 3, 10, 9, 0, 0, 0, time.UTC),
		// The .astro component is by far the newest file, so any test that
		// wrongly followed a component import would show its date and be
		// caught — the same trick TestResolveReusable_ImportMap uses for .jsx.
		astroDate:  time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC),
		footerDate: time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC),
	}
	repo.Commit(sr.stepsDate, "docs", map[string]string{
		"astro.config.mjs":            "integrations: [starlight()]\n",
		"package.json":                `{"dependencies":{"@astrojs/starlight":"^0.34.0"}}`,
		"src/content/docs/_steps.mdx": "steps body\n",
	})
	repo.Commit(sr.astroDate, "restyle component", map[string]string{
		"src/components/CustomCard.astro": "---\n---\n<div />",
	})
	repo.Commit(sr.footerDate, "add markdoc partial", map[string]string{
		"src/content/docs/_footer.mdoc": "footer body\n",
	})
	p, ok := config.LookupProfile(config.ProfileStarlight)
	if !ok {
		t.Fatal("starlight profile missing from config registry")
	}
	rp, err := NewReusablePatternsFor(ReusableConfig{
		Patterns:   p.ReusablePatterns,
		Extensions: p.ReusableExtensions,
		Root:       repo.Dir,
		Resolver:   p.Resolver,
		Profile:    p.Name,
		ImportMap:  p.ImportMap,
	})
	if err != nil {
		t.Fatalf("NewReusablePatternsFor: %v", err)
	}
	sr.rp = rp
	return sr
}

// page writes an uncommitted page and returns its absolute path. Its own
// history does not matter: these tests ask what its *references* resolve to.
func (sr *starlightRepo) page(rel, body string) string {
	return sr.repo.Write(rel, body)
}

// TestStarlightResolveReusable_Classification is the starlight-profile table:
// what each reference form on a Starlight page resolves to, and — just as
// important — what is skipped rather than reported broken (#18). The skip
// rules are the shared import map's, but the shapes a Starlight page actually
// contains differ from Mintlify's, so they are pinned against the profile's
// own configuration: a local .astro component, the built-ins from the bare
// package specifier, an .mdoc import the map deliberately does not follow,
// and an unimported tag.
func TestStarlightResolveReusable_Classification(t *testing.T) {
	sr := newStarlightRepo(t)

	page := sr.page("src/content/docs/guide.mdx", `---
title: Guide
---

import LocalSteps from "./_steps.mdx";
import CustomCard from "../../components/CustomCard.astro";
import { Tabs, TabItem } from "@astrojs/starlight/components";
import MarkdocPartial from "./_partial.mdoc";
import Gone from "./missing.mdx";

# Guide

<LocalSteps />
<CustomCard />
<Tabs><TabItem>ok</TabItem></Tabs>
<MarkdocPartial />
<Card />
<Gone />
`)

	tests := []struct {
		name string
		ref  string
		want Resolution
		date *time.Time
	}{
		{"page-relative mdx import resolves", "LocalSteps", ResolutionResolved, &sr.stepsDate},
		{"local astro component skipped", "CustomCard", ResolutionSkipped, nil},
		{"built-in from package specifier skipped", "Tabs", ResolutionSkipped, nil},
		{"second built-in from package specifier skipped", "TabItem", ResolutionSkipped, nil},
		{"mdoc import skipped", "MarkdocPartial", ResolutionSkipped, nil},
		// A component used but never imported: a Markdoc preset tag or a
		// built-in on an MDX page, not an include.
		{"unimported component skipped", "Card", ResolutionSkipped, nil},
		// A content import that names no file is a genuine defect.
		{"missing content import unresolved", "Gone", ResolutionUnresolved, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, res := ResolveReusable(tt.ref, page, sr.rp)
			if res != tt.want {
				t.Fatalf("ResolveReusable(%q) resolution = %v, want %v", tt.ref, res, tt.want)
			}
			if tt.date == nil {
				if info != nil {
					t.Fatalf("ResolveReusable(%q) = %+v, want nil info", tt.ref, info)
				}
				return
			}
			if info == nil {
				t.Fatalf("ResolveReusable(%q) = nil info, want a date", tt.ref)
			}
			if !info.LastModified.Equal(*tt.date) {
				t.Errorf("ResolveReusable(%q) date = %s, want %s", tt.ref, info.LastModified, *tt.date)
			}
		})
	}

	// The .astro file really is the newest thing in the repo, so "skipped" is
	// load-bearing rather than incidental: following it would have changed
	// every answer above.
	astro, err := git.GetFileLastModified(sr.repo.Path("src/components/CustomCard.astro"))
	if err != nil || astro == nil {
		t.Fatalf("CustomCard.astro should have history: %v", err)
	}
	if !astro.LastModified.After(sr.stepsDate) {
		t.Fatal("fixture no longer makes the .astro component the newest file")
	}

	// A broken import is reported under the path it named, relative to the
	// project root — not under the bare symbol (#68 review).
	if got := sr.rp.DisplayName("Gone", page, nil); got != "src/content/docs/missing.mdx" {
		t.Errorf("DisplayName(Gone) = %q, want src/content/docs/missing.mdx", got)
	}
}

// TestStarlightMarkdocTagsAreNotUnresolved pins the Markdoc half of the
// contract: in .mdoc pages Starlight's components come from the Markdoc
// preset and are never imported (upstream: "Unlike MDX, components in
// Markdoc do not need to be imported"). Whatever the component pattern
// captures on an .mdoc page must therefore be skipped, never counted as a
// broken include.
func TestStarlightMarkdocTagsAreNotUnresolved(t *testing.T) {
	sr := newStarlightRepo(t)
	body := `# API

<Tabs>
  <TabItem label="a">x</TabItem>
</Tabs>

<Card title="t">c</Card>

<Aside icon="note">a</Aside>
`
	page := sr.page("src/content/docs/api.mdoc", body)
	data, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	refs := FindReusables(string(data), sr.rp)
	if len(refs) == 0 {
		t.Fatal("no components captured on a Markdoc page")
	}
	for _, ref := range refs {
		if _, res := ResolveReusable(ref, page, sr.rp); res != ResolutionSkipped {
			t.Errorf("ResolveReusable(%q) = %v, want ResolutionSkipped", ref, res)
		}
	}
}

// TestStarlightSectionStaleness is the point of the profile: an imported
// partial's date folds into the freshness of the section that renders it,
// while the built-ins — package import, local component, import-free tag —
// fold nothing in. The newest file in the fixture is a restyled .astro
// component, so following any of them would show.
func TestStarlightSectionStaleness(t *testing.T) {
	sr := newStarlightRepo(t)
	body := `import LocalSteps from "./_steps.mdx";
import CustomCard from "../../components/CustomCard.astro";
import { Tabs } from "@astrojs/starlight/components";

# Setup

<LocalSteps />
<Tabs><TabItem>ok</TabItem></Tabs>
<CustomCard />
<Card />
`
	page := sr.page("src/content/docs/guide.mdx", body)

	oldLine := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	section := &Chunk{
		Title: "Setup",
		Reusables: FindReusables(
			"<LocalSteps />\n<Tabs><TabItem>ok</TabItem></Tabs>\n<CustomCard />\n<Card />\n", sr.rp),
		Lines: []git.LineInfo{{LineNumber: 1, Timestamp: oldLine}},
	}
	got := CalculateSectionStaleness(section, page, sr.rp)
	if got == nil {
		t.Fatal("CalculateSectionStaleness = nil")
	}
	if !got.Equal(sr.stepsDate) {
		t.Errorf("effective date = %s, want the imported partial's %s (the .astro component's %s must not be folded in)",
			got, sr.stepsDate, sr.astroDate)
	}
}

// TestStarlightMarkdocPartialIncludes pins the include half of the profile
// (#18 review): Astro's Markdoc integration documents
// {% partial file="./_footer.mdoc" /%} for reusing .mdoc content, so the
// partial's capture is a path — resolved, folded into the section that
// renders it, and counted unresolved when broken — not a component symbol
// the import map may skip. A partial shown inside a fence is an example, not
// a use, and is not captured at all.
func TestStarlightMarkdocPartialIncludes(t *testing.T) {
	sr := newStarlightRepo(t)

	body := "# API\n\n{% partial file=\"./_footer.mdoc\" /%}\n"
	page := sr.page("src/content/docs/api.mdoc", body)
	data, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	refs := FindReusables(string(data), sr.rp)
	if !slicesContains(refs, "./_footer.mdoc") {
		t.Fatalf("FindReusables = %v, want it to contain ./_footer.mdoc", refs)
	}
	info, res := ResolveReusable("./_footer.mdoc", page, sr.rp)
	if res != ResolutionResolved || info == nil {
		t.Fatalf("ResolveReusable(partial) = %v %+v, want resolved", res, info)
	}
	if !info.LastModified.Equal(sr.footerDate) {
		t.Errorf("partial date = %s, want %s", info.LastModified, sr.footerDate)
	}

	// A broken partial came out of an include pattern, so it is unresolved —
	// counted and noted — never skipped, whatever its shape.
	broken := sr.page("src/content/docs/broken.mdoc", "{% partial file='./_gone.mdoc' /%}\n")
	brokenData, err := os.ReadFile(broken)
	if err != nil {
		t.Fatal(err)
	}
	FindReusables(string(brokenData), sr.rp)
	if _, res := ResolveReusable("./_gone.mdoc", broken, sr.rp); res != ResolutionUnresolved {
		t.Errorf("broken partial = %v, want ResolutionUnresolved", res)
	}

	// An example inside a fence neither renders nor resolves — not captured.
	fenced := sr.page("src/content/docs/example.mdoc",
		"# Example\n\n```mdoc\n{% partial file=\"./_gone.mdoc\" /%}\n```\n")
	fencedData, err := os.ReadFile(fenced)
	if err != nil {
		t.Fatal(err)
	}
	if refs := FindReusables(string(fencedData), sr.rp); len(refs) != 0 {
		t.Errorf("fenced partial example captured: %v", refs)
	}

	// The point of the pattern: the partial's date folds into the section
	// that renders it.
	section := &Chunk{
		Title:     "API",
		Reusables: []string{"./_footer.mdoc"},
		Lines:     []git.LineInfo{{LineNumber: 1, Timestamp: sr.stepsDate}},
	}
	if got := CalculateSectionStaleness(section, page, sr.rp); got == nil || !got.Equal(sr.footerDate) {
		t.Errorf("folded staleness = %v, want the partial's %s", got, sr.footerDate)
	}
}

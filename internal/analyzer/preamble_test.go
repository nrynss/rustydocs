package analyzer

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/testutil"
)

// sectionTitles lists a file's section titles in report order.
func sectionTitles(file FileAnalysis) []string {
	out := make([]string, 0, len(file.Sections))
	for _, s := range file.Sections {
		out = append(out, s.Title)
	}
	return out
}

// TestAnalyze_PreambleReusablesAreDetected is the end-to-end proof of #70 on a
// Mintlify-shaped repository: both include forms — an MDX import rendered as
// <McpHowItWorks /> and a literal <Snippet file="…" /> — sit above the first
// header, which used to be discarded wholesale.
//
// The fixture makes the fold observable rather than merely present: the page's
// own lines are ancient, so the only way the preamble can come out fresh is by
// taking a date from an include, and the section below it (which references
// nothing) must stay stale as the control.
func TestAnalyze_PreambleReusablesAreDetected(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	old := now.AddDate(0, 0, -300)
	recent := now.AddDate(0, 0, -10)

	repo := testutil.NewRepo(t)
	repo.Commit(old, "docs", map[string]string{
		"docs.json":                  `{"name":"docs","navigation":[]}`,
		"snippets/how-it-works.mdx":  "how it works\n",
		"snippets/trust-caveats.mdx": "caveats\n",
		"docs/uses-the-preamble.mdx": `---
title: Uses the preamble
---

import McpHowItWorks from "/snippets/how-it-works.mdx";

<McpHowItWorks />
<Snippet file="trust-caveats.mdx" />

## Plain section

Nothing reusable here.
`,
	})
	repo.Commit(recent, "refresh both snippets", map[string]string{
		"snippets/how-it-works.mdx":  "how it works, revised\n",
		"snippets/trust-caveats.mdx": "caveats, revised\n",
	})

	cfg := config.DefaultConfig()
	cfg.ThresholdDays = 90
	cfg.ContentDir = repo.Path("docs")

	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if cfg.ResolvedProfile.Name != config.ProfileMintlify {
		t.Fatalf("profile = %q, want mintlify", cfg.ResolvedProfile.Name)
	}
	if res.TotalFiles() != 1 {
		t.Fatalf("TotalFiles = %d, want 1", res.TotalFiles())
	}
	file := res.Files[0]

	if got := sectionTitles(file); !reflect.DeepEqual(got, []string{"(preamble)", "Plain section"}) {
		t.Fatalf("sections = %v, want [(preamble) Plain section]", got)
	}

	// Both includes are found, and reported under their resolved paths.
	names := make([]string, 0, len(file.Reusables))
	for _, r := range file.Reusables {
		names = append(names, r.Name)
	}
	sort.Strings(names)
	want := []string{"snippets/how-it-works.mdx", "snippets/trust-caveats.mdx"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("reusables = %v, want %v", names, want)
	}
	for _, r := range file.Reusables {
		if r.LastUpdated == nil || !r.LastUpdated.Equal(recent) {
			t.Errorf("reusable %s date = %v, want %s", r.Name, r.LastUpdated, recent)
		}
	}
	if res.UnresolvedReusables() != 0 {
		t.Errorf("UnresolvedReusables = %d (%v), want 0",
			res.UnresolvedReusables(), res.UnresolvedReusableRefs())
	}

	// The preamble's own lines are ancient; the refreshed snippets it renders
	// make it fresh. The section below it references nothing and stays stale.
	staleTitles := make([]string, 0, len(file.StaleSections))
	for _, s := range file.StaleSections {
		staleTitles = append(staleTitles, s.Title)
	}
	if !reflect.DeepEqual(staleTitles, []string{"Plain section"}) {
		t.Errorf("stale sections = %v, want [Plain section]", staleTitles)
	}
}

// TestAnalyze_PreambleBrokenReferenceIsReported is the other half: before #70 a
// snippet reference that named nothing was invisible when it sat in a preamble,
// so the page looked clean. It must now be counted like any other broken
// include.
func TestAnalyze_PreambleBrokenReferenceIsReported(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -300), "docs", map[string]string{
		"docs.json": `{"name":"docs","navigation":[]}`,
		"docs/page.mdx": `---
title: Page
---

<Snippet file="gone.mdx" />

## Plain section

body
`,
	})

	cfg := config.DefaultConfig()
	cfg.ThresholdDays = 90
	cfg.ContentDir = repo.Path("docs")

	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got := res.UnresolvedReusables(); got != 1 {
		t.Fatalf("UnresolvedReusables = %d (%v), want 1",
			got, res.UnresolvedReusableRefs())
	}
	if got := res.UnresolvedReusableRefs(); !reflect.DeepEqual(got, []string{"gone.mdx"}) {
		t.Errorf("UnresolvedReusableRefs = %v, want [gone.mdx]", got)
	}
}

// TestAnalyze_PreambleBlameDates checks the plain-prose case: a preamble with
// no includes at all still contributes its own blame dates, independently of
// the sections around it.
func TestAnalyze_PreambleBlameDates(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	old := now.AddDate(0, 0, -300)
	recent := now.AddDate(0, 0, -5)

	repo := testutil.NewRepo(t)
	repo.Commit(old, "first", map[string]string{
		"docs/page.md": "Intro prose.\n\n# Heading\n\nbody\n",
	})
	// Rewrite only the section body; the preamble keeps the ancient date.
	repo.Commit(recent, "revise the body", map[string]string{
		"docs/page.md": "Intro prose.\n\n# Heading\n\nbody, revised\n",
	})

	cfg := config.DefaultConfig()
	cfg.ThresholdDays = 90
	cfg.ContentDir = repo.Path("docs")

	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	file := res.Files[0]
	if got := sectionTitles(file); !reflect.DeepEqual(got, []string{"(preamble)", "Heading"}) {
		t.Fatalf("sections = %v, want [(preamble) Heading]", got)
	}
	if got := file.Sections[0].LastUpdated(); got == nil || !got.Equal(old) {
		t.Errorf("preamble LastUpdated = %v, want %s", got, old)
	}
	if got := file.Sections[1].LastUpdated(); got == nil || !got.Equal(recent) {
		t.Errorf("section LastUpdated = %v, want %s", got, recent)
	}
	staleTitles := make([]string, 0, len(file.StaleSections))
	for _, s := range file.StaleSections {
		staleTitles = append(staleTitles, s.Title)
	}
	if !reflect.DeepEqual(staleTitles, []string{"(preamble)"}) {
		t.Errorf("stale sections = %v, want [(preamble)] — the whole point of #70", staleTitles)
	}
}

// TestAnalyze_PreambleParagraphLevel checks --paragraph-level splits a
// multi-paragraph preamble the way it splits a section, each paragraph carrying
// its own date.
func TestAnalyze_PreambleParagraphLevel(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	old := now.AddDate(0, 0, -300)
	recent := now.AddDate(0, 0, -5)

	repo := testutil.NewRepo(t)
	repo.Commit(old, "first", map[string]string{
		"docs/page.md": "---\ntitle: Page\n---\n\nFirst paragraph.\n\nSecond paragraph.\n\n# Heading\n\nbody\n",
	})
	repo.Commit(recent, "revise the second preamble paragraph", map[string]string{
		"docs/page.md": "---\ntitle: Page\n---\n\nFirst paragraph.\n\nSecond paragraph, revised.\n\n# Heading\n\nbody\n",
	})

	cfg := config.DefaultConfig()
	cfg.ThresholdDays = 90
	cfg.ParagraphLevel = true
	cfg.ContentDir = repo.Path("docs")

	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	file := res.Files[0]
	want := []string{"(preamble) (L5)", "(preamble) (L7)", "Heading", "Heading (L11)"}
	if got := sectionTitles(file); !reflect.DeepEqual(got, want) {
		t.Fatalf("sections = %v, want %v", got, want)
	}
	if got := file.Sections[0].LastUpdated(); got == nil || !got.Equal(old) {
		t.Errorf("first preamble paragraph = %v, want %s", got, old)
	}
	if got := file.Sections[1].LastUpdated(); got == nil || !got.Equal(recent) {
		t.Errorf("second preamble paragraph = %v, want %s", got, recent)
	}
}

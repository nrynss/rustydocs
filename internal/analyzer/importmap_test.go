package analyzer

import (
	"reflect"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/testutil"
)

// TestAnalyze_MintlifyImportMap is the end-to-end proof of #68: a
// Mintlify-shaped repository whose reusable references are MDX imports — the
// form that actually appears in the wild, and that reported nothing before —
// now folds the imported snippet's date into the including section's staleness.
//
// The fixture is built so that every rule is load-bearing:
//
//   - page.mdx's own prose is ancient, so a section that looks fresh can only
//     have got its date from an include;
//   - the imported snippet is recent, so the section that renders it is fresh
//     and the one that does not stays stale;
//   - the imported .jsx component is the newest file in the repository, so
//     following it would make *every* section fresh and the test would fail
//     loudly rather than quietly.
func TestAnalyze_MintlifyImportMap(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	old := now.AddDate(0, 0, -300)
	recent := now.AddDate(0, 0, -10)
	newest := now.AddDate(0, 0, -1)

	repo := testutil.NewRepo(t)
	repo.Commit(old, "docs", map[string]string{
		"docs.json":               `{"name":"docs","navigation":[]}`,
		"snippets/shared.mdx":     "shared body\n",
		"snippets/yaml-table.jsx": "export const YamlTable = () => null;\n",
		"docs/page.mdx": `import Shared from "/snippets/shared.mdx";
import { YamlTable } from "/snippets/yaml-table.jsx";

# Uses the snippet

<Shared />
<Card title="built-in" />

# Uses only a component

<YamlTable />
<Tabs>x</Tabs>
`,
	})
	repo.Commit(recent, "refresh the snippet", map[string]string{
		"snippets/shared.mdx": "shared body, revised\n",
	})
	// Newest of all, and imported by the page: the date that must NOT appear.
	repo.Commit(newest, "restyle the component", map[string]string{
		"snippets/yaml-table.jsx": "export const YamlTable = () => <table />;\n",
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

	// One reusable, reported under the resolved path rather than the symbol.
	names := make([]string, 0, len(file.Reusables))
	for _, r := range file.Reusables {
		names = append(names, r.Name)
	}
	if !reflect.DeepEqual(names, []string{"snippets/shared.mdx"}) {
		t.Errorf("reusables = %v, want [snippets/shared.mdx]", names)
	}
	if got := file.Reusables[0].LastUpdated; got == nil || !got.Equal(recent) {
		t.Errorf("reusable date = %v, want %s", got, recent)
	}
	if !file.Reusables[0].IsFresh {
		t.Error("the refreshed snippet should be fresh")
	}

	// The section rendering the import is fresh; the one rendering only
	// skipped components keeps the page's own ancient date.
	if len(file.Sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(file.Sections))
	}
	if len(file.StaleSections) != 1 {
		t.Fatalf("StaleSections = %d, want 1 (only the component-only section)",
			len(file.StaleSections))
	}
	if got := file.StaleSections[0].Title; got != "Uses only a component" {
		t.Errorf("stale section = %q, want %q", got, "Uses only a component")
	}

	// Nothing is reported broken: <Card>, <Tabs> and the .jsx import are out of
	// scope by design, and the only real include resolved.
	if res.UnresolvedReusables() != 0 {
		t.Errorf("UnresolvedReusables = %d (%v), want 0",
			res.UnresolvedReusables(), res.UnresolvedReusableRefs())
	}
	// The component's date is newer than everything; if it had leaked in, the
	// file's effective date would be it.
	if file.EffectiveLastUpdated != nil && file.EffectiveLastUpdated.Equal(newest) {
		t.Error("the .jsx component's date leaked into the file's freshness")
	}
}

// TestAnalyze_MintlifyBrokenImportIsReported is the other half: a content
// import that names no file is a genuine defect and must still be counted, so
// the skipping rules cannot be used to explain away real breakage.
func TestAnalyze_MintlifyBrokenImportIsReported(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -300), "docs", map[string]string{
		"docs.json": `{"name":"docs","navigation":[]}`,
		"docs/page.mdx": `import Gone from "/snippets/missing.mdx";
import { YamlTable } from "/snippets/yaml-table.jsx";

# Heading

<Gone />
<YamlTable />
<Card />
`,
	})

	cfg := config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")
	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if res.UnresolvedReusables() != 1 {
		t.Fatalf("UnresolvedReusables = %d (%v), want 1",
			res.UnresolvedReusables(), res.UnresolvedReusableRefs())
	}
	if got := res.UnresolvedReusableRefs(); !reflect.DeepEqual(got, []string{"Gone"}) {
		t.Errorf("UnresolvedReusableRefs = %v, want [Gone]", got)
	}
}

// TestAnalyze_ExtensionlessBrokenSnippetIsReported is the end-to-end form of
// the provenance rule (#68 review). An extensionless <Snippet file="…" />
// capture is capitalised and separator-free — the shape the import map calls a
// component — but it is an include, and a broken one has to be reported. It
// was instead classified *skipped*, which removed it from the report, from the
// unresolved count and from the stderr note in one go: a regression against
// shipped #7, where this exact page reported the reference unresolved.
func TestAnalyze_ExtensionlessBrokenSnippetIsReported(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -300), "docs", map[string]string{
		"docs.json":           `{"name":"docs","navigation":[]}`,
		"snippets/shared.mdx": "shared body\n",
		"docs/page.mdx": `# Heading

<Snippet file="shared" />
<Snippet file="AlsoMissing" />
<Card title="a built-in" />
`,
	})

	cfg := config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")
	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got := res.UnresolvedReusableRefs(); !reflect.DeepEqual(got, []string{"AlsoMissing"}) {
		t.Errorf("UnresolvedReusableRefs = %v, want [AlsoMissing]", got)
	}
	if res.UnresolvedReusables() != 1 {
		t.Errorf("UnresolvedReusables = %d, want 1", res.UnresolvedReusables())
	}

	// It also earns a row, reported unknown — "skipped" meant no row at all.
	if res.TotalFiles() != 1 {
		t.Fatalf("TotalFiles = %d, want 1", res.TotalFiles())
	}
	var broken, resolved bool
	for _, r := range res.Files[0].Reusables {
		switch r.Name {
		case "AlsoMissing":
			broken = true
			if r.LastUpdated != nil {
				t.Errorf("the broken include has a date: %+v", r)
			}
		case "snippets/shared.mdx":
			resolved = true
		case "Card":
			t.Errorf("a built-in component earned a row: %+v", r)
		}
	}
	if !broken {
		t.Errorf("reusables = %+v, want a row for the broken include", res.Files[0].Reusables)
	}
	if !resolved {
		t.Errorf("reusables = %+v, want the extensionless snippet that does exist to resolve",
			res.Files[0].Reusables)
	}
}

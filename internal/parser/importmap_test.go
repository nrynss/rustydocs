package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/git"
	"github.com/nrynss/rustydocs/internal/testutil"
)

// TestImportedSymbols covers the clause parser: which local bindings each
// import form introduces, and that unrecognised junk binds nothing rather than
// being guessed at.
func TestImportedSymbols(t *testing.T) {
	tests := []struct {
		name   string
		clause string
		want   []string
	}{
		{"default", "Button", []string{"Button"}},
		{"named one", "{ Alpha }", []string{"Alpha"}},
		{"named several", "{ Alpha, Beta, Gamma }", []string{"Alpha", "Beta", "Gamma"}},
		{"named with alias", "{ Alpha as A, Beta }", []string{"A", "Beta"}},
		{"default plus named", "Default, { Alpha }", []string{"Alpha", "Default"}},
		{"namespace alias", "* as NS", []string{"NS"}},
		{"named multiline", "{\n\tAlpha,\n\tBeta,\n}", []string{"Alpha", "Beta"}},
		{"trailing comma", "{ Alpha, }", []string{"Alpha"}},
		{"empty braces", "{}", nil},
		{"bare star binds nothing", "*", nil},
		{"junk", "{ 1nvalid, ok }", []string{"ok"}},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := importedSymbols(tt.clause)
			sort.Strings(got)
			want := append([]string(nil), tt.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("importedSymbols(%q) = %v, want %v", tt.clause, got, want)
			}
		})
	}
}

// TestIsImportableContent pins the load-bearing rule of #68: .md and .mdx are
// followed, every other asset kind is skipped, and a package specifier is never
// mistaken for an extensionless file.
func TestIsImportableContent(t *testing.T) {
	content := []string{
		"/snippets/a.mdx", "./b.md", "../c.MDX", "/snippets/deep/d.mdx", "./no-extension",
	}
	for _, ref := range content {
		if !isImportableContent(ref) {
			t.Errorf("isImportableContent(%q) = false, want true", ref)
		}
	}
	notContent := []string{
		"/snippets/yaml-table.jsx", "./widget.js", "/styles/main.css",
		"/snippets/a.tsx", "./data.json", "/img/x.svg",
	}
	for _, ref := range notContent {
		if isImportableContent(ref) {
			t.Errorf("isImportableContent(%q) = true, want false", ref)
		}
	}
	for _, ref := range []string{"react", "@mintlify/components", "@astrojs/starlight/components"} {
		if !isBareModuleSpecifier(ref) {
			t.Errorf("isBareModuleSpecifier(%q) = false, want true", ref)
		}
	}
	for _, ref := range []string{"/snippets/a.mdx", "./a.mdx", "../a.mdx"} {
		if isBareModuleSpecifier(ref) {
			t.Errorf("isBareModuleSpecifier(%q) = true, want false", ref)
		}
	}
}

// importRepo builds a Mintlify-shaped repository: a root with snippets/, a
// page directory, and distinct commit dates per file so a folded date is
// attributable to exactly one of them.
//
// Naming rule for these fixtures: a test that asserts a symbol must NOT
// resolve has to pick a symbol that cannot case-collide with any file below,
// because "Shared" and snippets/shared.mdx are the same path on a
// case-insensitive filesystem and such a test would then be asserting the
// resolver's case rules rather than its own subject. Resolution is case-exact
// on every platform (see caseExactUnder and TestResolveDirectPath_CaseExact),
// so the collision no longer decides the outcome — but a test whose intent
// depends on that second mechanism is a test that says two things at once.
type importRepo struct {
	repo        *testutil.Repo
	root        string
	rp          *ReusablePatterns
	snippetDate time.Time
	otherDate   time.Time
	jsxDate     time.Time
}

func newImportRepo(t *testing.T) *importRepo {
	t.Helper()
	repo := testutil.NewRepo(t)
	ir := &importRepo{
		repo:        repo,
		root:        repo.Dir,
		snippetDate: time.Date(2024, 3, 10, 9, 0, 0, 0, time.UTC),
		otherDate:   time.Date(2024, 7, 21, 9, 0, 0, 0, time.UTC),
		jsxDate:     time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC),
	}
	repo.Commit(ir.snippetDate, "snippets", map[string]string{
		"docs.json":                `{"name":"x","navigation":[]}`,
		"snippets/shared.mdx":      "shared body\n",
		"snippets/other.mdx":       "other body\n",
		"snippets/legacy.md":       "legacy body\n",
		"guides/local-partial.mdx": "local body\n",
	})
	repo.Commit(ir.otherDate, "touch other", map[string]string{
		"snippets/other.mdx": "other body, revised\n",
	})
	// The React component is by far the newest file, so any test that wrongly
	// followed a .jsx import would show its date and be caught.
	repo.Commit(ir.jsxDate, "restyle component", map[string]string{
		"snippets/yaml-table.jsx": "export const YamlTable = () => null;\n",
	})
	ir.rp = newPathRP(t, repo.Dir)
	return ir
}

// page writes an uncommitted page and returns its absolute path. Its own
// history does not matter: these tests ask what its *imports* resolve to.
func (ir *importRepo) page(rel, body string) string {
	return ir.repo.Write(rel, body)
}

// TestResolveReusable_ImportMap is the core table: what each import form and
// each usage resolves to, and — just as important — what is skipped rather than
// reported broken (#68).
func TestResolveReusable_ImportMap(t *testing.T) {
	ir := newImportRepo(t)

	page := ir.page("guides/page.mdx", `---
title: Guide
---

import Shared from "/snippets/shared.mdx";
import { Other } from "/snippets/other.mdx";
import { Alpha, Beta } from "/snippets/shared.mdx";
import Legacy from "/snippets/legacy.md";
import Local from "./local-partial.mdx";
import Renamed from "/snippets/other.mdx";
import { YamlTable } from "/snippets/yaml-table.jsx";
import Widget from "/snippets/widget.js";
import "/styles/main.css";
import { Card } from "@mintlify/components";
import Gone from "/snippets/missing.mdx";
import Unused from "/snippets/shared.mdx";

# Heading

<Shared />
<Other />
<Card title="x" />
`)

	tests := []struct {
		name string
		ref  string
		want Resolution
		date *time.Time
	}{
		{"default import", "Shared", ResolutionResolved, &ir.snippetDate},
		{"named import", "Other", ResolutionResolved, &ir.otherDate},
		{"first of several named symbols", "Alpha", ResolutionResolved, &ir.snippetDate},
		{"second of several named symbols", "Beta", ResolutionResolved, &ir.snippetDate},
		{"md import", "Legacy", ResolutionResolved, &ir.snippetDate},
		{"page-relative import", "Local", ResolutionResolved, &ir.snippetDate},
		{"two symbols for one file", "Renamed", ResolutionResolved, &ir.otherDate},
		// Imported but never used: resolvable on demand, and simply never asked
		// about by a section, which is why nothing below asserts a usage.
		{"unused import still resolves", "Unused", ResolutionResolved, &ir.snippetDate},

		// The rule the issue exists for.
		{"jsx import skipped", "YamlTable", ResolutionSkipped, nil},
		{"js import skipped", "Widget", ResolutionSkipped, nil},
		{"package import skipped", "Card", ResolutionSkipped, nil},
		// A component used but never imported: a layout built-in, not an
		// include.
		{"unimported component skipped", "Tabs", ResolutionSkipped, nil},
		{"another unimported component", "Accordion", ResolutionSkipped, nil},

		// A content import that names no file is a genuine defect.
		{"missing content import", "Gone", ResolutionUnresolved, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, res := ResolveReusable(tt.ref, page, ir.rp)
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
				t.Errorf("ResolveReusable(%q) date = %s, want %s",
					tt.ref, info.LastModified, *tt.date)
			}
		})
	}

	// The .jsx file really is the newest thing in the repo, so "skipped" is
	// load-bearing rather than incidental: following it would have changed
	// every answer above.
	jsx, err := git.GetFileLastModified(ir.repo.Path("snippets/yaml-table.jsx"))
	if err != nil || jsx == nil {
		t.Fatalf("yaml-table.jsx should have history: %v", err)
	}
	if !jsx.LastModified.After(ir.otherDate) {
		t.Fatal("fixture no longer makes the component the newest file")
	}
}

// TestResolveReusable_SnippetAndImportTogether pins the layering: the import
// map sits on top of the existing path resolver, so <Snippet file="…" /> keeps
// resolving on the very same page as imports (#68).
func TestResolveReusable_SnippetAndImportTogether(t *testing.T) {
	ir := newImportRepo(t)
	page := ir.page("guides/mixed.mdx", `import Shared from "/snippets/shared.mdx";

# Mixed

<Shared />
<Snippet file="other.mdx" />
`)

	body, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	refs := FindReusables(string(body), ir.rp)
	// Both kinds of reference are detected: the snippet path and the symbol.
	for _, want := range []string{"other.mdx", "Shared"} {
		if !contains(refs, want) {
			t.Errorf("FindReusables = %v, missing %q", refs, want)
		}
	}

	shared, res := ResolveReusable("Shared", page, ir.rp)
	if res != ResolutionResolved || shared == nil || !shared.LastModified.Equal(ir.snippetDate) {
		t.Errorf("import symbol: %v %+v", res, shared)
	}
	// A bare snippet path resolves under <root>/snippets/, unchanged by #68.
	snip, res := ResolveReusable("other.mdx", page, ir.rp)
	if res != ResolutionResolved || snip == nil || !snip.LastModified.Equal(ir.otherDate) {
		t.Errorf("snippet path: %v %+v", res, snip)
	}
}

// TestResolveReusable_ImportMapOffKeepsOldBehaviour checks that the layer is
// opt-in: with ImportMap disabled (every profile but mintlify today) a
// component capture is still an unresolved reference, so hugo's reporting is
// untouched.
func TestResolveReusable_ImportMapOffKeepsOldBehaviour(t *testing.T) {
	ir := newImportRepo(t)
	// SharedPartial, not Shared: the symbol must not double as a file name in
	// the fixture, or "stays unresolved" could pass for the wrong reason.
	page := ir.page("guides/off.mdx",
		"import SharedPartial from \"/snippets/shared.mdx\";\n\n<SharedPartial />\n")

	rp, err := NewReusablePatternsFor(ReusableConfig{
		Patterns:   mintlifyPatternStrings,
		Extensions: []string{".mdx", ".md"},
		Root:       ir.root,
		Resolver:   config.ResolverPath,
		ImportMap:  false,
	})
	if err != nil {
		t.Fatal(err)
	}
	info, res := ResolveReusable("SharedPartial", page, rp)
	if info != nil || res != ResolutionUnresolved {
		t.Errorf("without the import map, a symbol must stay unresolved; got %v %+v", res, info)
	}
}

// TestCalculateSectionStaleness_ImportFolding is the point of the whole
// feature: an imported snippet's date folds into the freshness of the section
// that renders it, and a skipped .jsx import does not.
func TestCalculateSectionStaleness_ImportFolding(t *testing.T) {
	ir := newImportRepo(t)
	page := ir.page("guides/fold.mdx", `import Other from "/snippets/other.mdx";
import { YamlTable } from "/snippets/yaml-table.jsx";

# Folding

<Other />
<YamlTable />
`)

	oldLine := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	section := &Chunk{
		Title:     "Folding",
		Reusables: FindReusables("<Other />\n<YamlTable />\n", ir.rp),
		Lines:     []git.LineInfo{{LineNumber: 1, Timestamp: oldLine}},
	}
	got := CalculateSectionStaleness(section, page, ir.rp)
	if got == nil {
		t.Fatal("CalculateSectionStaleness = nil")
	}
	if !got.Equal(ir.otherDate) {
		t.Errorf("effective date = %s, want the imported snippet's %s "+
			"(the .jsx component's %s must not be folded in)",
			got, ir.otherDate, ir.jsxDate)
	}
}

// TestDisplayName_ImportedSymbol checks the reporting label: an imported
// snippet is reported under its path relative to the project root, not under
// the local symbol, which differs per page.
func TestDisplayName_ImportedSymbol(t *testing.T) {
	ir := newImportRepo(t)
	page := ir.page("guides/name.mdx", `import Alias from "/snippets/shared.mdx";
import Fresh from "./brand-new.mdx";
import { YamlTable } from "/snippets/yaml-table.jsx";
`)
	// An uncommitted snippet: resolvable, but with no git info to derive a
	// path from, so the label has to come from the import map.
	ir.page("guides/brand-new.mdx", "new\n")

	info, _ := ResolveReusable("Alias", page, ir.rp)
	if got := ir.rp.DisplayName("Alias", page, info); got != "snippets/shared.mdx" {
		t.Errorf("DisplayName(committed import) = %q, want snippets/shared.mdx", got)
	}
	if got := ir.rp.DisplayName("Fresh", page, nil); got != "guides/brand-new.mdx" {
		t.Errorf("DisplayName(uncommitted import) = %q, want guides/brand-new.mdx", got)
	}
	// A skipped import names no file, so the raw capture stays.
	if got := ir.rp.DisplayName("YamlTable", page, nil); got != "YamlTable" {
		t.Errorf("DisplayName(skipped import) = %q, want the raw capture", got)
	}
}

// TestImportMap_UnreadablePageIsEmpty covers the degenerate inputs: a page that
// cannot be read, and no source file at all. Both mean "nothing was imported",
// so every component on them is out of scope rather than broken.
func TestImportMap_UnreadablePageIsEmpty(t *testing.T) {
	ir := newImportRepo(t)
	for _, src := range []string{"", filepath.Join(ir.root, "does-not-exist.mdx")} {
		if got := ir.rp.importsFor(src); len(got) != 0 {
			t.Errorf("importsFor(%q) = %v, want empty", src, got)
		}
		// SharedPartial for the same reason as above: no fixture file can be
		// mistaken for it under any case-folding rule.
		if _, res := ResolveReusable("SharedPartial", src, ir.rp); res != ResolutionSkipped {
			t.Errorf("ResolveReusable with source %q = %v, want ResolutionSkipped", src, res)
		}
	}
}

// TestImportMap_Memoized checks that the map is built once per source file: it
// is read from disk, and a section-by-section rebuild would re-read the page
// for every reference on it.
func TestImportMap_Memoized(t *testing.T) {
	ir := newImportRepo(t)
	page := ir.page("guides/memo.mdx", "import Shared from \"/snippets/shared.mdx\";\n")

	first := ir.rp.importsFor(page)
	second := ir.rp.importsFor(page)
	if len(first) != 1 {
		t.Fatalf("importsFor = %v, want one symbol", first)
	}
	if reflect.ValueOf(first).Pointer() != reflect.ValueOf(second).Pointer() {
		t.Error("importsFor rebuilt the map instead of memoizing it")
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// TestResolveReusable_BrokenSnippetCaptureIsNeverSkipped is the regression test
// for the provenance rule (#68 review). <Snippet file="AlsoMissing" /> captures
// a name that is capitalised, extensionless and separator-free — exactly the
// shape isComponentSymbol calls a component — but it came out of an *include*
// pattern, and the path resolver supports that spelling through its
// .mdx/.md/index.* fallback. Classifying it skipped made a broken include
// vanish from the report, the unresolved count and the stderr note alike, which
// is a regression against shipped #7.
func TestResolveReusable_BrokenSnippetCaptureIsNeverSkipped(t *testing.T) {
	ir := newImportRepo(t)
	body := `# Broken

<Snippet file="AlsoMissing" />
<Snippet file="also-missing" />
<Card title="a built-in" />
`
	page := ir.page("guides/broken.mdx", body)

	// Detection is what establishes provenance, exactly as the analyzer does it.
	refs := FindReusables(body, ir.rp)
	for _, want := range []string{"AlsoMissing", "also-missing", "Card"} {
		if !contains(refs, want) {
			t.Fatalf("FindReusables = %v, missing %q", refs, want)
		}
	}

	// Both broken snippet captures are unresolved, whatever their casing.
	for _, ref := range []string{"AlsoMissing", "also-missing"} {
		info, res := ResolveReusable(ref, page, ir.rp)
		if res != ResolutionUnresolved || info != nil {
			t.Errorf("ResolveReusable(%q) = %v %+v, want ResolutionUnresolved and nil info",
				ref, res, info)
		}
	}
	// The component capture on the same page is still skipped: the fix is about
	// provenance, not about giving up the rule that keeps <Card /> out (#68).
	if _, res := ResolveReusable("Card", page, ir.rp); res != ResolutionSkipped {
		t.Errorf("ResolveReusable(Card) = %v, want ResolutionSkipped", res)
	}

	// An extensionless snippet that *does* exist still resolves, so the case
	// above is about brokenness and not about extensionless captures at large.
	if _, res := ResolveReusable("shared", page, ir.rp); res != ResolutionResolved {
		t.Errorf("ResolveReusable(shared) = %v, want ResolutionResolved", res)
	}
}

// TestBuildImportMap_FencedCodeIsNotAnImport pins the fence rule (#68 review).
// An import shown as an example inside a fence is documentation about an
// import, not one; following it folds a file's commit date into the section and
// makes the page look *fresher* than it is, which is the one direction the
// import map must never fail in.
func TestBuildImportMap_FencedCodeIsNotAnImport(t *testing.T) {
	ir := newImportRepo(t)

	tests := []struct {
		name string
		body string
		// want maps a symbol to the path its import resolved to; a symbol
		// absent from the map must be absent from the import map too.
		want map[string]string
	}{
		{
			name: "import inside a fence is ignored",
			body: "# Page\n\n```mdx\nimport Shared from \"/snippets/shared.mdx\";\n```\n\n<Shared />\n",
			want: map[string]string{},
		},
		{
			name: "tilde fence",
			body: "~~~mdx\nimport Shared from \"/snippets/shared.mdx\";\n~~~\n",
			want: map[string]string{},
		},
		{
			name: "indented fence",
			body: "Text:\n\n   ```\n   import Shared from \"/snippets/shared.mdx\";\n   ```\n",
			want: map[string]string{},
		},
		{
			name: "decoy in a fence does not shadow the real import below",
			body: "```mdx\nimport Shared from \"/snippets/other.mdx\";\n```\n\n" +
				"import Shared from \"/snippets/shared.mdx\";\n\n<Shared />\n",
			want: map[string]string{"Shared": "snippets/shared.mdx"},
		},
		{
			name: "an unclosed fence swallows the rest of the page",
			body: "```mdx\nimport Shared from \"/snippets/shared.mdx\";\n\nstill inside the fence\n",
			want: map[string]string{},
		},
		{
			name: "an import immediately after a closed fence still works",
			body: "```\nnot code we read\n```\nimport Shared from \"/snippets/shared.mdx\";\n",
			want: map[string]string{"Shared": "snippets/shared.mdx"},
		},
		{
			name: "a longer closing fence closes it",
			body: "```mdx\nimport Decoy from \"/snippets/other.mdx\";\n`````\n\n" +
				"import Shared from \"/snippets/shared.mdx\";\n",
			want: map[string]string{"Shared": "snippets/shared.mdx"},
		},
		{
			name: "a shorter run does not close a longer fence",
			body: "````mdx\nimport Decoy from \"/snippets/other.mdx\";\n```\n" +
				"import Shared from \"/snippets/shared.mdx\";\n",
			want: map[string]string{},
		},
		{
			name: "a fence of the other character does not close it",
			body: "```mdx\nimport Decoy from \"/snippets/other.mdx\";\n~~~\n" +
				"import Shared from \"/snippets/shared.mdx\";\n",
			want: map[string]string{},
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A fresh page per case: the import map is memoized per source file.
			page := ir.page(fmt.Sprintf("guides/fence-%d.mdx", i), tt.body)
			got := ir.rp.importsFor(page)
			if len(got) != len(tt.want) {
				t.Fatalf("importsFor = %+v, want %d entr(ies) %v", got, len(tt.want), tt.want)
			}
			for sym, rel := range tt.want {
				target, ok := got[sym]
				if !ok {
					t.Fatalf("importsFor = %+v, missing %q", got, sym)
				}
				if target.path != ir.repo.Path(rel) {
					t.Errorf("%q resolved to %q, want %q", sym, target.path, ir.repo.Path(rel))
				}
			}
		})
	}
}

// TestCalculateSectionStaleness_FencedImportDoesNotRefresh is the fence rule
// stated as the harm it prevents: a page whose only content is a code sample
// showing an import must not inherit the imported file's freshness.
func TestCalculateSectionStaleness_FencedImportDoesNotRefresh(t *testing.T) {
	ir := newImportRepo(t)
	body := "# Example\n\n```mdx\nimport Guide from \"/snippets/other.mdx\";\n\n<Guide />\n```\n"
	page := ir.page("guides/fenced-fold.mdx", body)

	oldLine := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	section := &Chunk{
		Title:     "Example",
		Reusables: FindReusables(body, ir.rp),
		Lines:     []git.LineInfo{{LineNumber: 1, Timestamp: oldLine}},
	}
	got := CalculateSectionStaleness(section, page, ir.rp)
	if got == nil || !got.Equal(oldLine) {
		t.Errorf("effective date = %v, want the section's own %s: an import inside a "+
			"fence must not fold in %s", got, oldLine, ir.otherDate)
	}
}

// TestImportMap_FirstImportWins pins the tie-break the map documents. A page
// that binds one name twice is invalid JavaScript, but the rule still has to be
// deterministic — and "last wins" would let a trailing example or a stray
// duplicate silently retarget a symbol.
func TestImportMap_FirstImportWins(t *testing.T) {
	ir := newImportRepo(t)
	page := ir.page("guides/dup.mdx", `import Dup from "/snippets/shared.mdx";
import Dup from "/snippets/other.mdx";

<Dup />
`)
	info, res := ResolveReusable("Dup", page, ir.rp)
	if res != ResolutionResolved || info == nil {
		t.Fatalf("ResolveReusable(Dup) = %v %+v", res, info)
	}
	if !info.LastModified.Equal(ir.snippetDate) {
		t.Errorf("Dup resolved to the date %s, want the first import's %s "+
			"(the second import's is %s)", info.LastModified, ir.snippetDate, ir.otherDate)
	}
}

// TestDisplayName_UnresolvedImportNamesItsPath is the anti-collision rule for
// broken imports (#68 review). Two pages that import different files under the
// same local symbol must not collapse into one row of the cross-file reusables
// table just because neither file exists.
func TestDisplayName_UnresolvedImportNamesItsPath(t *testing.T) {
	ir := newImportRepo(t)
	pageA := ir.page("guides/a.mdx", "import Card from \"/snippets/gone-a.mdx\";\n\n<Card />\n")
	pageB := ir.page("guides/b.mdx", "import Card from \"./gone-b.mdx\";\n\n<Card />\n")

	gotA := ir.rp.DisplayName("Card", pageA, nil)
	gotB := ir.rp.DisplayName("Card", pageB, nil)
	if gotA != "snippets/gone-a.mdx" {
		t.Errorf("DisplayName(page A) = %q, want snippets/gone-a.mdx", gotA)
	}
	if gotB != "guides/gone-b.mdx" {
		t.Errorf("DisplayName(page B) = %q, want guides/gone-b.mdx", gotB)
	}
	if gotA == gotB {
		t.Errorf("two broken imports of different files collapsed into one row (%q)", gotA)
	}

	// An import that escapes the project root has no root-relative name, so the
	// raw capture is kept rather than a path outside the tree being reported.
	pageC := ir.page("guides/c.mdx", "import Card from \"../../outside.mdx\";\n")
	if got := ir.rp.DisplayName("Card", pageC, nil); got != "Card" {
		t.Errorf("DisplayName(escaping import) = %q, want the raw capture", got)
	}
}

// TestFindReusables_UnderscoreAndDollarComponents is the regression test for
// the component pattern's identifier class (PR #71 review).
//
// The import parser accepts any JavaScript binding name, "_" and "$" included,
// so `import Shared_One from "…"` really does introduce the symbol
// "Shared_One". config.MDXComponentPattern used to stop the capture at
// [a-zA-Z0-9], so `<Shared_One />` was captured as "Shared" — a symbol the
// import map has never heard of, which then took the unimported-component
// branch and was silently skipped while the genuine include went unattributed.
// The capture must be the whole binding, and it must resolve.
func TestFindReusables_UnderscoreAndDollarComponents(t *testing.T) {
	ir := newImportRepo(t)

	page := ir.page("guides/underscore.mdx", `import Shared_One from "/snippets/shared.mdx";
import { Other as Other$Two } from "/snippets/other.mdx";

# Heading

<Shared_One />
<Other$Two />
`)
	body, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}

	got := FindReusables(string(body), ir.rp)
	for _, want := range []string{"Shared_One", "Other$Two"} {
		if !slicesContains(got, want) {
			t.Errorf("FindReusables() = %v, want it to contain %q", got, want)
		}
	}

	for ref, wantDate := range map[string]time.Time{
		"Shared_One": ir.snippetDate,
		"Other$Two":  ir.otherDate,
	} {
		info, res := ResolveReusable(ref, page, ir.rp)
		if res != ResolutionResolved {
			t.Fatalf("ResolveReusable(%q) = %v, want resolved", ref, res)
		}
		if !info.LastModified.Equal(wantDate) {
			t.Errorf("ResolveReusable(%q) date = %s, want %s", ref, info.LastModified, wantDate)
		}
	}
}

func slicesContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

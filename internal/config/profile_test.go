package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestLookupProfile(t *testing.T) {
	for _, name := range []string{"markdown", "hugo", " Hugo "} {
		p, ok := LookupProfile(name)
		if !ok {
			t.Errorf("LookupProfile(%q) not found", name)
			continue
		}
		if p.Name != strings.ToLower(strings.TrimSpace(name)) {
			t.Errorf("LookupProfile(%q).Name = %q", name, p.Name)
		}
	}
	if _, ok := LookupProfile("bogus"); ok {
		t.Error("LookupProfile(bogus) should not be found")
	}
	if _, ok := LookupProfile(""); ok {
		t.Error("LookupProfile(\"\") should not be found")
	}
}

func TestLookupProfile_ReturnsCopy(t *testing.T) {
	p, _ := LookupProfile(ProfileHugo)
	p.ReusablePatterns[0] = "mutated"
	p.ContentExtensions = append(p.ContentExtensions, ".x")
	again, _ := LookupProfile(ProfileHugo)
	if again.ReusablePatterns[0] == "mutated" || len(again.ContentExtensions) != 3 {
		t.Error("LookupProfile must return a copy; the registry was mutated")
	}
}

func TestProfiles_Sorted(t *testing.T) {
	names := Profiles()
	want := []string{"hugo", "markdown", "mintlify"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("Profiles() = %v, want %v", names, want)
	}
	all := AllProfiles()
	if len(all) != len(want) {
		t.Fatalf("AllProfiles() len = %d, want %d", len(all), len(want))
	}
	for i, p := range all {
		if p.Name != want[i] {
			t.Errorf("AllProfiles()[%d].Name = %q, want %q", i, p.Name, want[i])
		}
		if p.Description == "" {
			t.Errorf("profile %q has no description", p.Name)
		}
	}
}

// wantHugoMarkers pins the hugo profile's RootMarkers; README, CHANGELOG and
// the profile Description list the same set.
var wantHugoMarkers = []string{
	"layouts/", "themes/",
	"hugo.toml", "hugo.yaml", "hugo.json",
	"config/_default/hugo.toml", "config/_default/hugo.yaml", "config/_default/hugo.json",
	"config/_default/config.toml", "config/_default/config.yaml", "config/_default/config.json",
}

func TestBuiltinProfiles_Shape(t *testing.T) {
	md := DefaultProfile()
	if md.Name != ProfileMarkdown {
		t.Fatalf("DefaultProfile = %q, want markdown", md.Name)
	}
	if !reflect.DeepEqual(md.ContentExtensions, []string{".md", ".markdown"}) {
		t.Errorf("markdown extensions = %v", md.ContentExtensions)
	}
	if md.RootMarkers != nil || md.ReusablePatterns != nil || md.Resolver != ResolverNone {
		t.Errorf("markdown profile must have no root markers, no patterns and ResolverNone: %+v", md)
	}

	hugo, _ := LookupProfile(ProfileHugo)
	if !reflect.DeepEqual(hugo.ContentExtensions, []string{".md", ".markdown", ".mdx"}) {
		t.Errorf("hugo extensions = %v", hugo.ContentExtensions)
	}
	if !reflect.DeepEqual(hugo.RootMarkers, wantHugoMarkers) || hugo.Resolver != ResolverHugo {
		t.Errorf("hugo profile root markers/resolver wrong: %+v", hugo)
	}
	if len(hugo.ReusablePatterns) != 2 || !reflect.DeepEqual(hugo.ReusableExtensions, []string{".md", ".mdx", ".html"}) {
		t.Errorf("hugo profile patterns/extensions wrong: %+v", hugo)
	}

	mint, ok := LookupProfile(ProfileMintlify)
	if !ok {
		t.Fatal("mintlify profile missing from registry")
	}
	if !reflect.DeepEqual(mint.ContentExtensions, []string{".md", ".mdx"}) {
		t.Errorf("mintlify extensions = %v", mint.ContentExtensions)
	}
	if !reflect.DeepEqual(mint.RootMarkers, []string{"docs.json", "mint.json"}) || mint.Resolver != ResolverPath {
		t.Errorf("mintlify profile root markers/resolver wrong: %+v", mint)
	}
	// Two snippet patterns (one per quote style) plus the shared MDX component
	// pattern, which only means anything because the profile carries an import
	// map to say which captures are includes (#68).
	wantMintPatterns := []string{
		`<Snippet\b[^>]*\bfile="([^"]+)"`,
		`<Snippet\b[^>]*\bfile='([^']+)'`,
		MDXComponentPattern,
	}
	if !reflect.DeepEqual(mint.ReusablePatterns, wantMintPatterns) {
		t.Errorf("mintlify patterns = %v", mint.ReusablePatterns)
	}
	if !reflect.DeepEqual(mint.ReusableExtensions, []string{".mdx", ".md"}) {
		t.Errorf("mintlify reusable extensions = %v", mint.ReusableExtensions)
	}
	if !mint.ImportMap {
		t.Error("mintlify profile should enable the MDX import map (#68)")
	}

	// The import map is opt-in per profile: hugo resolves a component capture
	// as a shortcode name and must not start reading imports instead.
	if hugo.ImportMap {
		t.Error("hugo profile should not enable the MDX import map")
	}
	if md, _ := LookupProfile(ProfileMarkdown); md.ImportMap {
		t.Error("markdown profile should not enable the MDX import map")
	}
}

// TestMintlifyPattern_NoComponentLeak pins the narrowness of the Mintlify
// *snippet* patterns: what <Snippet file="…"> captures is the path, and nothing
// else on the page captures a path. A bare MDX component or a Hugo shortcode
// must never reach the path resolver as if it named a file (#7).
//
// The profile also carries the shared MDX component pattern (#68), which by
// design captures every capitalised tag. That is a different capture with a
// different meaning — a symbol, resolved through the page's import map, and
// skipped outright when no import introduced it — so it is deliberately
// excluded here and covered by the import-map tests instead.
func TestMintlifyPattern_NoComponentLeak(t *testing.T) {
	mint, _ := LookupProfile(ProfileMintlify)
	// One snippet pattern per quote style, then the component pattern.
	if len(mint.ReusablePatterns) != 3 {
		t.Fatalf("mintlify should have two snippet patterns plus the component pattern, got %v",
			mint.ReusablePatterns)
	}
	if mint.ReusablePatterns[2] != MDXComponentPattern {
		t.Fatalf("mintlify pattern 3 should be the shared component pattern, got %q",
			mint.ReusablePatterns[2])
	}
	snippetPatterns := mint.ReusablePatterns[:2]
	res := make([]*regexp.Regexp, 0, len(snippetPatterns))
	for _, p := range snippetPatterns {
		res = append(res, regexp.MustCompile(p))
	}

	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{"snippet absolute", `<Snippet file="/snippets/foo.mdx" />`, []string{"/snippets/foo.mdx"}},
		{"snippet relative", `<Snippet file="./partial.mdx" />`, []string{"./partial.mdx"}},
		{"snippet with other attrs", `<Snippet other="x" file="/s/a.mdx" more="y"/>`, []string{"/s/a.mdx"}},
		{"two snippets", "<Snippet file=\"/a.mdx\" />\n<Snippet file=\"/b.mdx\" />", []string{"/a.mdx", "/b.mdx"}},
		// Single quotes are legal MDX and must be picked up too (#7 review).
		{"single-quoted snippet", `<Snippet file='/snippets/foo.mdx' />`, []string{"/snippets/foo.mdx"}},
		{"single-quoted relative", `<Snippet file='./partial.mdx' />`, []string{"./partial.mdx"}},
		{"single-quoted with attrs before", `<Snippet other='x' file='/s/a.mdx' />`, []string{"/s/a.mdx"}},
		{"single-quoted with attrs after", `<Snippet file='/s/a.mdx' more='y' />`, []string{"/s/a.mdx"}},
		{"double-quoted with attrs before and after", `<Snippet a="1" file="/s/b.mdx" b="2" />`, []string{"/s/b.mdx"}},
		{"mixed quote styles on one page", "<Snippet file=\"/a.mdx\" />\n<Snippet file='/b.mdx' />",
			[]string{"/a.mdx", "/b.mdx"}},
		{"bare component", `<Card title="x" />`, nil},
		{"tabs component", "<Tabs>\n<Tab>x</Tab>\n</Tabs>", nil},
		{"hugo shortcode", `{{< alert >}}`, nil},
		{"snippet without file attr", `<Snippet />`, nil},
		{"lowercase snippet", `<snippet file="/a.mdx" />`, nil},
		{"attr ending in file", `<Snippet datafile="/a.mdx" />`, nil},
		// A different element that merely starts with "Snippet" is not one.
		{"snippet group double-quoted", `<SnippetGroup file="/a.mdx" />`, nil},
		{"snippet group single-quoted", `<SnippetGroup file='/a.mdx' />`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, re := range res {
				for _, m := range re.FindAllStringSubmatch(tt.content, -1) {
					got = append(got, m[1])
				}
			}
			sort.Strings(got)
			want := append([]string(nil), tt.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("captures = %v, want %v", got, tt.want)
			}
		})
	}
}

// detectRootByMarkers is the marker-list form of the root walk, kept as a test
// helper only. The package used to export it as DetectRoot; it was removed
// because it skipped the profiles' marker content predicates, so any caller
// reaching for it (config.DetectRoot(dir, mintProfile.RootMarkers)) silently
// got the pre-predicate behaviour back. Production code goes through
// Profile.DetectRoot; these tests exercise the same walker directly.
func detectRootByMarkers(contentDir string, markers []string) string {
	return detectRoot(contentDir, markers, nil, "", nil)
}

func TestDetectRoot(t *testing.T) {
	root := t.TempDir()
	content := filepath.Join(root, "content", "docs")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}

	// No marker anywhere up to the filesystem root.
	if got := detectRootByMarkers(content, []string{"layouts/", "docs.json"}); got != "" {
		t.Errorf("detectRootByMarkers(no markers present) = %q, want \"\"", got)
	}
	// Empty marker list never matches.
	if got := detectRootByMarkers(content, nil); got != "" {
		t.Errorf("detectRootByMarkers(nil markers) = %q, want \"\"", got)
	}

	// A directory marker ("layouts/") does not match a regular file of that
	// name: a stray file called layouts must not turn the tree into a Hugo site.
	if err := os.WriteFile(filepath.Join(root, "layouts"), []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := detectRootByMarkers(content, []string{"layouts/"}); got != "" {
		t.Errorf("detectRootByMarkers(dir marker vs file) = %q, want \"\"", got)
	}
	if err := os.Remove(filepath.Join(root, "layouts")); err != nil {
		t.Fatal(err)
	}

	// Directory marker matches a directory.
	if err := os.Mkdir(filepath.Join(root, "layouts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := detectRootByMarkers(content, []string{"layouts/"}); got != root {
		t.Errorf("detectRootByMarkers(dir marker) = %q, want %q", got, root)
	}

	// A file marker ("docs.json") does not match a directory of that name.
	mid := filepath.Join(root, "content")
	if err := os.Mkdir(filepath.Join(mid, "docs.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := detectRootByMarkers(content, []string{"docs.json", "mint.json"}); got != "" {
		t.Errorf("detectRootByMarkers(file marker vs dir) = %q, want \"\"", got)
	}
	if err := os.Remove(filepath.Join(mid, "docs.json")); err != nil {
		t.Fatal(err)
	}

	// File marker matches a regular file, found at an intermediate level
	// (closest wins).
	if err := os.WriteFile(filepath.Join(mid, "docs.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := detectRootByMarkers(content, []string{"docs.json", "mint.json"}); got != mid {
		t.Errorf("detectRootByMarkers(file marker) = %q, want %q", got, mid)
	}
	// Marker in the content dir itself.
	if got := detectRootByMarkers(mid, []string{"docs.json"}); got != mid {
		t.Errorf("detectRootByMarkers(marker in dir) = %q, want %q", got, mid)
	}
	// Whitespace around a marker is tolerated; a bare "/" marker is ignored.
	if got := detectRootByMarkers(content, []string{" layouts/ ", "/"}); got != root {
		t.Errorf("detectRootByMarkers(padded dir marker) = %q, want %q", got, root)
	}
}

func TestDetectRoot_RelativePathWalksAboveCwd(t *testing.T) {
	root := t.TempDir()
	content := filepath.Join(root, "site", "content")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "layouts"), 0o755); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(root, "site")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	got := detectRootByMarkers("content", []string{"layouts/"})
	// Compare via os.SameFile: t.TempDir may sit behind a symlink on some OSes.
	a, err1 := os.Stat(got)
	b, err2 := os.Stat(root)
	if err1 != nil || err2 != nil || !os.SameFile(a, b) {
		t.Errorf("detectRootByMarkers(relative) = %q, want %q", got, root)
	}
}

// TestDetectRoot_HugoConfigMarkers pins the fix for fresh clones: git does not
// track the empty layouts/ directory of a canonical Hugo site, so the hugo
// profile must also be detected from Hugo's own config file names, with the
// root at the directory holding the file. A tree with neither marker stays
// markdown. It also exercises detectNearest with several markers on one
// profile at one level (any match wins).
func TestDetectRoot_HugoConfigMarkers(t *testing.T) {
	hugo := mustProfile(ProfileHugo)
	for _, marker := range []string{"hugo.toml", "hugo.yaml", "hugo.json"} {
		t.Run(marker, func(t *testing.T) {
			root := t.TempDir()
			content := filepath.Join(root, "content", "docs")
			if err := os.MkdirAll(content, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, marker), []byte("baseURL = 'x'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			// No layouts/ anywhere: only the config file identifies the site.
			if got := detectRootByMarkers(content, hugo.RootMarkers); got != root {
				t.Errorf("detectRootByMarkers(%s only) = %q, want %q", marker, got, root)
			}
			if p, r, ok := detectNearest(content, builtinProfiles, nil); !ok || p.Name != ProfileHugo || r != root {
				t.Errorf("detectNearest(%s only) = (%q, %q, %v), want (hugo, %q, true)", marker, p.Name, r, ok, root)
			}
			if p, r, _ := detectProfile(content, nil); p.Name != ProfileHugo || r != root {
				t.Errorf("detectProfile(%s only) = (%q, %q), want (hugo, %q)", marker, p.Name, r, root)
			}
			// A directory of the same name is not a config file.
			if err := os.Remove(filepath.Join(root, marker)); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, marker), 0o755); err != nil {
				t.Fatal(err)
			}
			if got := detectRootByMarkers(content, hugo.RootMarkers); got != "" {
				t.Errorf("detectRootByMarkers(%s as a directory) = %q, want \"\"", marker, got)
			}
		})
	}

	t.Run("both layouts and hugo.toml at one level", func(t *testing.T) {
		root, content := hugoTree(t)
		if err := os.WriteFile(filepath.Join(root, "hugo.toml"), []byte(""), 0o600); err != nil {
			t.Fatal(err)
		}
		if p, r, ok := detectNearest(content, builtinProfiles, nil); !ok || p.Name != ProfileHugo || r != root {
			t.Errorf("detectNearest(both markers) = (%q, %q, %v), want (hugo, %q, true)", p.Name, r, ok, root)
		}
	})

	t.Run("neither marker is markdown", func(t *testing.T) {
		content := plainTree(t)
		if got := detectRootByMarkers(content, hugo.RootMarkers); got != "" {
			t.Errorf("detectRootByMarkers(plain) = %q, want \"\"", got)
		}
		if p, r, _ := detectProfile(content, nil); p.Name != ProfileMarkdown || r != "" {
			t.Errorf("detectProfile(plain, nil) = (%q, %q), want (markdown, \"\")", p.Name, r)
		}
	})
}

// TestMarkerKind_PathSeparators pins that only a trailing "/" carries the
// directory kind: interior separators are kept as path segments, and hasMarker
// joins them with the host separator.
func TestMarkerKind_PathSeparators(t *testing.T) {
	cases := []struct {
		in      string
		name    string
		wantDir bool
	}{
		{"layouts/", "layouts", true},
		{"docs.json", "docs.json", false},
		{"config/_default/hugo.toml", "config/_default/hugo.toml", false},
		{"config/_default/", "config/_default", true},
		{" themes/ ", "themes", true},
	}
	for _, c := range cases {
		name, wantDir := markerKind(c.in)
		if name != c.name || wantDir != c.wantDir {
			t.Errorf("markerKind(%q) = (%q, %v), want (%q, %v)", c.in, name, wantDir, c.name, c.wantDir)
		}
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config", "_default"), 0o755); err != nil {
		t.Fatal(err)
	}
	if hasMarker(root, []string{"config/_default/hugo.toml"}, nil, "", nil) {
		t.Error("hasMarker(nested file marker) = true before the file exists")
	}
	if !hasMarker(root, []string{"config/_default/"}, nil, "", nil) {
		t.Error("hasMarker(nested dir marker) = false, want true")
	}
	if err := os.WriteFile(filepath.Join(root, "config", "_default", "hugo.toml"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hasMarker(root, []string{"config/_default/hugo.toml"}, nil, "", nil) {
		t.Error("hasMarker(nested file marker) = false, want true")
	}
}

// TestDetectRoot_HugoThemeAndSplitConfigMarkers pins detection of Hugo sites
// that keep their config under config/_default/ and get their layouts from a
// theme: no top-level layouts/ and no top-level hugo.*, so only themes/ and
// the split-config files identify the site. A plain repo with an unrelated
// config/ directory must stay markdown, and a regular file named themes is not
// the marker.
func TestDetectRoot_HugoThemeAndSplitConfigMarkers(t *testing.T) {
	hugo := mustProfile(ProfileHugo)

	mkTree := func(t *testing.T) (root, content string) {
		root = t.TempDir()
		content = filepath.Join(root, "content", "docs")
		if err := os.MkdirAll(content, 0o755); err != nil {
			t.Fatal(err)
		}
		return root, content
	}
	assertHugo := func(t *testing.T, label, content, root string) {
		t.Helper()
		if got := detectRootByMarkers(content, hugo.RootMarkers); got != root {
			t.Errorf("detectRootByMarkers(%s) = %q, want %q", label, got, root)
		}
		if p, r, _ := detectProfile(content, nil); p.Name != ProfileHugo || r != root {
			t.Errorf("detectProfile(%s, nil) = (%q, %q), want (hugo, %q)", label, p.Name, r, root)
		}
	}
	assertMarkdown := func(t *testing.T, label, content string) {
		t.Helper()
		if got := detectRootByMarkers(content, hugo.RootMarkers); got != "" {
			t.Errorf("detectRootByMarkers(%s) = %q, want \"\"", label, got)
		}
		if p, r, _ := detectProfile(content, nil); p.Name != ProfileMarkdown || r != "" {
			t.Errorf("detectProfile(%s, nil) = (%q, %q), want (markdown, \"\")", label, p.Name, r)
		}
	}

	t.Run("config/_default/hugo.toml with theme layouts", func(t *testing.T) {
		root, content := mkTree(t)
		if err := os.MkdirAll(filepath.Join(root, "config", "_default"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "config", "_default", "hugo.toml"), []byte("baseURL = 'x'\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		// Layouts live in the theme; there is no top-level layouts/.
		if err := os.MkdirAll(filepath.Join(root, "themes", "x", "layouts", "shortcodes"), 0o755); err != nil {
			t.Fatal(err)
		}
		assertHugo(t, "split config + theme", content, root)
	})

	t.Run("themes/ alone", func(t *testing.T) {
		root, content := mkTree(t)
		if err := os.Mkdir(filepath.Join(root, "themes"), 0o755); err != nil {
			t.Fatal(err)
		}
		assertHugo(t, "themes/ only", content, root)
	})

	t.Run("regular file named themes", func(t *testing.T) {
		root, content := mkTree(t)
		if err := os.WriteFile(filepath.Join(root, "themes"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertMarkdown(t, "file named themes", content)
	})

	for _, name := range []string{"config.toml", "config.yaml", "config.json", "hugo.yaml", "hugo.json"} {
		t.Run("config/_default/"+name, func(t *testing.T) {
			root, content := mkTree(t)
			if err := os.MkdirAll(filepath.Join(root, "config", "_default"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "config", "_default", name), []byte(""), 0o600); err != nil {
				t.Fatal(err)
			}
			assertHugo(t, "config/_default/"+name, content, root)
		})
	}

	t.Run("plain repo with a config dir stays markdown", func(t *testing.T) {
		root, content := mkTree(t)
		// A config/ directory with non-Hugo contents, a top-level config.toml
		// (used by many tools) and even an empty config/_default/ must not
		// select hugo.
		if err := os.MkdirAll(filepath.Join(root, "config", "_default"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"config/settings.yaml", "config.toml"} {
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(f)), []byte(""), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		assertMarkdown(t, "plain repo with config/", content)
	})
}

// hugoTree returns a content dir with a layouts/ directory above it.
func hugoTree(t *testing.T) (root, content string) {
	t.Helper()
	root = t.TempDir()
	content = filepath.Join(root, "content", "docs")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "layouts"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, content
}

// hugoConfigTree returns a content dir with only a hugo.toml (no layouts/)
// above it, the shape of a fresh clone of a canonical Hugo site.
func hugoConfigTree(t *testing.T) (root, content string) {
	t.Helper()
	root = t.TempDir()
	content = filepath.Join(root, "content", "docs")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hugo.toml"), []byte("baseURL = 'x'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, content
}

// mintlifyConfigJSON is a minimal but realistic Mintlify config: the marker is
// validated by content as well as by name (see isMintlifyConfig), so test trees
// must carry something a real docs.json / mint.json would carry.
const mintlifyConfigJSON = `{"$schema":"https://mintlify.com/docs.json",` +
	`"name":"Docs","theme":"mint","colors":{"primary":"#000"},` +
	`"navigation":{"pages":["docs/index"]}}`

// mintlifyTree returns a content dir with a Mintlify config file (docs.json or
// mint.json, per marker) above it.
func mintlifyTree(t *testing.T, marker string) (root, content string) {
	t.Helper()
	root = t.TempDir()
	content = filepath.Join(root, "docs")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, marker), []byte(mintlifyConfigJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, content
}

// plainTree returns a content dir with no project markers above it.
func plainTree(t *testing.T) string {
	t.Helper()
	content := filepath.Join(t.TempDir(), "docs")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	return content
}

func TestApplyProfile(t *testing.T) {
	hugoRoot, hugoContent := hugoTree(t)
	tomlRoot, tomlContent := hugoConfigTree(t)
	plainContent := plainTree(t)
	markerlessRoot := t.TempDir()
	mintDocsRoot, mintDocsContent := mintlifyTree(t, "docs.json")
	mintLegacyRoot, mintLegacyContent := mintlifyTree(t, "mint.json")
	hugo := mustProfile(ProfileHugo)
	mint := mustProfile(ProfileMintlify)

	tests := []struct {
		name     string
		cfg      Config
		wantErr  string
		wantName string
		wantAuto bool
		check    func(t *testing.T, c *Config)
	}{
		{
			name:     "explicit markdown disables detection even inside a hugo tree",
			cfg:      Config{Profile: "markdown", ContentDir: hugoContent},
			wantName: ProfileMarkdown,
			check: func(t *testing.T, c *Config) {
				if !reflect.DeepEqual(c.ContentExtensions, []string{".md", ".markdown"}) {
					t.Errorf("extensions = %v", c.ContentExtensions)
				}
				if len(c.Reusables.Patterns) != 0 {
					t.Errorf("markdown profile must leave patterns empty, got %v", c.Reusables.Patterns)
				}
				if c.ProjectRoot != "" {
					t.Errorf("markdown profile must not set ProjectRoot, got %q", c.ProjectRoot)
				}
			},
		},
		{
			name:     "explicit hugo fills patterns and detects root",
			cfg:      Config{Profile: "hugo", ContentDir: hugoContent},
			wantName: ProfileHugo,
			check: func(t *testing.T, c *Config) {
				if !reflect.DeepEqual(c.ContentExtensions, hugo.ContentExtensions) {
					t.Errorf("extensions = %v", c.ContentExtensions)
				}
				if !reflect.DeepEqual(c.Reusables.Patterns, hugo.ReusablePatterns) {
					t.Errorf("patterns = %v", c.Reusables.Patterns)
				}
				if !reflect.DeepEqual(c.Reusables.Extensions, hugo.ReusableExtensions) {
					t.Errorf("reusable extensions = %v", c.Reusables.Extensions)
				}
				if c.ProjectRoot != hugoRoot {
					t.Errorf("ProjectRoot = %q, want %q", c.ProjectRoot, hugoRoot)
				}
			},
		},
		{
			name:     "explicit hugo outside a hugo tree still enables detection, root stays empty",
			cfg:      Config{Profile: "hugo", ContentDir: plainContent},
			wantName: ProfileHugo,
			check: func(t *testing.T, c *Config) {
				if len(c.Reusables.Patterns) != 2 {
					t.Errorf("patterns = %v", c.Reusables.Patterns)
				}
				if c.ProjectRoot != "" {
					t.Errorf("ProjectRoot = %q, want \"\"", c.ProjectRoot)
				}
			},
		},
		{
			name:    "unknown profile errors and names the valid ones",
			cfg:     Config{Profile: "bogus", ContentDir: plainContent},
			wantErr: "valid profiles: hugo, markdown, mintlify",
		},
		{
			name:     "explicit mintlify fills the snippet pattern and detects the root",
			cfg:      Config{Profile: "mintlify", ContentDir: mintDocsContent},
			wantName: ProfileMintlify,
			check: func(t *testing.T, c *Config) {
				if !reflect.DeepEqual(c.ContentExtensions, mint.ContentExtensions) {
					t.Errorf("extensions = %v", c.ContentExtensions)
				}
				if !reflect.DeepEqual(c.Reusables.Patterns, mint.ReusablePatterns) {
					t.Errorf("patterns = %v", c.Reusables.Patterns)
				}
				if !reflect.DeepEqual(c.Reusables.Extensions, mint.ReusableExtensions) {
					t.Errorf("reusable extensions = %v", c.Reusables.Extensions)
				}
				if c.ProjectRoot != mintDocsRoot {
					t.Errorf("root = %q, want %q", c.ProjectRoot, mintDocsRoot)
				}
				if c.ResolvedProfile.Resolver != ResolverPath {
					t.Errorf("resolver = %q, want path", c.ResolvedProfile.Resolver)
				}
			},
		},
		{
			name:     "auto-detects mintlify from docs.json",
			cfg:      Config{ContentDir: mintDocsContent},
			wantName: ProfileMintlify,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if c.ProjectRoot != mintDocsRoot {
					t.Errorf("root = %q, want %q", c.ProjectRoot, mintDocsRoot)
				}
			},
		},
		{
			name:     "auto-detects mintlify from legacy mint.json",
			cfg:      Config{ContentDir: mintLegacyContent},
			wantName: ProfileMintlify,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if c.ProjectRoot != mintLegacyRoot {
					t.Errorf("root = %q, want %q", c.ProjectRoot, mintLegacyRoot)
				}
			},
		},
		{
			name: "explicit user settings beat the mintlify profile",
			cfg: Config{
				Profile:           "mintlify",
				ContentDir:        mintDocsContent,
				ContentExtensions: []string{"RST"},
				Reusables:         ReusablesConfig{Patterns: []string{`\{\{(\w+)\}\}`}, Extensions: []string{".txt"}},
			},
			wantName: ProfileMintlify,
			check: func(t *testing.T, c *Config) {
				if !reflect.DeepEqual(c.ContentExtensions, []string{".rst"}) {
					t.Errorf("extensions = %v, want [.rst]", c.ContentExtensions)
				}
				if !reflect.DeepEqual(c.Reusables.Patterns, []string{`\{\{(\w+)\}\}`}) {
					t.Errorf("patterns = %v", c.Reusables.Patterns)
				}
				if !reflect.DeepEqual(c.Reusables.Extensions, []string{".txt"}) {
					t.Errorf("reusable extensions = %v", c.Reusables.Extensions)
				}
			},
		},
		{
			name:     "auto-detects hugo when layouts/ exists above content_dir",
			cfg:      Config{ContentDir: hugoContent},
			wantName: ProfileHugo,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if c.ProjectRoot != hugoRoot {
					t.Errorf("ProjectRoot = %q, want %q", c.ProjectRoot, hugoRoot)
				}
				if len(c.Reusables.Patterns) != 2 {
					t.Errorf("patterns = %v", c.Reusables.Patterns)
				}
			},
		},
		{
			name:     "auto-detects hugo from hugo.toml alone (fresh clone without layouts/)",
			cfg:      Config{ContentDir: tomlContent},
			wantName: ProfileHugo,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if c.ProjectRoot != tomlRoot {
					t.Errorf("ProjectRoot = %q, want %q", c.ProjectRoot, tomlRoot)
				}
				if !reflect.DeepEqual(c.Reusables.Patterns, hugo.ReusablePatterns) {
					t.Errorf("patterns = %v, want hugo defaults", c.Reusables.Patterns)
				}
				if !reflect.DeepEqual(c.ContentExtensions, hugo.ContentExtensions) {
					t.Errorf("extensions = %v", c.ContentExtensions)
				}
			},
		},
		{
			name:     "auto-detects markdown when no marker is found",
			cfg:      Config{ContentDir: plainContent},
			wantName: ProfileMarkdown,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if len(c.Reusables.Patterns) != 0 || len(c.Reusables.Extensions) != 0 {
					t.Errorf("markdown must not enable reusables: %+v", c.Reusables)
				}
				if c.ProjectRoot != "" {
					t.Errorf("ProjectRoot = %q, want \"\"", c.ProjectRoot)
				}
			},
		},
		{
			// Legacy compatibility: the deprecated "hugo_root" key used to be
			// how you said "this is a Hugo site", so a root supplied through
			// it still selects hugo when no marker is found anywhere. The root
			// must exist (ApplyProfile validates a user-supplied one) but
			// carries no Hugo marker here.
			name:     "legacy hugo_root selects hugo without a layouts marker",
			cfg:      Config{ContentDir: plainContent, legacyHugoRoot: markerlessRoot},
			wantName: ProfileHugo,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if c.ProjectRoot != markerlessRoot {
					t.Errorf("ProjectRoot = %q, want the user's value", c.ProjectRoot)
				}
				if !c.RootFromUser {
					t.Error("RootFromUser = false, want true for a legacy hugo_root")
				}
				if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "deprecated") ||
					!strings.Contains(c.Warnings[0], "rename it") {
					t.Errorf("Warnings = %v, want a hugo_root rename notice", c.Warnings)
				}
				if strings.Contains(c.Warnings[0], "was ignored") {
					t.Errorf("Warnings = %v: the key supplied the root, it was not ignored", c.Warnings)
				}
			},
		},
		{
			// The current spelling says where to resolve from, never what the
			// project is: on a markerless tree it leaves the profile alone.
			name:     "project_root never selects hugo on its own",
			cfg:      Config{ContentDir: plainContent, ProjectRoot: markerlessRoot},
			wantName: ProfileMarkdown,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if c.ProjectRoot != markerlessRoot {
					t.Errorf("ProjectRoot = %q, want the user's value", c.ProjectRoot)
				}
				if !c.RootFromUser {
					t.Error("RootFromUser = false, want true for an explicit project_root")
				}
				if len(c.Warnings) != 0 {
					t.Errorf("Warnings = %v, want none for the current spelling", c.Warnings)
				}
			},
		},
		{
			// A Mintlify tree with a --project-root and no --profile: the
			// marker still decides, so snippet paths keep resolving with the
			// path resolver instead of being captured as hugo component names.
			name:     "project_root on a mintlify tree keeps auto-detecting mintlify",
			cfg:      Config{ContentDir: mintDocsContent, ProjectRoot: mintDocsRoot},
			wantName: ProfileMintlify,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if c.ResolvedProfile.Resolver != ResolverPath {
					t.Errorf("resolver = %q, want path", c.ResolvedProfile.Resolver)
				}
				if c.ProjectRoot != mintDocsRoot {
					t.Errorf("ProjectRoot = %q, want %q", c.ProjectRoot, mintDocsRoot)
				}
			},
		},
		{
			// An explicit profile always wins over both detection and the
			// legacy fallback.
			name:     "an explicit profile wins over a supplied root",
			cfg:      Config{Profile: ProfileMintlify, ContentDir: plainContent, ProjectRoot: markerlessRoot},
			wantName: ProfileMintlify,
			check: func(t *testing.T, c *Config) {
				if c.ProjectRoot != markerlessRoot {
					t.Errorf("ProjectRoot = %q, want the user's value", c.ProjectRoot)
				}
			},
		},
		{
			name:     "an explicit profile wins over the legacy hugo_root fallback",
			cfg:      Config{Profile: ProfileMintlify, ContentDir: plainContent, legacyHugoRoot: markerlessRoot},
			wantName: ProfileMintlify,
			check: func(t *testing.T, c *Config) {
				if c.ProjectRoot != markerlessRoot {
					t.Errorf("ProjectRoot = %q, want the user's value", c.ProjectRoot)
				}
			},
		},
		{
			// Both spellings present: the current one wins, and the legacy
			// one no longer drags the hugo profile in with it.
			name: "project_root wins over legacy hugo_root",
			cfg: Config{
				ContentDir:     plainContent,
				ProjectRoot:    markerlessRoot,
				legacyHugoRoot: plainContent,
			},
			wantName: ProfileMarkdown,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if c.ProjectRoot != markerlessRoot {
					t.Errorf("ProjectRoot = %q, want the project_root value", c.ProjectRoot)
				}
				// The dead key must not be dropped in silence: a stale
				// "hugo_root" pointing somewhere else looked like it was
				// still in force (#7 review pass 4).
				var warned bool
				for _, w := range c.Warnings {
					if strings.Contains(w, `"hugo_root" is deprecated`) &&
						strings.Contains(w, "was ignored") &&
						strings.Contains(w, strconv.Quote(plainContent)) {
						warned = true
					}
				}
				if !warned {
					t.Errorf("Warnings = %v, want one saying hugo_root was ignored and naming %q",
						c.Warnings, plainContent)
				}
			},
		},
		{
			name:     "empty content_dir falls back to markdown",
			cfg:      Config{},
			wantName: ProfileMarkdown,
			wantAuto: true,
		},
		{
			name: "explicit user extensions and patterns are preserved",
			cfg: Config{
				ContentDir:        hugoContent,
				ContentExtensions: []string{".rst"},
				Reusables:         ReusablesConfig{Patterns: []string{`\{% include "([^"]+)" %\}`}, Extensions: []string{".txt"}},
			},
			wantName: ProfileHugo,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if !reflect.DeepEqual(c.ContentExtensions, []string{".rst"}) {
					t.Errorf("user extensions overwritten: %v", c.ContentExtensions)
				}
				if len(c.Reusables.Patterns) != 1 || !reflect.DeepEqual(c.Reusables.Extensions, []string{".txt"}) {
					t.Errorf("user reusables overwritten: %+v", c.Reusables)
				}
			},
		},
		{
			name:     "explicit user patterns kept on the markdown profile",
			cfg:      Config{ContentDir: plainContent, Reusables: ReusablesConfig{Patterns: []string{`<<([a-z]+)>>`}}},
			wantName: ProfileMarkdown,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if !reflect.DeepEqual(c.Reusables.Patterns, []string{`<<([a-z]+)>>`}) {
					t.Errorf("patterns = %v", c.Reusables.Patterns)
				}
				// Patterns without extensions: resolution needs some extension
				// list, so the hugo defaults are borrowed.
				if len(c.Reusables.Extensions) == 0 {
					t.Error("expected reusable extensions to be filled when patterns are set")
				}
			},
		},
		{
			// The legacy reusables-dir flow predates profiles and was built for
			// Hugo shortcodes / MDX components, so it must restore the hugo
			// pattern list *and* the hugo content extensions: dropping .mdx here
			// would silently stop analyzing the files these setups use.
			name:     "legacy reusables.dir with no patterns gets the hugo patterns and extensions",
			cfg:      Config{ContentDir: plainContent, Reusables: ReusablesConfig{Dir: "shared"}},
			wantName: ProfileMarkdown,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if !reflect.DeepEqual(c.Reusables.Patterns, hugo.ReusablePatterns) {
					t.Errorf("patterns = %v, want hugo defaults", c.Reusables.Patterns)
				}
				if !reflect.DeepEqual(c.Reusables.Extensions, hugo.ReusableExtensions) {
					t.Errorf("reusable extensions = %v, want hugo defaults", c.Reusables.Extensions)
				}
				if !reflect.DeepEqual(c.ContentExtensions, hugo.ContentExtensions) {
					t.Errorf("content extensions = %v, want hugo defaults %v (.mdx must stay analyzed)",
						c.ContentExtensions, hugo.ContentExtensions)
				}
				// The widening was this branch's doing, not the user's: the
				// zero-files warning must keep attributing it to the profile.
				if c.ExtensionsFromUser {
					t.Error("ExtensionsFromUser = true, want false (the legacy branch widened them)")
				}
			},
		},
		{
			name: "legacy reusables.dir does not override explicit content extensions",
			cfg: Config{
				ContentDir:        plainContent,
				ContentExtensions: []string{".md"},
				Reusables:         ReusablesConfig{Dir: "shared"},
			},
			wantName: ProfileMarkdown,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if !reflect.DeepEqual(c.ContentExtensions, []string{".md"}) {
					t.Errorf("user extensions overwritten: %v", c.ContentExtensions)
				}
				if !c.ExtensionsFromUser {
					t.Error("ExtensionsFromUser = false, want true (the user set content_extensions)")
				}
				if !reflect.DeepEqual(c.Reusables.Patterns, hugo.ReusablePatterns) {
					t.Errorf("patterns = %v, want hugo defaults", c.Reusables.Patterns)
				}
			},
		},
		{
			// The stored allowlist must be exactly what the analyzer's walk
			// matches on, because the banner, the stderr warnings and the JSON
			// report's config.content_extensions all echo this field (#11).
			name: "user content extensions are canonicalised in place",
			cfg: Config{
				ContentDir:        plainContent,
				ContentExtensions: []string{"mdx", ".MD", "", ".md", "  .Markdown  ", "MDX"},
			},
			wantName: ProfileMarkdown,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				want := []string{".mdx", ".md", ".markdown"}
				if !reflect.DeepEqual(c.ContentExtensions, want) {
					t.Errorf("content extensions = %v, want %v", c.ContentExtensions, want)
				}
				if !c.ExtensionsFromUser {
					t.Error("ExtensionsFromUser = false, want true (the user set content_extensions)")
				}
			},
		},
		{
			// A list of nothing but blanks is no override at all: it must fall
			// through to the profile's defaults rather than leave the walk with
			// an empty allowlist.
			name: "blank-only content extensions fall back to the profile",
			cfg: Config{
				ContentDir:        plainContent,
				ContentExtensions: []string{"", "  "},
			},
			wantName: ProfileMarkdown,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if !reflect.DeepEqual(c.ContentExtensions, []string{".md", ".markdown"}) {
					t.Errorf("content extensions = %v, want the markdown profile's", c.ContentExtensions)
				}
				if c.ExtensionsFromUser {
					t.Error("ExtensionsFromUser = true, want false (blanks are not an override)")
				}
			},
		},
		{
			// The compat branch only fires on a profile with no patterns of its
			// own; under hugo the profile's own extensions apply as usual.
			name:     "reusables.dir under the hugo profile keeps the hugo profile's own defaults",
			cfg:      Config{Profile: "hugo", ContentDir: plainContent, Reusables: ReusablesConfig{Dir: "shared"}},
			wantName: ProfileHugo,
			check: func(t *testing.T, c *Config) {
				if !reflect.DeepEqual(c.ContentExtensions, hugo.ContentExtensions) {
					t.Errorf("content extensions = %v", c.ContentExtensions)
				}
			},
		},
		{
			name:     "deprecated top-level reusables_dir is migrated and enables patterns",
			cfg:      Config{ContentDir: plainContent, ReusablesDir: "shared"},
			wantName: ProfileMarkdown,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if c.Reusables.Dir != "shared" {
					t.Errorf("Reusables.Dir = %q, want migrated \"shared\"", c.Reusables.Dir)
				}
				if len(c.Reusables.Patterns) != 2 {
					t.Errorf("patterns = %v, want hugo defaults", c.Reusables.Patterns)
				}
				if !reflect.DeepEqual(c.ContentExtensions, hugo.ContentExtensions) {
					t.Errorf("content extensions = %v, want hugo defaults", c.ContentExtensions)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.cfg
			err := c.ApplyProfile()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ApplyProfile() error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ApplyProfile() error = %v", err)
			}
			if c.ResolvedProfile.Name != tc.wantName {
				t.Errorf("ResolvedProfile.Name = %q, want %q", c.ResolvedProfile.Name, tc.wantName)
			}
			if c.ProfileAuto != tc.wantAuto {
				t.Errorf("ProfileAuto = %v, want %v", c.ProfileAuto, tc.wantAuto)
			}
			if tc.check != nil {
				tc.check(t, &c)
			}
		})
	}
}

// TestApplyProfile_Idempotent: running ApplyProfile twice (e.g. the CLI and
// then the analyzer's guard) must not change the result.
func TestApplyProfile_Idempotent(t *testing.T) {
	_, content := hugoTree(t)
	c := Config{ContentDir: content}
	if err := c.ApplyProfile(); err != nil {
		t.Fatal(err)
	}
	first := c
	if err := c.ApplyProfile(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, c) {
		t.Errorf("second ApplyProfile changed config:\n%+v\n%+v", first, c)
	}
}

func TestValidate_Profile(t *testing.T) {
	c := DefaultConfig()
	c.Profile = "hugo"
	if err := c.Validate(); err != nil {
		t.Errorf("known profile should validate: %v", err)
	}
	c.Profile = "nope"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "hugo, markdown") {
		t.Errorf("unknown profile should fail validation naming the valid ones, got %v", err)
	}
}

// TestDetectNearest proves the walk is nearest-marker-first: a marker close to
// content_dir beats a marker of an earlier candidate that sits farther up,
// and only a tie at the same level falls back to candidate order.
func TestDetectNearest(t *testing.T) {
	layoutsProfile := Profile{Name: "layouts-tool", RootMarkers: []string{"layouts/"}}
	docsProfile := Profile{Name: "docsjson-tool", RootMarkers: []string{"docs.json", "mint.json"}}
	noMarkers := Profile{Name: "plain"}
	candidates := []Profile{noMarkers, layoutsProfile, docsProfile}

	// root/layouts/            <- earlier candidate, farther away
	// root/site/docs.json      <- later candidate, nearer
	// root/site/pages/         <- content dir
	root := t.TempDir()
	site := filepath.Join(root, "site")
	content := filepath.Join(site, "pages")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}

	// Nothing anywhere: not detected.
	if p, r, ok := detectNearest(content, candidates, nil); ok || p.Name != "" || r != "" {
		t.Errorf("detectNearest(no markers) = (%q, %q, %v), want not found", p.Name, r, ok)
	}

	// Wrong kinds: a regular file named layouts and a directory named
	// docs.json match neither candidate's marker.
	if err := os.WriteFile(filepath.Join(root, "layouts"), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(site, "docs.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, r, ok := detectNearest(content, candidates, nil); ok || p.Name != "" || r != "" {
		t.Errorf("detectNearest(wrong marker kinds) = (%q, %q, %v), want not found", p.Name, r, ok)
	}
	if err := os.Remove(filepath.Join(root, "layouts")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(site, "docs.json")); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(root, "layouts"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Only the far marker exists: it wins with its own root.
	if p, r, ok := detectNearest(content, candidates, nil); !ok || p.Name != "layouts-tool" || r != root {
		t.Errorf("detectNearest(far only) = (%q, %q, %v), want (layouts-tool, %q, true)", p.Name, r, ok, root)
	}

	if err := os.WriteFile(filepath.Join(site, "docs.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Nearer marker of the later candidate beats the farther earlier one.
	if p, r, ok := detectNearest(content, candidates, nil); !ok || p.Name != "docsjson-tool" || r != site {
		t.Errorf("detectNearest(nearest) = (%q, %q, %v), want (docsjson-tool, %q, true)", p.Name, r, ok, site)
	}

	// Same level: candidate order breaks the tie.
	if err := os.Mkdir(filepath.Join(site, "layouts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, r, ok := detectNearest(content, candidates, nil); !ok || p.Name != "layouts-tool" || r != site {
		t.Errorf("detectNearest(tie) = (%q, %q, %v), want (layouts-tool, %q, true)", p.Name, r, ok, site)
	}

	// The returned profile is a copy, not the caller's slice element.
	p, _, _ := detectNearest(content, candidates, nil)
	p.RootMarkers[0] = "mutated"
	if candidates[1].RootMarkers[0] != "layouts/" {
		t.Error("detectNearest must return a copy of the winning profile")
	}
}

// TestDetectProfile_NearestMarkerWins checks the registry-driven path through
// detectProfile: a nearby hugo marker is found even when the walk starts deep
// in the tree, and a tree with no marker anywhere reports that nothing was
// detected (which is what the legacy hugo_root fallback in ApplyProfile keys
// off).
func TestDetectProfile_NearestMarkerWins(t *testing.T) {
	root, content := hugoTree(t)
	deep := filepath.Join(content, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if p, r, _ := detectProfile(deep, nil); p.Name != ProfileHugo || r != root {
		t.Errorf("detectProfile(deep, nil) = (%q, %q), want (hugo, %q)", p.Name, r, root)
	}
	if p, r, _ := detectProfile(plainTree(t), nil); p.Name != ProfileMarkdown || r != "" {
		t.Errorf("detectProfile(plain, nil) = (%q, %q), want (markdown, \"\")", p.Name, r)
	}
	if p, r, _ := detectProfile("", nil); p.Name != ProfileMarkdown || r != "" {
		t.Errorf("detectProfile(empty, nil) = (%q, %q), want (markdown, \"\")", p.Name, r)
	}
	// Nothing found is reported as such, whatever root the caller may hold.
	if p, r, ok := detectProfile(plainTree(t), nil); ok || p.Name != ProfileMarkdown || r != "" {
		t.Errorf("detectProfile(plain, nil) = (%q, %q, %v), want (markdown, \"\", false)", p.Name, r, ok)
	}
	if _, _, ok := detectProfile(deep, nil); !ok {
		t.Error("detectProfile(deep) reported no detection, want true")
	}
}

// TestDetectProfile_MintlifyVsHugo pins how the two markered built-ins settle
// against each other (#7):
//
//   - a Mintlify docs tree nested inside a repo whose root carries a Hugo
//     layouts/ selects mintlify, because the nearer marker always wins;
//   - the mirror case (a Hugo content tree under a repo root holding a
//     docs.json) selects hugo, for the same reason;
//   - and when both markers sit in the *same* directory, registry order
//     decides, which is why hugo precedes mintlify: layouts/ and hugo.toml are
//     unambiguous Hugo evidence, and downgrading such a site to mintlify would
//     silently switch shortcode tracing off.
func TestDetectProfile_MintlifyVsHugo(t *testing.T) {
	writeMintlifyConfig := func(t *testing.T, dir string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "docs.json"), []byte(mintlifyConfigJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("nested mintlify beats a farther hugo marker", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "layouts"), 0o755); err != nil {
			t.Fatal(err)
		}
		site := filepath.Join(root, "site")
		content := filepath.Join(site, "pages")
		if err := os.MkdirAll(content, 0o755); err != nil {
			t.Fatal(err)
		}
		writeMintlifyConfig(t, site)
		if p, r, _ := detectProfile(content, nil); p.Name != ProfileMintlify || r != site {
			t.Errorf("detectProfile = (%q, %q), want (mintlify, %q)", p.Name, r, site)
		}
	})

	t.Run("nested hugo beats a farther mintlify marker", func(t *testing.T) {
		root := t.TempDir()
		writeMintlifyConfig(t, root)
		site := filepath.Join(root, "site")
		content := filepath.Join(site, "content")
		if err := os.MkdirAll(content, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(site, "layouts"), 0o755); err != nil {
			t.Fatal(err)
		}
		if p, r, _ := detectProfile(content, nil); p.Name != ProfileHugo || r != site {
			t.Errorf("detectProfile = (%q, %q), want (hugo, %q)", p.Name, r, site)
		}
	})

	// Same level, both markers present and the docs.json is a genuine Mintlify
	// config: hugo still wins, because layouts/ is the stronger evidence and a
	// Hugo site that also ships a docs.json must keep tracing shortcodes.
	t.Run("same directory: hugo wins the tie", func(t *testing.T) {
		root := t.TempDir()
		content := filepath.Join(root, "docs")
		if err := os.MkdirAll(content, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(root, "layouts"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeMintlifyConfig(t, root)
		if p, r, _ := detectProfile(content, nil); p.Name != ProfileHugo || r != root {
			t.Errorf("detectProfile = (%q, %q), want (hugo, %q)", p.Name, r, root)
		}
	})

	// A Hugo site carrying an unrelated docs.json is not even a tie: the
	// content predicate rejects the marker outright, so nothing about the
	// Mintlify profile is in play (#7 review).
	t.Run("a generic docs.json does not select mintlify", func(t *testing.T) {
		root, content := hugoTree(t)
		if err := os.WriteFile(filepath.Join(root, "docs.json"),
			[]byte(`{"generatedBy":"some-other-tool","files":["a","b"]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if p, r, _ := detectProfile(content, nil); p.Name != ProfileHugo || r != root {
			t.Errorf("detectProfile = (%q, %q), want (hugo, %q)", p.Name, r, root)
		}
	})

	// A supplied project root no longer biases detection at all: a Mintlify
	// tree stays mintlify even when the user names its root, which used to
	// select hugo and capture "Snippet" as a component name (#7 review).
	t.Run("a supplied project root does not short-circuit to hugo", func(t *testing.T) {
		root, content := mintlifyTree(t, "docs.json")
		cfg := Config{ContentDir: content, ProjectRoot: root}
		if err := cfg.ApplyProfile(); err != nil {
			t.Fatalf("ApplyProfile: %v", err)
		}
		if cfg.ResolvedProfile.Name != ProfileMintlify || cfg.ProjectRoot != root {
			t.Errorf("profile/root = %q/%q, want mintlify/%q",
				cfg.ResolvedProfile.Name, cfg.ProjectRoot, root)
		}
	})
}

// TestMintlifyMarkerPredicate covers the content check behind the docs.json /
// mint.json markers: a file merely named docs.json must not select the
// mintlify profile, and neither must one that cannot be parsed — a malformed
// config fails the predicate rather than erroring the run (#7 review).
func TestMintlifyMarkerPredicate(t *testing.T) {
	mint := mustProfile(ProfileMintlify)

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"real docs.json", mintlifyConfigJSON, true},
		{"navigation only", `{"navigation":{"pages":["a"]}}`, true},
		{"legacy mint.json shape", `{"name":"D","navigation":[{"group":"g","pages":["a"]}],"colors":{"primary":"#111"}}`, true},
		{"schema url only", `{"$schema":"https://mintlify.com/docs.json"}`, true},
		{"theme only", `{"theme":"maple"}`, true},
		{"unrelated tool", `{"generatedBy":"some-other-tool","files":["a"]}`, false},
		{"name alone is not enough", `{"name":"anything"}`, false},
		{"empty object", `{}`, false},
		{"json array", `[{"navigation":1}]`, false},
		{"malformed json", `{"navigation":`, false},
		{"not json at all", "# just a markdown file\n", false},
		{"empty file", "", false},
		{"schema for another tool", `{"$schema":"https://example.com/other.json"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			content := filepath.Join(root, "docs")
			if err := os.MkdirAll(content, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "docs.json")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := isMintlifyConfig(path)
			if err != nil {
				t.Fatalf("isMintlifyConfig returned an error for a readable file: %v", err)
			}
			if got != tt.want {
				t.Errorf("isMintlifyConfig = %v, want %v", got, tt.want)
			}

			// End to end: the predicate decides whether the marker selects the
			// profile at all.
			wantRoot := ""
			if tt.want {
				wantRoot = root
			}
			if got := mint.DetectRoot(content, nil); got != wantRoot {
				t.Errorf("Profile.DetectRoot = %q, want %q", got, wantRoot)
			}
			wantName := ProfileMarkdown
			if tt.want {
				wantName = ProfileMintlify
			}
			if p, _, _ := detectProfile(content, nil); p.Name != wantName {
				t.Errorf("detectProfile = %q, want %q", p.Name, wantName)
			}
		})
	}

	// A directory named docs.json is still rejected by kind before the
	// predicate ever runs, and the predicate itself says no as well.
	root := t.TempDir()
	dirMarker := filepath.Join(root, "docs.json")
	if err := os.Mkdir(dirMarker, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, _ := isMintlifyConfig(dirMarker); got {
		t.Error("isMintlifyConfig(directory) = true, want false")
	}
	if got := mint.DetectRoot(root, nil); got != "" {
		t.Errorf("Profile.DetectRoot(dir named docs.json) = %q, want \"\"", got)
	}

	// A profile with no markers has no root to detect.
	if got := mustProfile(ProfileMarkdown).DetectRoot(root, nil); got != "" {
		t.Errorf("markdown DetectRoot = %q, want \"\"", got)
	}
}

// TestWalkUp_BoundedByGitRepo pins the repository bound on auto-detection: the
// upward walk stops at the directory holding .git, so a checkout that happens
// to live under an ancestor named themes/ or layouts/ is not mistaken for a
// Hugo site rooted outside the repository. A marker at the repository root
// itself is still found, and with no .git anywhere the walk reaches the
// filesystem root as before.
func TestWalkUp_BoundedByGitRepo(t *testing.T) {
	hugo := mustProfile(ProfileHugo)

	mkdirs := func(t *testing.T, dirs ...string) {
		t.Helper()
		for _, d := range dirs {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("repo under an ancestor named themes stays markdown", func(t *testing.T) {
		tmp := t.TempDir()
		proj := filepath.Join(tmp, "themes", "proj")
		content := filepath.Join(proj, "docs")
		mkdirs(t, content, filepath.Join(proj, ".git"))
		if err := os.WriteFile(filepath.Join(content, "a.md"), []byte("# a\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := detectRootByMarkers(content, hugo.RootMarkers); got != "" {
			t.Errorf("DetectRoot = %q, want \"\" (ancestor themes/ is outside the repo)", got)
		}
		if p, root, _ := detectProfile(content, nil); p.Name != ProfileMarkdown || root != "" {
			t.Errorf("detectProfile = (%q, %q), want (markdown, \"\")", p.Name, root)
		}
	})

	t.Run("dot-git worktree file bounds the walk too", func(t *testing.T) {
		tmp := t.TempDir()
		proj := filepath.Join(tmp, "layouts", "proj")
		content := filepath.Join(proj, "docs")
		mkdirs(t, content)
		gitdir := "gitdir: " + filepath.Join(tmp, "elsewhere", ".git", "worktrees", "w") + "\n"
		if err := os.WriteFile(filepath.Join(proj, ".git"), []byte(gitdir), 0o600); err != nil {
			t.Fatal(err)
		}
		if p, root, _ := detectProfile(content, nil); p.Name != ProfileMarkdown || root != "" {
			t.Errorf("detectProfile = (%q, %q), want (markdown, \"\")", p.Name, root)
		}
	})

	t.Run("submodule content dir is walked through to the parent site", func(t *testing.T) {
		// A Hugo site whose content/ was added with `git submodule add`: the
		// submodule's .git is a file pointing into the parent's
		// .git/modules/content, and the site's markers live one level up.
		tmp := t.TempDir()
		site := filepath.Join(tmp, "site")
		content := filepath.Join(site, "content")
		mkdirs(t, content, filepath.Join(site, ".git"), filepath.Join(site, "layouts", "shortcodes"))
		if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte("baseURL = 'x'\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitdir := "gitdir: " + filepath.Join(site, ".git", "modules", "content") + "\n"
		if err := os.WriteFile(filepath.Join(content, ".git"), []byte(gitdir), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := detectRootByMarkers(content, hugo.RootMarkers); got != site {
			t.Errorf("DetectRoot = %q, want the parent site %q", got, site)
		}
		if p, root, _ := detectProfile(content, nil); p.Name != ProfileHugo || root != site {
			t.Errorf("detectProfile = (%q, %q), want (hugo, %q)", p.Name, root, site)
		}
		// End to end through ApplyProfile, the way main.go resolves a run.
		cfg := &Config{ContentDir: content}
		if err := cfg.ApplyProfile(); err != nil {
			t.Fatalf("ApplyProfile: %v", err)
		}
		if cfg.ResolvedProfile.Name != ProfileHugo || !cfg.ProfileAuto {
			t.Errorf("ResolvedProfile = %q (auto=%v), want hugo auto-detected",
				cfg.ResolvedProfile.Name, cfg.ProfileAuto)
		}
		if cfg.ProjectRoot != site {
			t.Errorf("ProjectRoot = %q, want %q", cfg.ProjectRoot, site)
		}
	})

	t.Run("submodule checked out as a linked worktree is walked through", func(t *testing.T) {
		// The shape the submodule exception exists for, in its hardest form: a
		// Hugo site whose content/ submodule is checked out as a linked
		// worktree, so git writes a pointer that carries BOTH "worktrees" and
		// "modules" (.git/worktrees/<wt>/modules/<path>). It is still a
		// submodule checkout, so the walk must continue up to the site.
		tmp := t.TempDir()
		site := filepath.Join(tmp, "site")
		content := filepath.Join(site, "content")
		mkdirs(t, content, filepath.Join(site, "layouts", "shortcodes"))
		if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte("baseURL = 'x'\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(content, ".git"),
			[]byte("gitdir: ../../p/.git/worktrees/wt/modules/content\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := detectRootByMarkers(content, hugo.RootMarkers); got != site {
			t.Errorf("DetectRoot = %q, want the parent site %q", got, site)
		}
		cfg := &Config{ContentDir: content}
		if err := cfg.ApplyProfile(); err != nil {
			t.Fatalf("ApplyProfile: %v", err)
		}
		if cfg.ResolvedProfile.Name != ProfileHugo || !cfg.ProfileAuto || cfg.ProjectRoot != site {
			t.Errorf("ApplyProfile = %q (auto=%v, root=%q), want hugo auto-detected at %q",
				cfg.ResolvedProfile.Name, cfg.ProfileAuto, cfg.ProjectRoot, site)
		}
	})

	t.Run("linked worktree inside a submodule bounds the walk", func(t *testing.T) {
		// The mirror image: .git/modules/<sub>/worktrees/<wt> is a worktree
		// checkout of a submodule, a checkout root of its own. The walk must
		// stop there and not escape into an unrelated Hugo ancestor.
		tmp := t.TempDir()
		outer := filepath.Join(tmp, "outer")
		proj := filepath.Join(outer, "proj")
		content := filepath.Join(proj, "docs")
		mkdirs(t, content, filepath.Join(outer, "layouts"))
		if err := os.WriteFile(filepath.Join(outer, "hugo.toml"), []byte("baseURL = 'x'\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proj, ".git"),
			[]byte("gitdir: /r/.git/modules/sub/worktrees/wt\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := detectRootByMarkers(content, hugo.RootMarkers); got != "" {
			t.Errorf("DetectRoot = %q, want \"\" (the walk must stop at the worktree root)", got)
		}
		cfg := &Config{ContentDir: content}
		if err := cfg.ApplyProfile(); err != nil {
			t.Fatalf("ApplyProfile: %v", err)
		}
		if cfg.ResolvedProfile.Name != ProfileMarkdown || cfg.ProjectRoot != "" {
			t.Errorf("ApplyProfile = %q (root=%q), want markdown with no root",
				cfg.ResolvedProfile.Name, cfg.ProjectRoot)
		}
	})

	t.Run("genuine hugo repo still detected", func(t *testing.T) {
		tmp := t.TempDir()
		site := filepath.Join(tmp, "site")
		content := filepath.Join(site, "content")
		mkdirs(t, content, filepath.Join(site, ".git"), filepath.Join(site, "layouts"))
		if err := os.WriteFile(filepath.Join(content, "a.md"), []byte("# a\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := detectRootByMarkers(content, hugo.RootMarkers); got != site {
			t.Errorf("DetectRoot = %q, want %q", got, site)
		}
		if p, root, _ := detectProfile(content, nil); p.Name != ProfileHugo || root != site {
			t.Errorf("detectProfile = (%q, %q), want (hugo, %q)", p.Name, root, site)
		}
	})

	t.Run("marker at the same level as dot-git is found", func(t *testing.T) {
		tmp := t.TempDir()
		content := filepath.Join(tmp, "content", "docs")
		mkdirs(t, content, filepath.Join(tmp, ".git"))
		if err := os.WriteFile(filepath.Join(tmp, "hugo.toml"), []byte("baseURL = 'x'\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := detectRootByMarkers(content, hugo.RootMarkers); got != tmp {
			t.Errorf("DetectRoot = %q, want the repo root %q", got, tmp)
		}
		if p, root, _ := detectProfile(content, nil); p.Name != ProfileHugo || root != tmp {
			t.Errorf("detectProfile = (%q, %q), want (hugo, %q)", p.Name, root, tmp)
		}
	})

	t.Run("no dot-git anywhere still walks up", func(t *testing.T) {
		tmp := t.TempDir()
		content := filepath.Join(tmp, "themes", "proj", "docs")
		mkdirs(t, content)
		if got := detectRootByMarkers(content, hugo.RootMarkers); got != tmp {
			t.Errorf("DetectRoot = %q, want %q (unbounded walk without a repo)", got, tmp)
		}
		if p, root, _ := detectProfile(content, nil); p.Name != ProfileHugo || root != tmp {
			t.Errorf("detectProfile = (%q, %q), want (hugo, %q)", p.Name, root, tmp)
		}
	})
}

// TestGitFileIsSubmodule covers the two meanings of a .git *file*: a linked
// worktree pointer, which bounds the upward walk, and a submodule pointer into
// the parent repository's .git/modules/, which does not. The two nest in both
// directions, so the four real pointer shapes are pinned explicitly: the
// decision follows the *tail* of the path (a trailing "worktrees/<name>" is a
// worktree whatever it sits inside), never mere adjacency of ".git" and
// "modules". Anything unparsable is treated as a bound.
func TestGitFileIsSubmodule(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		// The four real shapes git writes, in the order documented on
		// gitFileIsSubmodule.
		{"shape: worktree", "gitdir: /r/.git/worktrees/wt\n", false},
		{"shape: submodule", "gitdir: ../.git/modules/content\n", true},
		{"shape: submodule inside a linked worktree", "gitdir: ../../p/.git/worktrees/wt/modules/content\n", true},
		{"shape: linked worktree inside a submodule", "gitdir: /r/.git/modules/sub/worktrees/wt\n", false},

		{"submodule absolute", "gitdir: /x/.git/modules/content\n", true},
		{"submodule relative", "gitdir: ../.git/modules/content\n", true},
		{"submodule nested", "gitdir: /x/.git/modules/a/modules/b\n", true},
		{"submodule no trailing newline", "gitdir: /x/.git/modules/content", true},
		{"worktree", "gitdir: /x/.git/worktrees/w\n", false},
		{"worktree under a dir named modules", "gitdir: /x/modules/y/.git/worktrees/w\n", false},
		{"plain gitdir", "gitdir: /elsewhere\n", false},
		{"empty target", "gitdir:\n", false},
		{"garbage", "not a git file at all\n", false},
		{"empty file", "", false},
		{"modules only after the first line", "gitdir: /x/.git/worktrees/w\ngitdir: /x/.git/modules/c\n", false},
		{"crlf submodule", "gitdir: ../.git/modules/content\r\n", true},
		{"crlf worktree", "gitdir: /r/.git/worktrees/wt\r\n", false},
		{"leading and trailing space", "  gitdir:   ../.git/modules/content   \n", true},
		{"windows separators submodule", `gitdir: ..\..\p\.git\worktrees\wt\modules\content` + "\n", true},
		{"windows separators worktree", `gitdir: C:\r\.git\modules\sub\worktrees\wt` + "\n", false},
		{"trailing worktrees with no name", "gitdir: /x/.git/modules/c/worktrees/\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gitFileIsSubmodule(tt.content); got != tt.want {
				t.Errorf("gitFileIsSubmodule(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

// TestIsRepoRoot pins the bound decision itself: a .git directory always stops
// the walk, a worktree .git file stops it, a submodule .git file does not, and
// an unreadable or unparsable .git file stops it (conservative).
func TestIsRepoRoot(t *testing.T) {
	writeGit := func(t *testing.T, content string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Run("dot-git directory stops", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if !IsRepoRoot(dir) {
			t.Error("IsRepoRoot = false, want true for a .git directory")
		}
	})

	t.Run("no dot-git does not stop", func(t *testing.T) {
		if IsRepoRoot(t.TempDir()) {
			t.Error("IsRepoRoot = true, want false with no .git entry")
		}
	})

	t.Run("worktree file stops", func(t *testing.T) {
		if !IsRepoRoot(writeGit(t, "gitdir: /x/.git/worktrees/w\n")) {
			t.Error("IsRepoRoot = false, want true for a linked worktree")
		}
	})

	t.Run("submodule file does not stop", func(t *testing.T) {
		if IsRepoRoot(writeGit(t, "gitdir: /x/.git/modules/content\n")) {
			t.Error("IsRepoRoot = true, want false for a submodule checkout")
		}
	})

	t.Run("garbage file stops", func(t *testing.T) {
		if !IsRepoRoot(writeGit(t, "\x00 not a pointer\n")) {
			t.Error("IsRepoRoot = false, want true for an unparsable .git file")
		}
	})

	t.Run("symlinked dot-git directory stops", func(t *testing.T) {
		tmp := t.TempDir()
		real := filepath.Join(tmp, "real.git")
		if err := os.MkdirAll(real, 0o755); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(tmp, "checkout")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(dir, ".git")); err != nil {
			t.Skipf("symlinks unsupported: %v", err)
		}
		if !IsRepoRoot(dir) {
			t.Error("IsRepoRoot = false, want true for a symlinked .git directory")
		}
	})
}

// TestKnownContentExtensions pins the union used by the analyzer's
// partial-scan diagnostic (#11): sorted, de-duplicated, and a superset of
// every built-in profile's ContentExtensions.
func TestKnownContentExtensions(t *testing.T) {
	got := KnownContentExtensions()
	if !sort.StringsAreSorted(got) {
		t.Errorf("KnownContentExtensions not sorted: %v", got)
	}
	seen := make(map[string]int, len(got))
	for _, e := range got {
		seen[e]++
		if seen[e] > 1 {
			t.Errorf("duplicate extension %q in %v", e, got)
		}
		if !strings.HasPrefix(e, ".") {
			t.Errorf("extension %q is not dot-prefixed", e)
		}
	}
	for _, p := range AllProfiles() {
		for _, e := range p.ContentExtensions {
			if _, ok := seen[e]; !ok {
				t.Errorf("profile %q extension %q missing from the union %v", p.Name, e, got)
			}
		}
	}
	// .mdx comes only from the hugo profile, so the union must be strictly
	// wider than the default profile's list.
	if len(got) <= len(DefaultProfile().ContentExtensions) {
		t.Errorf("union %v is not wider than the markdown profile's list", got)
	}
}

// TestNormalizeExtensions pins the canonical form the analyzer's walk matches
// on: lowercase, trimmed, dot-prefixed, blanks dropped, duplicates removed,
// input order preserved, and nil when nothing survives (so len() == 0 still
// means "not set").
func TestNormalizeExtensions(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nil stays nil", in: nil, want: nil},
		{name: "blanks only yield nil", in: []string{"", "   ", "\t"}, want: nil},
		{name: "already canonical is unchanged", in: []string{".md", ".markdown"}, want: []string{".md", ".markdown"}},
		{
			name: "lowercases, dot-prefixes, trims, de-duplicates, keeps order",
			in:   []string{"mdx", ".MD", "", ".md", "  .Markdown  ", "MDX"},
			want: []string{".mdx", ".md", ".markdown"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeExtensions(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NormalizeExtensions(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestNormalizeExtensions_DoesNotAliasInput: the returned slice must be fresh,
// so ApplyProfile filling ContentExtensions from a profile cannot let a later
// mutation reach the registry.
func TestNormalizeExtensions_DoesNotAliasInput(t *testing.T) {
	in := []string{".md", ".mdx"}
	got := NormalizeExtensions(in)
	got[0] = ".changed"
	if in[0] != ".md" {
		t.Errorf("input mutated: %v", in)
	}
}

// TestApplyProfile_ExplicitRootMustExist covers the fail-fast check on a
// project root the user named. Before it, "--project-root /does/not/exist"
// was accepted in silence: the banner printed the bogus root, every include
// resolved to nothing, every reusable was reported "unknown" and the run
// exited 0 — a quietly useless report (#7 review pass 2).
func TestApplyProfile_ExplicitRootMustExist(t *testing.T) {
	content := plainTree(t)
	missing := filepath.Join(t.TempDir(), "nope")

	t.Run("project_root that does not exist is an error naming the flag", func(t *testing.T) {
		cfg := Config{ContentDir: content, Profile: ProfileMintlify, ProjectRoot: missing}
		err := cfg.ApplyProfile()
		if err == nil {
			t.Fatal("ApplyProfile() = nil, want an error for a nonexistent project root")
		}
		if !strings.Contains(err.Error(), strconv.Quote(missing)) {
			t.Errorf("error does not name the path: %v", err)
		}
		if !strings.Contains(err.Error(), "--project-root") || !strings.Contains(err.Error(), "project_root") {
			t.Errorf("error does not name the flag/key that supplied it: %v", err)
		}
	})

	t.Run("legacy hugo_root that does not exist names that key", func(t *testing.T) {
		cfg := Config{ContentDir: content, Profile: ProfileHugo, legacyHugoRoot: missing}
		err := cfg.ApplyProfile()
		if err == nil {
			t.Fatal("ApplyProfile() = nil, want an error")
		}
		if !strings.Contains(err.Error(), `"hugo_root"`) {
			t.Errorf("error does not name hugo_root: %v", err)
		}
		if strings.Contains(err.Error(), "--project-root") {
			t.Errorf("error blames the wrong knob: %v", err)
		}
	})

	t.Run("a dangling symlink does not exist either", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(missing, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		cfg := Config{ContentDir: content, Profile: ProfileMintlify, ProjectRoot: link}
		if err := cfg.ApplyProfile(); err == nil {
			t.Fatal("ApplyProfile() = nil, want an error for a dangling symlink")
		}
	})

	t.Run("a file is not a directory", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "docs.json")
		if err := os.WriteFile(file, []byte(mintlifyConfigJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := Config{ContentDir: content, Profile: ProfileMintlify, ProjectRoot: file}
		err := cfg.ApplyProfile()
		if err == nil {
			t.Fatal("ApplyProfile() = nil, want an error for a file")
		}
		if !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("error = %v, want it to say the root is not a directory", err)
		}
	})

	t.Run("a symlink to a real directory is accepted", func(t *testing.T) {
		real := t.TempDir()
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(real, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		cfg := Config{ContentDir: content, Profile: ProfileMintlify, ProjectRoot: link}
		if err := cfg.ApplyProfile(); err != nil {
			t.Fatalf("ApplyProfile() = %v, want a symlinked directory to be accepted", err)
		}
		if cfg.ProjectRoot != link {
			t.Errorf("ProjectRoot = %q, want %q", cfg.ProjectRoot, link)
		}
		if !cfg.RootFromUser {
			t.Error("RootFromUser = false, want true")
		}
	})

	t.Run("an auto-detected root is not affected", func(t *testing.T) {
		root := t.TempDir()
		docs := filepath.Join(root, "docs")
		if err := os.MkdirAll(docs, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "docs.json"), []byte(mintlifyConfigJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := Config{ContentDir: docs}
		if err := cfg.ApplyProfile(); err != nil {
			t.Fatalf("ApplyProfile() = %v", err)
		}
		if cfg.ResolvedProfile.Name != ProfileMintlify || cfg.ProjectRoot != root {
			t.Errorf("profile/root = %q/%q, want mintlify/%q", cfg.ResolvedProfile.Name, cfg.ProjectRoot, root)
		}
		if cfg.RootFromUser {
			t.Error("RootFromUser = true for a detected root, want false")
		}
	})

	t.Run("no root at all is not affected", func(t *testing.T) {
		cfg := Config{ContentDir: content}
		if err := cfg.ApplyProfile(); err != nil {
			t.Fatalf("ApplyProfile() = %v", err)
		}
		if cfg.RootFromUser {
			t.Error("RootFromUser = true with no root configured, want false")
		}
	})
}

// TestApplyProfile_UnreadableMarkerWarns pins the diagnostic for a marker
// candidate the predicate could not read: a chmod 000 docs.json used to
// downgrade a real Mintlify project to the markdown profile with no signal at
// all. Detection is unchanged — the marker still does not select the profile —
// but the reason is now recorded on Config.Warnings for the CLI to print
// (#7 review pass 2).
func TestApplyProfile_UnreadableMarkerWarns(t *testing.T) {
	newTree := func(t *testing.T, docsJSON string) (root, content string) {
		t.Helper()
		root = t.TempDir()
		content = filepath.Join(root, "docs")
		if err := os.MkdirAll(content, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "docs.json"), []byte(docsJSON), 0o600); err != nil {
			t.Fatal(err)
		}
		return root, content
	}

	t.Run("unreadable docs.json warns and detection falls through", func(t *testing.T) {
		root, content := newTree(t, mintlifyConfigJSON)
		marker := filepath.Join(root, "docs.json")
		makeUnreadable(t, marker)

		cfg := Config{ContentDir: content}
		if err := cfg.ApplyProfile(); err != nil {
			t.Fatalf("ApplyProfile() = %v, want the unreadable marker to be non-fatal", err)
		}
		if cfg.ResolvedProfile.Name != ProfileMarkdown {
			t.Errorf("profile = %q, want the unreadable marker not to select mintlify", cfg.ResolvedProfile.Name)
		}
		if len(cfg.Warnings) != 1 {
			t.Fatalf("warnings = %v, want exactly one", cfg.Warnings)
		}
		w := cfg.Warnings[0]
		if !strings.Contains(w, marker) {
			t.Errorf("warning does not name the path: %s", w)
		}
		if !strings.Contains(w, ProfileMintlify) {
			t.Errorf("warning does not name the profile: %s", w)
		}
	})

	// The warning is about the *marker*, not about the profile: a directory
	// holding an unreadable docs.json next to a readable mint.json still
	// selects mintlify, and saying "mintlify was not selected" there would be
	// plainly false (#7 review pass 3).
	t.Run("an unreadable marker alongside a readable one still selects the profile", func(t *testing.T) {
		root, content := newTree(t, mintlifyConfigJSON)
		marker := filepath.Join(root, "docs.json")
		makeUnreadable(t, marker)
		if err := os.WriteFile(filepath.Join(root, "mint.json"), []byte(mintlifyConfigJSON), 0o600); err != nil {
			t.Fatal(err)
		}

		cfg := Config{ContentDir: content}
		if err := cfg.ApplyProfile(); err != nil {
			t.Fatalf("ApplyProfile() = %v", err)
		}
		if cfg.ResolvedProfile.Name != ProfileMintlify || cfg.ProjectRoot != root {
			t.Fatalf("profile/root = %q/%q, want mintlify/%q",
				cfg.ResolvedProfile.Name, cfg.ProjectRoot, root)
		}
		if len(cfg.Warnings) != 1 {
			t.Fatalf("warnings = %v, want exactly one (the unreadable docs.json)", cfg.Warnings)
		}
		w := cfg.Warnings[0]
		if !strings.Contains(w, "docs.json") || !strings.Contains(w, "marker was skipped") {
			t.Errorf("warning should be scoped to the marker: %s", w)
		}
		if strings.Contains(w, "does not select the profile") ||
			strings.Contains(w, "detection continued without it") {
			t.Errorf("warning claims the profile was not selected, but it was: %s", w)
		}
	})

	t.Run("a non-Mintlify docs.json is not a warning", func(t *testing.T) {
		_, content := newTree(t, `{"generatedBy":"some-other-tool"}`)
		cfg := Config{ContentDir: content}
		if err := cfg.ApplyProfile(); err != nil {
			t.Fatalf("ApplyProfile() = %v", err)
		}
		if cfg.ResolvedProfile.Name != ProfileMarkdown {
			t.Errorf("profile = %q, want markdown", cfg.ResolvedProfile.Name)
		}
		if len(cfg.Warnings) != 0 {
			t.Errorf("warnings = %v, want none: not matching is not a problem", cfg.Warnings)
		}
	})

	t.Run("a readable Mintlify docs.json is not a warning", func(t *testing.T) {
		root, content := newTree(t, mintlifyConfigJSON)
		cfg := Config{ContentDir: content}
		if err := cfg.ApplyProfile(); err != nil {
			t.Fatalf("ApplyProfile() = %v", err)
		}
		if cfg.ResolvedProfile.Name != ProfileMintlify || cfg.ProjectRoot != root {
			t.Errorf("profile/root = %q/%q, want mintlify/%q", cfg.ResolvedProfile.Name, cfg.ProjectRoot, root)
		}
		if len(cfg.Warnings) != 0 {
			t.Errorf("warnings = %v, want none", cfg.Warnings)
		}
	})

	t.Run("warnings do not accumulate across calls", func(t *testing.T) {
		root, content := newTree(t, mintlifyConfigJSON)
		marker := filepath.Join(root, "docs.json")
		makeUnreadable(t, marker)

		cfg := Config{ContentDir: content}
		for i := 0; i < 3; i++ {
			cfg.ResolvedProfile = Profile{}
			if err := cfg.ApplyProfile(); err != nil {
				t.Fatalf("ApplyProfile() = %v", err)
			}
			if len(cfg.Warnings) != 1 {
				t.Fatalf("after %d calls warnings = %v, want exactly one", i+1, cfg.Warnings)
			}
		}
	})
}

// TestIsMintlifyConfig_SizeCap pins the read cap. The predicate streams the
// document key by key, but encoding/json still buffers each top-level value
// whole before the key can be judged, so a docs.json with a huge first value
// used to allocate all of it (a 200 MB filler string drove peak RSS to
// ~700 MB). Reading through an io.LimitReader bounds that: past the cap the
// document is truncated, fails to decode, and is simply not a match
// (#7 review pass 2).
func TestIsMintlifyConfig_SizeCap(t *testing.T) {
	write := func(t *testing.T, fillerLen int) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "docs.json")
		f, err := os.Create(path) //nolint:gosec // test-controlled path
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if _, err := f.WriteString(`{"filler":"` + strings.Repeat("x", fillerLen) + `","navigation":{}}`); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// Comfortably under the cap once the surrounding syntax is counted: the
	// deciding "navigation" key is still reached.
	under := write(t, maxMintlifyConfigBytes-1024)
	got, err := isMintlifyConfig(under)
	if err != nil {
		t.Fatalf("isMintlifyConfig(under the cap) error = %v", err)
	}
	if !got {
		t.Error("isMintlifyConfig(under the cap) = false, want true")
	}

	// Just over: the filler value is cut off, the document no longer parses,
	// and "navigation" is never seen.
	over := write(t, maxMintlifyConfigBytes+1024)
	got, err = isMintlifyConfig(over)
	if err != nil {
		t.Fatalf("isMintlifyConfig(over the cap) error = %v", err)
	}
	if got {
		t.Error("isMintlifyConfig(over the cap) = true, want false: the file is truncated at the cap")
	}
}

// makeUnreadable chmods path so it cannot be read, and skips the test when the
// platform does not honour that. os.Chmod on Windows only toggles the
// read-only attribute, so a 0o000 file is still perfectly readable there, and
// root ignores the mode entirely — in both cases the scenario under test
// cannot be set up at all, which is not a failure of the code.
func makeUnreadable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o000); err != nil {
		t.Skipf("chmod unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skipf("%s is still readable after chmod 000 (Windows, or running as root)", path)
	}
}

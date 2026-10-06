package parser

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/testutil"
)

// renamedProfileRP deliberately changes the registry identity before handing
// the parser only its behavior and the effective reusable settings.
func renamedProfileRP(t *testing.T, p config.Profile, rc ReusableConfig) *ReusablePatterns {
	t.Helper()
	p.Name = "synthetic-renamed-profile"
	rc.Capabilities = p.ParserCapabilities
	rp, err := NewReusablePatternsFor(rc)
	if err != nil {
		t.Fatal(err)
	}
	return rp
}

// TestCapabilities_FenceGatesAreIndependent keeps reusable fence policies independent from heading detection.
func TestCapabilities_FenceGatesAreIndependent(t *testing.T) {
	pattern := `include="([^"]+)"`
	body := "# Page\n```\n# Example\ninclude=\"fenced.md\"\n```\ninclude=\"live.md\"\n"
	for _, mask := range []bool{false, true} {
		for _, skip := range []bool{false, true} {
			p := config.Profile{ParserCapabilities: config.ParserCapabilities{
				MaskFencedChunking: mask, SkipFencedCaptures: skip,
			}}
			rp := renamedProfileRP(t, p, ReusableConfig{Patterns: []string{pattern}})
			wantRefs := []string{"fenced.md", "live.md"}
			if skip {
				wantRefs = []string{"live.md"}
			}
			if got := FindReusables(body, rp); !reflect.DeepEqual(got, wantRefs) {
				t.Errorf("mask=%v skip=%v: captures=%v, want %v", mask, skip, got, wantRefs)
			}
			for _, paragraphs := range []bool{false, true} {
				foundExample := false
				for _, chunk := range ParseChunks(body, nil, paragraphs, rp) {
					if chunk.Title == "Example" {
						foundExample = true
					}
				}
				if foundExample {
					t.Errorf("mask=%v skip=%v paragraphs=%v: example heading present=%v", mask, skip, paragraphs, foundExample)
				}
			}
		}
	}
}

func TestCapabilities_RenamedPathRestrictions(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "targets", map[string]string{
		"docs/deep/page.md": "# Page\n", "docs/deep/local.md": "local\n",
		"snippets/root-only.md": "shared only\n", "root-only.md": "root only\n",
		"snippets/fallback.md": "shared parent fallback\n", "absolute.md": "absolute\n",
		"docs/deep/upper.MD": "upper\n", "docs/deep/custom.mdx": "override extension\n",
	})
	p, _ := config.LookupProfile(config.ProfileGitBook)
	rp := renamedProfileRP(t, p, ReusableConfig{
		Patterns: []string{`include="([^"]+)"`}, Extensions: []string{".mdx", ".md"},
		Root: repo.Dir, Resolver: p.Resolver,
	})
	page := repo.Path("docs/deep/page.md")
	for _, tc := range []struct{ ref, want string }{
		{"local.md#heading", "docs/deep/local.md"},
		{"local.md?view=1#heading", "docs/deep/local.md"},
		{"/absolute.md", "absolute.md"},
		// Effective user extension overrides still apply to extensionless captures.
		{"custom", "docs/deep/custom.mdx"},
	} {
		if got, ok := rp.resolveDirectPath(tc.ref, page); !ok || got != repo.Path(tc.want) {
			t.Errorf("resolve %q = %q, %v; want %q", tc.ref, got, ok, tc.want)
		}
	}
	for _, ref := range []string{"root-only.md", "./root-only.md", "../snippets/fallback.md", "custom.mdx", "upper.MD"} {
		if got, ok := rp.resolveDirectPath(ref, page); ok {
			t.Errorf("restricted reference %q resolved to %q", ref, got)
		}
	}
	if got := FindReusables(`include="https://example.com/remote.md" include="local.md"`, rp); !reflect.DeepEqual(got, []string{"local.md"}) {
		t.Errorf("custom pattern captures = %v", got)
	}
	if _, ok := rp.resolveDirectPath("local.md", ""); ok {
		t.Error("page-only reference resolved without a page")
	}
	if got, ok := rp.resolveDirectPath("/absolute.md", ""); !ok || got != repo.Path("absolute.md") {
		t.Errorf("absolute reference without page = %q, %v", got, ok)
	}
}

func TestCapabilities_ExplicitExtensionsNilAndEmpty(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "targets", map[string]string{
		"page.md": "page\n", "note.mdx": "note\n", "upper.MD": "upper\n",
	})
	for _, tc := range []struct {
		name       string
		allowed    []string
		mdx, upper bool
	}{
		{"nil unrestricted", nil, true, true},
		{"empty rejects explicit", []string{}, false, false},
		{"exact lowercase", []string{".mdx"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rp := renamedProfileRP(t, config.Profile{ParserCapabilities: config.ParserCapabilities{
				AllowedExplicitPathExtensions: tc.allowed,
			}}, ReusableConfig{Extensions: []string{".mdx"}, Root: repo.Dir})
			for _, ref := range []struct {
				name string
				want bool
			}{
				{"note.mdx", tc.mdx}, {"upper.MD", tc.upper}, {"note", true},
			} {
				if _, ok := rp.resolveDirectPath(ref.name, repo.Path("page.md")); ok != ref.want {
					t.Errorf("resolve %q = %v, want %v", ref.name, ok, ref.want)
				}
			}
		})
	}
}

func TestCapabilities_IndexOrderingAndCopies(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "targets", map[string]string{
		"page.md": "page\n", "guide/index.mdx": "first extension wins\n",
		"guide/start.md": "first name second extension\n", "guide/start.mdx": "first name wins\n",
		"ordered/index.mdx": "first extension wins\n", "ordered/start.md": "first name second extension\n",
		"default/index.mdx": "default index\n",
	})
	extensions := []string{".mdx", ".md"}
	allowed := []string{".mdx"}
	indexes := []string{"start", "index"}
	rp := renamedProfileRP(t, config.Profile{ParserCapabilities: config.ParserCapabilities{
		AllowedExplicitPathExtensions: allowed, IndexFileNames: indexes,
	}}, ReusableConfig{Extensions: extensions, Root: repo.Dir})
	// All slices passed at construction belong to the caller.
	extensions[0], allowed[0], indexes[0] = ".json", ".json", "changed"
	page := repo.Path("page.md")
	for _, tc := range []struct{ ref, want string }{
		{"guide", "guide/start.mdx"}, {"guide/index.mdx", "guide/index.mdx"},
	} {
		if got, ok := rp.resolveDirectPath(tc.ref, page); !ok || got != repo.Path(tc.want) {
			t.Errorf("after mutation %q = %q, %v; want %q", tc.ref, got, ok, tc.want)
		}
	}
	// The extension-major ordering must try index.mdx before start.md.
	rp = renamedProfileRP(t, config.Profile{ParserCapabilities: config.ParserCapabilities{
		IndexFileNames: []string{"start", "index"},
	}}, ReusableConfig{Extensions: []string{".mdx", ".md"}, Root: repo.Dir})
	if got, ok := rp.resolveDirectPath("ordered", page); !ok || got != repo.Path("ordered/index.mdx") {
		t.Errorf("extension-major lookup = %q, %v", got, ok)
	}
	for _, tc := range []struct {
		names []string
		want  bool
	}{
		{nil, true}, {[]string{}, false},
	} {
		rp = renamedProfileRP(t, config.Profile{ParserCapabilities: config.ParserCapabilities{
			IndexFileNames: tc.names,
		}}, ReusableConfig{Extensions: []string{".mdx"}, Root: repo.Dir})
		if _, ok := rp.resolveDirectPath("default", page); ok != tc.want {
			t.Errorf("index names %#v: resolved=%v, want %v", tc.names, ok, tc.want)
		}
	}
}

func TestCapabilities_PathOnlyPrecedesResolverDispatch(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "targets", map[string]string{
		"docs/page.md": "page\n", "docs/direct.md": "direct\n", "legacy/missing.md": "legacy\n",
	})
	for _, pathOnly := range []bool{false, true} {
		rp := renamedProfileRP(t, config.Profile{ParserCapabilities: config.ParserCapabilities{
			PathCapturesOnly: pathOnly,
		}}, ReusableConfig{
			Extensions: []string{".md"}, Root: repo.Path("docs"), ReusablesDir: repo.Path("legacy"),
			Resolver: config.ResolverNone,
		})
		if info, _ := ResolveReusable("missing", repo.Path("docs/page.md"), rp); (info != nil) == pathOnly {
			t.Errorf("pathOnly=%v: legacy result = %+v", pathOnly, info)
		}
		if pathOnly {
			if info, state := ResolveReusable("direct", repo.Path("docs/page.md"), rp); info == nil || state != ResolutionResolved {
				t.Errorf("direct lookup before resolver dispatch = %+v, %v", info, state)
			}
		}
	}
}

func TestCapabilities_AliasSkipPreservesIncludeProvenance(t *testing.T) {
	repo := testutil.NewRepo(t)
	repo.Commit(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "targets", map[string]string{
		"page.md": "page\n", "Present.md": "present include\n",
	})
	p, _ := config.LookupProfile(config.ProfileStarlight)
	for _, skip := range []bool{false, true} {
		p.ParserCapabilities.SkipAliasShapedIncludes = skip
		rp := renamedProfileRP(t, p, ReusableConfig{
			Patterns:   []string{config.MDXComponentPattern, `include="([^"]+)"`},
			Extensions: []string{".md"}, Root: repo.Dir, Resolver: config.ResolverPath,
		})
		// Alias appears first as a component, then as an include. Deduplication
		// must still retain the include's provenance.
		FindReusables(`<Alias /> <OnlyComponent /> include="Alias" include="@parts/footer.md" include="./missing.md" include="Present"`, rp)
		for _, tc := range []struct {
			ref  string
			want Resolution
		}{
			{"Alias", ResolutionUnresolved}, {"@parts/footer.md", ResolutionUnresolved},
			{"./missing.md", ResolutionUnresolved}, {"OnlyComponent", ResolutionUnresolved},
			{"UnseenAlias", ResolutionUnresolved}, {"Present", ResolutionResolved},
		} {
			if skip && (tc.ref == "Alias" || tc.ref == "@parts/footer.md") {
				tc.want = ResolutionSkipped
			}
			if _, state := ResolveReusable(tc.ref, filepath.Join(repo.Dir, "page.md"), rp); state != tc.want {
				t.Errorf("skip=%v ref=%q: state=%v, want %v", skip, tc.ref, state, tc.want)
			}
		}
	}
}

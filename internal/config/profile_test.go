package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
	want := []string{"hugo", "markdown"}
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
}

func TestDetectRoot(t *testing.T) {
	root := t.TempDir()
	content := filepath.Join(root, "content", "docs")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}

	// No marker anywhere up to the filesystem root.
	if got := DetectRoot(content, []string{"layouts/", "docs.json"}); got != "" {
		t.Errorf("DetectRoot(no markers present) = %q, want \"\"", got)
	}
	// Empty marker list never matches.
	if got := DetectRoot(content, nil); got != "" {
		t.Errorf("DetectRoot(nil markers) = %q, want \"\"", got)
	}

	// A directory marker ("layouts/") does not match a regular file of that
	// name: a stray file called layouts must not turn the tree into a Hugo site.
	if err := os.WriteFile(filepath.Join(root, "layouts"), []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := DetectRoot(content, []string{"layouts/"}); got != "" {
		t.Errorf("DetectRoot(dir marker vs file) = %q, want \"\"", got)
	}
	if err := os.Remove(filepath.Join(root, "layouts")); err != nil {
		t.Fatal(err)
	}

	// Directory marker matches a directory.
	if err := os.Mkdir(filepath.Join(root, "layouts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DetectRoot(content, []string{"layouts/"}); got != root {
		t.Errorf("DetectRoot(dir marker) = %q, want %q", got, root)
	}

	// A file marker ("docs.json") does not match a directory of that name.
	mid := filepath.Join(root, "content")
	if err := os.Mkdir(filepath.Join(mid, "docs.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DetectRoot(content, []string{"docs.json", "mint.json"}); got != "" {
		t.Errorf("DetectRoot(file marker vs dir) = %q, want \"\"", got)
	}
	if err := os.Remove(filepath.Join(mid, "docs.json")); err != nil {
		t.Fatal(err)
	}

	// File marker matches a regular file, found at an intermediate level
	// (closest wins).
	if err := os.WriteFile(filepath.Join(mid, "docs.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := DetectRoot(content, []string{"docs.json", "mint.json"}); got != mid {
		t.Errorf("DetectRoot(file marker) = %q, want %q", got, mid)
	}
	// Marker in the content dir itself.
	if got := DetectRoot(mid, []string{"docs.json"}); got != mid {
		t.Errorf("DetectRoot(marker in dir) = %q, want %q", got, mid)
	}
	// Whitespace around a marker is tolerated; a bare "/" marker is ignored.
	if got := DetectRoot(content, []string{" layouts/ ", "/"}); got != root {
		t.Errorf("DetectRoot(padded dir marker) = %q, want %q", got, root)
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
	got := DetectRoot("content", []string{"layouts/"})
	// Compare via os.SameFile: t.TempDir may sit behind a symlink on some OSes.
	a, err1 := os.Stat(got)
	b, err2 := os.Stat(root)
	if err1 != nil || err2 != nil || !os.SameFile(a, b) {
		t.Errorf("DetectRoot(relative) = %q, want %q", got, root)
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
			if got := DetectRoot(content, hugo.RootMarkers); got != root {
				t.Errorf("DetectRoot(%s only) = %q, want %q", marker, got, root)
			}
			if p, r, ok := detectNearest(content, builtinProfiles); !ok || p.Name != ProfileHugo || r != root {
				t.Errorf("detectNearest(%s only) = (%q, %q, %v), want (hugo, %q, true)", marker, p.Name, r, ok, root)
			}
			if p, r := detectProfile(content, ""); p.Name != ProfileHugo || r != root {
				t.Errorf("detectProfile(%s only) = (%q, %q), want (hugo, %q)", marker, p.Name, r, root)
			}
			// A directory of the same name is not a config file.
			if err := os.Remove(filepath.Join(root, marker)); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, marker), 0o755); err != nil {
				t.Fatal(err)
			}
			if got := DetectRoot(content, hugo.RootMarkers); got != "" {
				t.Errorf("DetectRoot(%s as a directory) = %q, want \"\"", marker, got)
			}
		})
	}

	t.Run("both layouts and hugo.toml at one level", func(t *testing.T) {
		root, content := hugoTree(t)
		if err := os.WriteFile(filepath.Join(root, "hugo.toml"), []byte(""), 0o600); err != nil {
			t.Fatal(err)
		}
		if p, r, ok := detectNearest(content, builtinProfiles); !ok || p.Name != ProfileHugo || r != root {
			t.Errorf("detectNearest(both markers) = (%q, %q, %v), want (hugo, %q, true)", p.Name, r, ok, root)
		}
	})

	t.Run("neither marker is markdown", func(t *testing.T) {
		content := plainTree(t)
		if got := DetectRoot(content, hugo.RootMarkers); got != "" {
			t.Errorf("DetectRoot(plain) = %q, want \"\"", got)
		}
		if p, r := detectProfile(content, ""); p.Name != ProfileMarkdown || r != "" {
			t.Errorf("detectProfile(plain) = (%q, %q), want (markdown, \"\")", p.Name, r)
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
	if hasMarker(root, []string{"config/_default/hugo.toml"}) {
		t.Error("hasMarker(nested file marker) = true before the file exists")
	}
	if !hasMarker(root, []string{"config/_default/"}) {
		t.Error("hasMarker(nested dir marker) = false, want true")
	}
	if err := os.WriteFile(filepath.Join(root, "config", "_default", "hugo.toml"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hasMarker(root, []string{"config/_default/hugo.toml"}) {
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
		if got := DetectRoot(content, hugo.RootMarkers); got != root {
			t.Errorf("DetectRoot(%s) = %q, want %q", label, got, root)
		}
		if p, r := detectProfile(content, ""); p.Name != ProfileHugo || r != root {
			t.Errorf("detectProfile(%s) = (%q, %q), want (hugo, %q)", label, p.Name, r, root)
		}
	}
	assertMarkdown := func(t *testing.T, label, content string) {
		t.Helper()
		if got := DetectRoot(content, hugo.RootMarkers); got != "" {
			t.Errorf("DetectRoot(%s) = %q, want \"\"", label, got)
		}
		if p, r := detectProfile(content, ""); p.Name != ProfileMarkdown || r != "" {
			t.Errorf("detectProfile(%s) = (%q, %q), want (markdown, \"\")", label, p.Name, r)
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
	hugo := mustProfile(ProfileHugo)

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
				if c.HugoRoot != "" {
					t.Errorf("markdown profile must not set HugoRoot, got %q", c.HugoRoot)
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
				if c.HugoRoot != hugoRoot {
					t.Errorf("HugoRoot = %q, want %q", c.HugoRoot, hugoRoot)
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
				if c.HugoRoot != "" {
					t.Errorf("HugoRoot = %q, want \"\"", c.HugoRoot)
				}
			},
		},
		{
			name:    "unknown profile errors and names the valid ones",
			cfg:     Config{Profile: "bogus", ContentDir: plainContent},
			wantErr: "valid profiles: hugo, markdown",
		},
		{
			name:     "auto-detects hugo when layouts/ exists above content_dir",
			cfg:      Config{ContentDir: hugoContent},
			wantName: ProfileHugo,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if c.HugoRoot != hugoRoot {
					t.Errorf("HugoRoot = %q, want %q", c.HugoRoot, hugoRoot)
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
				if c.HugoRoot != tomlRoot {
					t.Errorf("HugoRoot = %q, want %q", c.HugoRoot, tomlRoot)
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
				if c.HugoRoot != "" {
					t.Errorf("HugoRoot = %q, want \"\"", c.HugoRoot)
				}
			},
		},
		{
			name:     "explicit hugo_root selects hugo without a layouts marker",
			cfg:      Config{ContentDir: plainContent, HugoRoot: "/some/site"},
			wantName: ProfileHugo,
			wantAuto: true,
			check: func(t *testing.T, c *Config) {
				if c.HugoRoot != "/some/site" {
					t.Errorf("HugoRoot = %q, want the user's value", c.HugoRoot)
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
	if p, r, ok := detectNearest(content, candidates); ok || p.Name != "" || r != "" {
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
	if p, r, ok := detectNearest(content, candidates); ok || p.Name != "" || r != "" {
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
	if p, r, ok := detectNearest(content, candidates); !ok || p.Name != "layouts-tool" || r != root {
		t.Errorf("detectNearest(far only) = (%q, %q, %v), want (layouts-tool, %q, true)", p.Name, r, ok, root)
	}

	if err := os.WriteFile(filepath.Join(site, "docs.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Nearer marker of the later candidate beats the farther earlier one.
	if p, r, ok := detectNearest(content, candidates); !ok || p.Name != "docsjson-tool" || r != site {
		t.Errorf("detectNearest(nearest) = (%q, %q, %v), want (docsjson-tool, %q, true)", p.Name, r, ok, site)
	}

	// Same level: candidate order breaks the tie.
	if err := os.Mkdir(filepath.Join(site, "layouts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, r, ok := detectNearest(content, candidates); !ok || p.Name != "layouts-tool" || r != site {
		t.Errorf("detectNearest(tie) = (%q, %q, %v), want (layouts-tool, %q, true)", p.Name, r, ok, site)
	}

	// The returned profile is a copy, not the caller's slice element.
	p, _, _ := detectNearest(content, candidates)
	p.RootMarkers[0] = "mutated"
	if candidates[1].RootMarkers[0] != "layouts/" {
		t.Error("detectNearest must return a copy of the winning profile")
	}
}

// TestDetectProfile_NearestMarkerWins checks the registry-driven path through
// detectProfile: a nearby hugo marker is found even when the walk starts deep
// in the tree, and an explicit hugo_root short-circuits detection.
func TestDetectProfile_NearestMarkerWins(t *testing.T) {
	root, content := hugoTree(t)
	deep := filepath.Join(content, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if p, r := detectProfile(deep, ""); p.Name != ProfileHugo || r != root {
		t.Errorf("detectProfile(deep) = (%q, %q), want (hugo, %q)", p.Name, r, root)
	}
	if p, r := detectProfile(plainTree(t), ""); p.Name != ProfileMarkdown || r != "" {
		t.Errorf("detectProfile(plain) = (%q, %q), want (markdown, \"\")", p.Name, r)
	}
	if p, r := detectProfile("", ""); p.Name != ProfileMarkdown || r != "" {
		t.Errorf("detectProfile(empty) = (%q, %q), want (markdown, \"\")", p.Name, r)
	}
	if p, r := detectProfile(plainTree(t), "/explicit"); p.Name != ProfileHugo || r != "/explicit" {
		t.Errorf("detectProfile(hugo_root) = (%q, %q), want (hugo, /explicit)", p.Name, r)
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
		if got := DetectRoot(content, hugo.RootMarkers); got != "" {
			t.Errorf("DetectRoot = %q, want \"\" (ancestor themes/ is outside the repo)", got)
		}
		if p, root := detectProfile(content, ""); p.Name != ProfileMarkdown || root != "" {
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
		if p, root := detectProfile(content, ""); p.Name != ProfileMarkdown || root != "" {
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
		if got := DetectRoot(content, hugo.RootMarkers); got != site {
			t.Errorf("DetectRoot = %q, want the parent site %q", got, site)
		}
		if p, root := detectProfile(content, ""); p.Name != ProfileHugo || root != site {
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
		if cfg.HugoRoot != site {
			t.Errorf("HugoRoot = %q, want %q", cfg.HugoRoot, site)
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
		if got := DetectRoot(content, hugo.RootMarkers); got != site {
			t.Errorf("DetectRoot = %q, want the parent site %q", got, site)
		}
		cfg := &Config{ContentDir: content}
		if err := cfg.ApplyProfile(); err != nil {
			t.Fatalf("ApplyProfile: %v", err)
		}
		if cfg.ResolvedProfile.Name != ProfileHugo || !cfg.ProfileAuto || cfg.HugoRoot != site {
			t.Errorf("ApplyProfile = %q (auto=%v, root=%q), want hugo auto-detected at %q",
				cfg.ResolvedProfile.Name, cfg.ProfileAuto, cfg.HugoRoot, site)
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
		if got := DetectRoot(content, hugo.RootMarkers); got != "" {
			t.Errorf("DetectRoot = %q, want \"\" (the walk must stop at the worktree root)", got)
		}
		cfg := &Config{ContentDir: content}
		if err := cfg.ApplyProfile(); err != nil {
			t.Fatalf("ApplyProfile: %v", err)
		}
		if cfg.ResolvedProfile.Name != ProfileMarkdown || cfg.HugoRoot != "" {
			t.Errorf("ApplyProfile = %q (root=%q), want markdown with no root",
				cfg.ResolvedProfile.Name, cfg.HugoRoot)
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
		if got := DetectRoot(content, hugo.RootMarkers); got != site {
			t.Errorf("DetectRoot = %q, want %q", got, site)
		}
		if p, root := detectProfile(content, ""); p.Name != ProfileHugo || root != site {
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
		if got := DetectRoot(content, hugo.RootMarkers); got != tmp {
			t.Errorf("DetectRoot = %q, want the repo root %q", got, tmp)
		}
		if p, root := detectProfile(content, ""); p.Name != ProfileHugo || root != tmp {
			t.Errorf("detectProfile = (%q, %q), want (hugo, %q)", p.Name, root, tmp)
		}
	})

	t.Run("no dot-git anywhere still walks up", func(t *testing.T) {
		tmp := t.TempDir()
		content := filepath.Join(tmp, "themes", "proj", "docs")
		mkdirs(t, content)
		if got := DetectRoot(content, hugo.RootMarkers); got != tmp {
			t.Errorf("DetectRoot = %q, want %q (unbounded walk without a repo)", got, tmp)
		}
		if p, root := detectProfile(content, ""); p.Name != ProfileHugo || root != tmp {
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
		if !isRepoRoot(dir) {
			t.Error("isRepoRoot = false, want true for a .git directory")
		}
	})

	t.Run("no dot-git does not stop", func(t *testing.T) {
		if isRepoRoot(t.TempDir()) {
			t.Error("isRepoRoot = true, want false with no .git entry")
		}
	})

	t.Run("worktree file stops", func(t *testing.T) {
		if !isRepoRoot(writeGit(t, "gitdir: /x/.git/worktrees/w\n")) {
			t.Error("isRepoRoot = false, want true for a linked worktree")
		}
	})

	t.Run("submodule file does not stop", func(t *testing.T) {
		if isRepoRoot(writeGit(t, "gitdir: /x/.git/modules/content\n")) {
			t.Error("isRepoRoot = true, want false for a submodule checkout")
		}
	})

	t.Run("garbage file stops", func(t *testing.T) {
		if !isRepoRoot(writeGit(t, "\x00 not a pointer\n")) {
			t.Error("isRepoRoot = false, want true for an unparsable .git file")
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
		if !isRepoRoot(dir) {
			t.Error("isRepoRoot = false, want true for a symlinked .git directory")
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

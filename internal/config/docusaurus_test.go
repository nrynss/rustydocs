package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDocusaurusSelection(t *testing.T) {
	for _, marker := range []string{"docusaurus.config.js", "docusaurus.config.ts"} {
		t.Run(marker, func(t *testing.T) {
			root := t.TempDir()
			content := filepath.Join(root, "website", "docs", "guide")
			if err := os.MkdirAll(content, 0o755); err != nil {
				t.Fatal(err)
			}
			// Detection uses the marker's name and kind, without executing JS/TS.
			if err := os.WriteFile(filepath.Join(root, marker), []byte("export default {};\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, selection := range []string{"", " Docusaurus "} {
				cfg := Config{ContentDir: content, Profile: selection}
				if err := cfg.ApplyProfile(); err != nil {
					t.Fatal(err)
				}
				p := cfg.ResolvedProfile
				if p.Name != ProfileDocusaurus || cfg.ProjectRoot != root || cfg.ProfileAuto != (selection == "") {
					t.Fatalf("selection %q: profile=%q root=%q auto=%v", selection, p.Name, cfg.ProjectRoot, cfg.ProfileAuto)
				}
				if !reflect.DeepEqual(cfg.ContentExtensions, []string{".md", ".mdx"}) || p.Resolver != ResolverPath || !p.ImportMap ||
					!reflect.DeepEqual(cfg.Reusables.Patterns, []string{MDXComponentPattern}) ||
					!reflect.DeepEqual(cfg.Reusables.Extensions, []string{".mdx", ".md"}) {
					t.Fatalf("unexpected defaults: %+v", cfg)
				}
				want := ParserCapabilities{SkipFencedCaptures: true, PathCapturesOnly: true, PathBaseMode: PathBasePageOnly}
				if !reflect.DeepEqual(p.ParserCapabilities, want) || p.DetectRoot(content, nil) != root {
					t.Fatalf("capabilities/root detection: %+v", p)
				}
			}
		})
	}
}

func TestDocusaurusRootPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, selection, wantProfile, wantRoot string
		files                                  map[string]string
		dirs                                   []string
	}{
		{"nearest Docusaurus beats parent Hugo", "", ProfileDocusaurus, "site", map[string]string{"hugo.toml": "", "site/docusaurus.config.js": ""}, nil},
		{"nearest Mintlify beats parent Docusaurus", "", ProfileMintlify, "site", map[string]string{"docusaurus.config.ts": "", "site/docs.json": `{"navigation":[]}`}, nil},
		{"nearest Docusaurus marker wins", "", ProfileDocusaurus, "site", map[string]string{"docusaurus.config.js": "", "site/docusaurus.config.ts": ""}, nil},
		{"same directory preserves Hugo precedence", "", ProfileHugo, "site", map[string]string{"site/hugo.toml": "", "site/docusaurus.config.js": ""}, nil},
		{"same directory preserves Starlight precedence", "", ProfileStarlight, "site", map[string]string{"site/astro.config.mjs": "integrations: [starlight()]", "site/docusaurus.config.ts": ""}, nil},
		{"explicit selection overrides tie", ProfileDocusaurus, ProfileDocusaurus, "site", map[string]string{"site/hugo.toml": "", "site/docusaurus.config.js": ""}, nil},
		{"directory is not a config file", "", ProfileMarkdown, "", nil, []string{"site/docusaurus.config.js", "site/docusaurus.config.ts"}},
		{"repository boundary stops walk", "", ProfileMarkdown, "", map[string]string{"docusaurus.config.js": ""}, []string{"site/.git"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			content := filepath.Join(root, "site", "docs", "nested")
			for _, dir := range append(tc.dirs, "site/docs/nested") {
				if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for rel, body := range tc.files {
				if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg := Config{ContentDir: content, Profile: tc.selection}
			if err := cfg.ApplyProfile(); err != nil {
				t.Fatal(err)
			}
			wantRoot := ""
			if tc.wantRoot != "" {
				wantRoot = filepath.Join(root, tc.wantRoot)
			}
			if cfg.ResolvedProfile.Name != tc.wantProfile || cfg.ProjectRoot != wantRoot {
				t.Fatalf("profile=%q root=%q; want %q %q", cfg.ResolvedProfile.Name, cfg.ProjectRoot, tc.wantProfile, wantRoot)
			}
		})
	}
}

func TestDocusaurusExplicitWithoutMarkerAndOverrides(t *testing.T) {
	root := t.TempDir()
	cfg := Config{ContentDir: root, Profile: ProfileDocusaurus, ProjectRoot: root,
		ContentExtensions: []string{".mdx"}, Reusables: ReusablesConfig{Patterns: []string{"(custom)"}, Extensions: []string{".md"}}}
	if err := cfg.ApplyProfile(); err != nil {
		t.Fatal(err)
	}
	if cfg.ResolvedProfile.Name != ProfileDocusaurus || cfg.ProjectRoot != root ||
		!reflect.DeepEqual(cfg.ContentExtensions, []string{".mdx"}) ||
		!reflect.DeepEqual(cfg.Reusables.Patterns, []string{"(custom)"}) || !reflect.DeepEqual(cfg.Reusables.Extensions, []string{".md"}) {
		t.Fatalf("explicit settings changed: %+v", cfg)
	}
	// A root alone must not select a tool profile.
	auto := Config{ContentDir: root, ProjectRoot: root}
	if err := auto.ApplyProfile(); err != nil {
		t.Fatal(err)
	}
	if auto.ResolvedProfile.Name != ProfileMarkdown {
		t.Fatalf("root-only profile = %q", auto.ResolvedProfile.Name)
	}
	noRoot := Config{ContentDir: root, Profile: ProfileDocusaurus}
	if err := noRoot.ApplyProfile(); err != nil {
		t.Fatal(err)
	}
	if noRoot.ProjectRoot != "" || noRoot.ResolvedProfile.Name != ProfileDocusaurus {
		t.Fatalf("markerless explicit profile = %+v", noRoot)
	}
}

func TestDocusaurusExplicitRootWinsDetectedRoot(t *testing.T) {
	root := t.TempDir()
	content := filepath.Join(root, "docs")
	explicit := filepath.Join(root, "site-root")
	for _, dir := range []string{content, explicit} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "docusaurus.config.js"), []byte("export default {};"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, selection := range []string{"", ProfileDocusaurus} {
		cfg := Config{ContentDir: content, ProjectRoot: explicit, Profile: selection}
		if err := cfg.ApplyProfile(); err != nil {
			t.Fatal(err)
		}
		if cfg.ResolvedProfile.Name != ProfileDocusaurus || cfg.ProjectRoot != explicit {
			t.Fatalf("selection %q: profile=%q root=%q; want docusaurus %q", selection, cfg.ResolvedProfile.Name, cfg.ProjectRoot, explicit)
		}
	}
}

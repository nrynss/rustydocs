package analyzer

import (
	"path/filepath"
	"testing"

	"github.com/nrynss/rustydocs/internal/config"
)

func TestIsContentFile(t *testing.T) {
	exts := contentExtensionSet([]string{".md", ".mdx"})
	cases := map[string]bool{
		"docs/a.md":  true,
		"docs/a.MD":  true, // case-insensitive
		"docs/a.mdx": true,
		"main.go":    false,
		"img/x.png":  false,
		".gitignore": false,
		"notes.rst":  false,
	}
	for p, want := range cases {
		if got := isContentFile(filepath.FromSlash(p), exts); got != want {
			t.Errorf("isContentFile(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestContentExtensionSet_NormalizationAndDefaults(t *testing.T) {
	def := contentExtensionSet(nil)
	for _, e := range []string{".md", ".markdown", ".mdx"} {
		if _, ok := def[e]; !ok {
			t.Errorf("default set missing %q", e)
		}
	}

	// Bare and mixed-case extensions are normalized to ".ext" lowercase.
	got := contentExtensionSet([]string{"RST", " .adoc "})
	for _, e := range []string{".rst", ".adoc"} {
		if _, ok := got[e]; !ok {
			t.Errorf("normalized set missing %q", e)
		}
	}
}

func TestShouldExclude(t *testing.T) {
	const base = "base"
	cfg := &config.Config{
		ExcludeDirs:     []string{"images", "node_modules"},
		ExcludePatterns: []string{"releasenotes/*", "docs", "*.tmp"},
	}
	rel := func(p string) string { return filepath.Join(base, filepath.FromSlash(p)) }

	excluded := []string{
		"images/logo.md",        // excluded dir, top level
		"a/images/logo.md",      // excluded dir, nested
		"node_modules/x.md",     // excluded dir
		"releasenotes/r1.md",    // pattern "dir/*"
		"releasenotes/sub/r.md", // nested under the pattern dir
		"docs/intro.md",         // bare pattern, segment prefix
		"scratch.tmp",           // "*.tmp" basename glob
		"a/scratch.tmp",         // "*.tmp" nested basename glob
	}
	for _, p := range excluded {
		if !shouldExclude(rel(p), cfg, base) {
			t.Errorf("expected %q to be excluded", p)
		}
	}

	kept := []string{
		"guide/intro.md",  // unrelated
		"mydocs/intro.md", // must NOT match bare "docs" (the #3 over-match)
		"imagesx/a.md",    // must NOT match excluded dir "images"
		"a.tmpl",          // must NOT match "*.tmp"
	}
	for _, p := range kept {
		if shouldExclude(rel(p), cfg, base) {
			t.Errorf("expected %q NOT to be excluded", p)
		}
	}
}

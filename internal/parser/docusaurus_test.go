package parser

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/testutil"
)

func TestDocusaurusImportResolution(t *testing.T) {
	repo := testutil.NewRepo(t)
	when := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	repo.Commit(when, "partials", map[string]string{
		"website/docusaurus.config.ts":        "export default {};\n",
		"website/docs/guide/_foo.mdx":         "partial body\n",
		"website/docs/_shared.md":             "shared body\n",
		"website/docs/guide/_entry/index.mdx": "directory partial\n",
		"website/docs/guide/Builtin.mdx":      "a same-named file is not an import\n",
		// Decoys must never satisfy a missing relative import or an alias.
		"website/snippets/_missing.mdx":            "wrong base\n",
		"website/docs/guide/@site/docs/_shared.md": "not an alias resolution\n",
		"outside.mdx": "outside the selected site root\n",
	})
	repo.Write("website/docs/guide/_new.mdx", "uncommitted partial\n")
	p, ok := config.LookupProfile(config.ProfileDocusaurus)
	if !ok {
		t.Fatal("missing profile")
	}
	rp, err := NewReusablePatternsFor(ReusableConfig{
		Patterns: p.ReusablePatterns, Extensions: p.ReusableExtensions, Root: repo.Path("website"),
		Resolver: p.Resolver, ImportMap: p.ImportMap, Capabilities: p.ParserCapabilities,
	})
	if err != nil {
		t.Fatal(err)
	}
	page := repo.Write("website/docs/guide/page.mdx", `import Foo from './_foo.mdx';
import { Part as Renamed } from '../_shared.md';
import Root from '/docs/_shared.md';
import Entry from './_entry';
import Gone from './_missing.mdx';
import New from './_new.mdx';
import Escape from '../../../outside.mdx';
import WrongCase from './_Foo.mdx';
import Site from '@site/docs/_shared.md';
import Theme from '@theme/Tabs';
import Alias from '@/docs/_shared.md';
import Package from 'some-package/readme.md';
import Code from '../../src/components/Card.tsx';
import Loader from '!!raw-loader!./_foo.mdx';

# Guide

<Foo /><Renamed /><Root /><Entry /><Gone /><New /><Escape /><WrongCase />
<Site /><Theme /><Alias /><Package /><Code /><Loader /><Builtin />
`)
	for _, tc := range []struct {
		symbol, path string
		status       Resolution
	}{
		{"Foo", "website/docs/guide/_foo.mdx", ResolutionResolved},
		{"Renamed", "website/docs/_shared.md", ResolutionResolved},
		{"Root", "website/docs/_shared.md", ResolutionResolved},
		{"Entry", "website/docs/guide/_entry/index.mdx", ResolutionResolved},
		{"Gone", "", ResolutionUnresolved},
		{"New", "", ResolutionUnresolved},
		{"Escape", "", ResolutionUnresolved},
		{"WrongCase", "", ResolutionUnresolved},
		{"Site", "", ResolutionSkipped},
		{"Theme", "", ResolutionSkipped},
		{"Alias", "", ResolutionSkipped},
		{"Package", "", ResolutionSkipped},
		{"Code", "", ResolutionSkipped},
		{"Loader", "", ResolutionSkipped},
		{"Builtin", "", ResolutionSkipped},
	} {
		t.Run(tc.symbol, func(t *testing.T) {
			info, status := ResolveReusable(tc.symbol, page, rp)
			if status != tc.status {
				t.Fatalf("status = %v, want %v", status, tc.status)
			}
			if tc.status == ResolutionResolved {
				if info == nil || info.Path != repo.Path(tc.path) || !info.LastModified.Equal(when) {
					t.Fatalf("resolved info = %+v", info)
				}
			} else if info != nil {
				t.Fatalf("unexpected history: %+v", info)
			}
		})
	}
	// An existing historyless dependency retains its file identity and hash.
	d := ResolveDependency(Dependency{Reference: "New"}, page, rp)
	if d.Status != "unresolved" || len(d.Files) != 1 || d.Files[0].Info != nil ||
		d.Files[0].Path != repo.Path("website/docs/guide/_new.mdx") || d.Files[0].Fingerprint == "" {
		t.Fatalf("historyless evidence = %+v", d)
	}
	if got := rp.DisplayName("New", page, nil); got != filepath.ToSlash("docs/guide/_new.mdx") {
		t.Fatalf("historyless display name = %q", got)
	}
}

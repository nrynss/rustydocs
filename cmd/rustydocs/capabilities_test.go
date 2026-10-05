package main

import (
	"strings"
	"testing"

	"github.com/nrynss/rustydocs/internal/config"
)

func TestDescribeUnresolvedReusables_IncludeExampleMetadata(t *testing.T) {
	for _, tc := range []struct{ profile, example string }{
		{config.ProfileMintlify, `<Snippet file="aws-config.mdx" />`},
		{config.ProfileGitBook, `{% include "./shared.md" %}`},
		{config.ProfileStarlight, `{% partial file="./_footer.mdoc" /%}`},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			p, ok := config.LookupProfile(tc.profile)
			if !ok {
				t.Fatal("missing built-in profile")
			}
			p.Name = "renamed-profile"
			cfg := &config.Config{ContentDir: "docs", ResolvedProfile: p}
			got := describeUnresolvedReusables(cfg, 1, []string{"missing.md"})
			if !strings.Contains(got, "(e.g. "+tc.example+")") {
				t.Errorf("renamed diagnostic = %q, want example %q", got, tc.example)
			}
			// A registered identity must not override explicitly supplied display metadata.
			p.Name, p.IncludeExample = tc.profile, `include "custom.txt"`
			cfg.ResolvedProfile = p
			got = describeUnresolvedReusables(cfg, 1, nil)
			if !strings.Contains(got, `(e.g. include "custom.txt")`) {
				t.Errorf("custom diagnostic = %q", got)
			}
		})
	}
	// Preserve the historical default for custom profiles with no example.
	cfg := &config.Config{ContentDir: "docs", ResolvedProfile: config.Profile{
		Name: "custom", Resolver: config.ResolverPath, RootMarkers: []string{"custom.json"},
	}}
	if got := describeUnresolvedReusables(cfg, 1, nil); !strings.Contains(got, `(e.g. <Snippet file="aws-config.mdx" />)`) {
		t.Errorf("default diagnostic = %q", got)
	}
}

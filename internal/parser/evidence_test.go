package parser

import (
	"github.com/nrynss/rustydocs/internal/config"
	"reflect"
	"testing"
)

// TestChunkIdentityAndImports keeps rendered content while excluding import boilerplate from identity and hashes.
func TestChunkIdentityAndImports(t *testing.T) {
	profile, _ := config.LookupProfile(config.ProfileMintlify)
	rp, err := NewReusablePatternsFor(ReusableConfig{Patterns: profile.ReusablePatterns, ImportMap: true})
	if err != nil {
		t.Fatal(err)
	}
	original := "import {\n A, B as C\n} from './shared.mdx';\nimport './style.css';\n\nRendered preamble <C />\n\n# Heading\ntext\n```md\n# fake\n```\n"
	a := ParseChunks(original, nil, false, rp)
	b := ParseChunks("\n"+original, nil, false, rp)
	if len(a) != 2 || a[0].Title != "(preamble)" || a[1].Title != "Heading" {
		t.Fatalf("chunks: %+v", a)
	}
	for i := range a {
		if a[i].LogicalKey != b[i].LogicalKey || a[i].Fingerprint != b[i].Fingerprint {
			t.Fatalf("unstable content identity: %+v %+v", a[i], b[i])
		}
	}
	if got := ParseChunks("import A from './a.mdx';\n", nil, false, rp); len(got) != 0 {
		t.Fatalf("import-only file: %+v", got)
	}
	if got := ParseChunks("import A from './a.mdx'; <A />\n# Heading\n", nil, false, rp); len(got) != 2 {
		t.Fatalf("rendered same-line preamble lost: %+v", got)
	}
	if !reflect.DeepEqual(a[1].HeadingPath, []string{"Heading"}) {
		t.Fatal(a[1].HeadingPath)
	}
}

// TestFencedHeadingsAllProfiles prevents example code from introducing headings under every profile.
func TestFencedHeadingsAllProfiles(t *testing.T) {
	for _, content := range []string{"# Real\n```md\n# fake\n```\n", "# Real\n~~~~\n# fake\n~~~~\n", "# Real\n```\n# fake\n"} {
		chunks := ParseSections(content, nil, nil)
		if len(chunks) != 1 || chunks[0].Title != "Real" {
			t.Fatalf("chunks: %+v", chunks)
		}
	}
	if Fingerprint("a\r\n\r\n") != Fingerprint("a\n") || Fingerprint("a b") == Fingerprint("ab") {
		t.Fatal("fingerprint normalization")
	}
}

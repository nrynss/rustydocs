package analyzer

import (
	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/testutil"
	"testing"
	"time"
)

// TestCompleteSectionEvidence retains fresh, stale and unknown sections with reusable provenance.
func TestCompleteSectionEvidence(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)
	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -200), "pages", map[string]string{
		"docs.json":           "{}",
		"docs/page.mdx":       "import Shared from '../snippets/shared.mdx';\nimport Button from 'pkg';\n\n# First\nOld prose\n<Shared />\n<Snippet file=\"/snippets/shared.mdx\" />\n<Button />\n<Snippet file=\"missing.mdx\" />\n\n## Duplicate\nold\n\n## Duplicate\nold again\n```md\n# example\n```\n",
		"snippets/shared.mdx": "Shared text\n",
	})
	fresh := now.AddDate(0, 0, -10)
	repo.Commit(fresh, "snippet", map[string]string{"snippets/shared.mdx": "New shared text\n"})
	repo.Write("docs/unknown.mdx", "# Unknown\nNo history\n")
	cfg := config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")
	cfg.Profile = config.ProfileMintlify
	cfg.ProjectRoot = repo.Dir
	res, err := Analyze(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("files: %d", len(res.Files))
	}
	f := res.Files[0]
	if len(f.Sections) != 3 || len(f.StaleSections) != 2 {
		t.Fatalf("sections=%d stale=%d", len(f.Sections), len(f.StaleSections))
	}
	first := f.Sections[0]
	if first.Title != "First" || first.LastUpdated() == nil || !first.LastUpdated().Equal(now.AddDate(0, 0, -200)) || first.DisplayDate() == nil || !first.DisplayDate().Equal(fresh) {
		t.Fatalf("own/effective: %+v", first)
	}
	if len(first.Dependencies) != 6 {
		t.Fatalf("references: %+v", first.Dependencies)
	}
	if first.Dependencies[0].Status != "resolved" || first.Dependencies[0].Line != 6 || len(first.Dependencies[0].Files) != 1 || first.Dependencies[3].Status != "skipped" || first.Dependencies[5].Status != "unresolved" {
		t.Fatalf("evidence: %+v", first.Dependencies)
	}
	if f.Sections[1].LogicalKey == f.Sections[2].LogicalKey {
		t.Fatal("duplicate identities collide")
	}
	if !res.Files[1].HistoryMissing || res.Files[1].Sections[0].DisplayDate() != nil {
		t.Fatal("missing history looks fresh")
	}
	if !res.GeneratedAt.Equal(now) {
		t.Fatal("analysis timestamp changed")
	}
}

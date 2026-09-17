package parser

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDisplayName_KeepsAuthoredSpelling pins the property the Windows CI run
// exposed: the label must be built from the spelling the reference used, not
// from filepath.EvalSymlinks' idea of the file's real name. Windows
// case-normalises there; everywhere else a symlink produces the same class of
// rewrite, which is what this reproduces locally.
func TestDisplayName_KeepsAuthoredSpelling(t *testing.T) {
	cr := newCaseRepo(t)
	link := filepath.Join(cr.repo.Dir, "snippets", "Alias.mdx")
	if err := os.Symlink(filepath.Join(cr.repo.Dir, "snippets", "note.mdx"), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	page := cr.repo.Write("guides/spell.mdx", "import Alias from \"/snippets/Alias.mdx\";\n")

	if got := cr.rp.DisplayName("Alias", page, nil); got != "snippets/Alias.mdx" {
		t.Errorf("DisplayName = %q, want snippets/Alias.mdx: the label must keep the "+
			"spelling the import used, not the name the filesystem resolves it to", got)
	}
}

package parser

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/testutil"
)

// filesystemIsCaseInsensitive reports whether dir lives on a filesystem that
// answers os.Stat for a spelling the file does not have. It is what decides
// whether the case tests below are exercising the fix or passing for free, so
// every one of them logs the regime it ran in rather than skipping: the point
// of the fix is that the assertions hold either way.
func filesystemIsCaseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "rustydocs-case-probe.tmp")
	if err := os.WriteFile(probe, []byte("x"), 0o600); err != nil {
		t.Fatalf("case probe: %v", err)
	}
	defer func() { _ = os.Remove(probe) }()
	_, err := os.Stat(filepath.Join(dir, "RUSTYDOCS-CASE-PROBE.TMP"))
	return err == nil
}

// caseRepo is a Mintlify-shaped fixture whose every snippet is lower case, so
// a capitalised capture can only match it by case-folding.
type caseRepo struct {
	repo *testutil.Repo
	rp   *ReusablePatterns
	when time.Time
	page string
}

func newCaseRepo(t *testing.T) *caseRepo {
	t.Helper()
	repo := testutil.NewRepo(t)
	when := time.Date(2024, 5, 4, 9, 0, 0, 0, time.UTC)
	repo.Commit(when, "snippets", map[string]string{
		"docs.json":           `{"name":"x","navigation":[]}`,
		"snippets/note.mdx":   "note body\n",
		"guides/overview.mdx": "# Overview\n",
	})
	return &caseRepo{
		repo: repo,
		rp:   newPathRP(t, repo.Dir),
		when: when,
		page: filepath.Join(repo.Dir, "guides", "overview.mdx"),
	}
}

// TestResolveDirectPath_CaseExact is the regression guard for the Windows-only
// CI failure behind this change: resolution must give the same answer on a
// case-sensitive and a case-insensitive filesystem alike.
//
// Before the fix the three platforms disagreed about <Note /> on a page with no
// import of it. Linux rejected the candidate outright; macOS accepted it for
// resolution and labelling but then found no git history, because git is
// case-exact; Windows accepted it all the way through, because
// filepath.EvalSymlinks rewrites every component to its real on-disk name
// there, so git was asked about snippets\note.mdx and answered — folding an
// unrelated snippet's commit date into the section's freshness.
func TestResolveDirectPath_CaseExact(t *testing.T) {
	cr := newCaseRepo(t)
	t.Logf("filesystem is case-insensitive: %v",
		filesystemIsCaseInsensitive(t, cr.repo.Dir))

	tests := []struct {
		name string
		ref  string
		want Resolution
	}{
		{"exact path resolves", "note.mdx", ResolutionResolved},
		{"exact extensionless path resolves", "note", ResolutionResolved},
		{"exact root-absolute path resolves", "/snippets/note.mdx", ResolutionResolved},
		{"file name differing only in case does not", "Note.mdx", ResolutionUnresolved},
		{"extensionless name differing only in case does not", "NOTE", ResolutionSkipped},
		{"directory differing only in case does not", "Snippets/note.mdx", ResolutionUnresolved},
		{"root-absolute with the wrong case does not", "/Snippets/Note.mdx", ResolutionUnresolved},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// resolveDirectPath is asserted separately from ResolveReusable
			// because git hides half the bug: a mis-cased candidate that the
			// resolver wrongly accepts still has no history on macOS and
			// Linux, so the reference comes back unresolved anyway and only
			// Windows visibly breaks. The resolver's own answer is the part
			// that can be checked on a case-insensitive filesystem here.
			target, ok := cr.rp.resolveDirectPath(tc.ref, cr.page)
			if want := tc.want == ResolutionResolved; ok != want {
				t.Errorf("resolveDirectPath(%q) = %q, %v; want ok=%v",
					tc.ref, target, ok, want)
			}

			info, res := ResolveReusable(tc.ref, cr.page, cr.rp)
			if res != tc.want {
				t.Errorf("ResolveReusable(%q) = %v, want %v", tc.ref, res, tc.want)
			}
			if tc.want == ResolutionResolved {
				if info == nil || !info.LastModified.Equal(cr.when) {
					t.Errorf("ResolveReusable(%q) info = %+v, want the snippet's %s",
						tc.ref, info, cr.when)
				}
				return
			}
			if info != nil {
				t.Errorf("ResolveReusable(%q) info = %+v, want nil", tc.ref, info)
			}
		})
	}
}

// TestDisplayName_CaseExact pins the half of the divergence that git's own
// case-exactness used to hide on macOS: resolveDirectPath accepted the
// mis-cased candidate even there, so the reusable was labelled
// "snippets/Note.mdx" — a path no file has — while Linux labelled it with the
// raw capture. The label is what the Markdown, HTML and JSON reports key rows
// on, so the two platforms produced different reports for the same repository
// even when both called the reference unresolved.
func TestDisplayName_CaseExact(t *testing.T) {
	cr := newCaseRepo(t)
	t.Logf("filesystem is case-insensitive: %v",
		filesystemIsCaseInsensitive(t, cr.repo.Dir))

	if got := cr.rp.DisplayName("note.mdx", cr.page, nil); got != "snippets/note.mdx" {
		t.Errorf("DisplayName(note.mdx) = %q, want snippets/note.mdx", got)
	}
	for _, ref := range []string{"Note.mdx", "NOTE.MDX", "Snippets/note.mdx"} {
		if got := cr.rp.DisplayName(ref, cr.page, nil); got != ref {
			t.Errorf("DisplayName(%q) = %q, want the raw capture: a capture that "+
				"resolves to no file must not be labelled with a path", ref, got)
		}
	}
}

// TestImportPath_CaseExact covers the import map, which resolves its module
// specifiers through the same resolveDirectPath. An import of a file that is
// not there is a defect worth reporting, and it must be reported as one
// everywhere rather than quietly resolving to a differently-cased neighbour.
func TestImportPath_CaseExact(t *testing.T) {
	cr := newCaseRepo(t)
	page := cr.repo.Write("guides/imports.mdx",
		"import Good from \"/snippets/note.mdx\";\n"+
			"import Bad from \"/snippets/Note.mdx\";\n")

	info, res := ResolveReusable("Good", page, cr.rp)
	if res != ResolutionResolved || info == nil || !info.LastModified.Equal(cr.when) {
		t.Errorf("exact import: %v %+v, want the snippet resolved", res, info)
	}
	if info, res := ResolveReusable("Bad", page, cr.rp); res != ResolutionUnresolved || info != nil {
		t.Errorf("mis-cased import = %v %+v, want ResolutionUnresolved and nil info", res, info)
	}
	// It is still labelled by the path it named, not by the local symbol.
	if got := cr.rp.DisplayName("Bad", page, nil); got != "snippets/Note.mdx" {
		t.Errorf("DisplayName(Bad) = %q, want snippets/Note.mdx", got)
	}
}

// TestLookupInDir_CaseExact covers the legacy reusables-directory resolver,
// which builds its candidates the same way and hands them straight to git. On
// macOS and Linux git's own case-exactness already made this behave; on Windows
// the path reaches git case-normalised, so the gate is what keeps the three
// platforms saying the same thing.
func TestLookupInDir_CaseExact(t *testing.T) {
	repo := testutil.NewRepo(t)
	when := time.Date(2023, 11, 2, 9, 0, 0, 0, time.UTC)
	repo.Commit(when, "reusables", map[string]string{
		"reusables/warning.md": "careful\n",
	})
	t.Logf("filesystem is case-insensitive: %v",
		filesystemIsCaseInsensitive(t, repo.Dir))

	rp, err := NewReusablePatternsFor(ReusableConfig{
		Patterns:     []string{`\{\{<\s*([a-zA-Z0-9_-]+)\s*>\}\}`},
		Extensions:   []string{".md", ".mdx"},
		ReusablesDir: filepath.Join(repo.Dir, "reusables"),
		Resolver:     config.ResolverNone,
	})
	if err != nil {
		t.Fatal(err)
	}

	if info := GetReusableInfo("warning", "", rp); info == nil || !info.LastModified.Equal(when) {
		t.Errorf("GetReusableInfo(warning) = %+v, want the committed reusable", info)
	}
	for _, ref := range []string{"Warning", "WARNING"} {
		if info := GetReusableInfo(ref, "", rp); info != nil {
			t.Errorf("GetReusableInfo(%q) = %+v, want nil: only the exact name matches", ref, info)
		}
	}
}

// TestCaseExactUnder_UnverifiableDirIsTrusted pins the deliberate escape hatch.
// A directory that exists but cannot be listed is not evidence that a candidate
// is mis-spelled, and treating it as such would lose resolutions that work on
// every platform today, so the check defers to os.Stat there.
func TestCaseExactUnder_UnverifiableDirIsTrusted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any directory, so the mode says nothing")
	}
	base := t.TempDir()
	dir := filepath.Join(base, "closed")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "snippet.mdx")
	if err := os.WriteFile(file, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 0o111: traversable, so os.Stat sees the child, but not listable.
	if err := os.Chmod(dir, 0o111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skipf("%s is still listable after chmod 111", dir)
	}

	rp := &ReusablePatterns{}
	if !rp.caseExactUnder(base, file) {
		t.Error("a directory that cannot be listed must be trusted, not rejected")
	}
	// The directory component itself is still checked, because base is listable.
	if rp.caseExactUnder(base, filepath.Join(base, "Closed", "snippet.mdx")) {
		t.Error("a mis-cased directory below a listable base must still be rejected")
	}
}

// TestCaseExactUnder_OutsideBaseChecksTheFileName documents the one place the
// walk is deliberately partial: a "../x.mdx" capture cleans to a path that is
// not under the base it was tried against, and only its own file name is
// verified.
func TestCaseExactUnder_OutsideBaseChecksTheFileName(t *testing.T) {
	root := t.TempDir()
	pageDir := filepath.Join(root, "guides")
	if err := os.Mkdir(pageDir, 0o750); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "sibling.mdx")
	if err := os.WriteFile(file, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rp := &ReusablePatterns{}
	if !rp.caseExactUnder(pageDir, filepath.Join(pageDir, "..", "sibling.mdx")) {
		t.Error("../sibling.mdx must resolve: the name matches the file")
	}
	if rp.caseExactUnder(pageDir, filepath.Join(pageDir, "..", "Sibling.mdx")) {
		t.Error("../Sibling.mdx must not resolve: the name does not match the file")
	}
}

// TestResolveDirectPath_CaseExactAcrossDotDot covers the escape hatch in
// caseExactUnder: a page-relative capture that climbs out of the page's own
// directory. Verifying only the file name there would let a mis-cased
// *directory* through on a case-insensitive filesystem, which is the same
// defect the gate exists to close, so the check runs from the project root.
//
// This asserts resolveDirectPath rather than ResolveReusable on purpose: git
// pathspecs are case-exact, so a mis-cased path that the resolver wrongly
// accepts still finds no history on macOS and Linux and the defect stays
// invisible one layer up. Windows normalises the case before git sees it, and
// then it is not invisible at all.
func TestResolveDirectPath_CaseExactAcrossDotDot(t *testing.T) {
	cr := newCaseRepo(t)
	t.Logf("filesystem is case-insensitive: %v",
		filesystemIsCaseInsensitive(t, cr.repo.Dir))

	// The page lives in guides/, so "../snippets/note.mdx" names the real file.
	if target, ok := cr.rp.resolveDirectPath("../snippets/note.mdx", cr.page); !ok {
		t.Errorf("exact ../ capture did not resolve (got %q)", target)
	}
	// Only the directory's case differs, so it must not resolve.
	for _, ref := range []string{"../Snippets/note.mdx", "../SNIPPETS/note.mdx"} {
		if target, ok := cr.rp.resolveDirectPath(ref, cr.page); ok {
			t.Errorf("resolveDirectPath(%q) = %q, true; want no match: a mis-cased "+
				"directory after \"..\" must not resolve to a differently-cased one",
				ref, target)
		}
	}
}

package parser

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/git"
	"github.com/nrynss/rustydocs/internal/testutil"
)

// TestSupportingScopeRejectsPoisonedCache verifies that cached candidates do
// not bypass the same authorization required of newly discovered supports.
func TestSupportingScopeRejectsPoisonedCache(t *testing.T) {
	root := testutil.NewRepo(t)
	external := testutil.NewRepo(t)
	external.Commit(time.Now(), "outside", map[string]string{"secret.html": "secret"})
	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".html"}, "", root.Dir)
	if err != nil {
		t.Fatal(err)
	}
	rp.shortcodeCache["danger"] = []string{external.Path("secret.html")}
	rp.cache = git.NewFileInfoCache()
	for i := 0; i < 2; i++ {
		d := ResolveDependency(Dependency{Reference: "danger", Line: 2}, "", rp)
		if len(d.Files) != 0 || len(d.Issues) == 0 || d.Issues[0].Code != "reusable_support_rejected" || d.Status != "unresolved" {
			t.Fatalf("cache authorized foreign history/content: %+v", d)
		}
	}
}

// TestExplicitLegacyRootIsScoped permits an explicitly selected directory while
// rejecting its traversal and symlink escapes, independently of the Hugo root.
func TestExplicitLegacyRootIsScoped(t *testing.T) {
	site := testutil.NewRepo(t)
	legacy := testutil.NewRepo(t)
	outside := testutil.NewRepo(t)
	legacy.Commit(time.Now(), "legacy", map[string]string{"allowed.md": "allowed content"})
	outside.Commit(time.Now(), "outside", map[string]string{"secret.md": "outside secret"})
	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".md"}, legacy.Dir, site.Dir)
	if err != nil {
		t.Fatal(err)
	}
	d := ResolveDependency(Dependency{Reference: "allowed", Line: 2}, "", rp)
	if d.Status != "resolved" || len(d.Files) != 1 || d.Files[0].Fingerprint != Fingerprint("allowed content") {
		t.Fatal(d)
	}
	if err := os.Symlink(outside.Path("secret.md"), legacy.Path("escape.md")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	d = ResolveDependency(Dependency{Reference: "escape", Line: 2}, "", rp)
	if len(d.Files) != 0 || len(d.Issues) == 0 {
		t.Fatalf("legacy symlink escape: %+v", d)
	}
	// The configured root itself may be symlinked: explicit selection grants
	// authority to its physical directory, not merely its lexical parent.
	alias := filepath.Join(t.TempDir(), "selected")
	if err := os.Symlink(legacy.Dir, alias); err != nil {
		t.Fatal(err)
	}
	rp, err = NewReusablePatterns(defaultPatternStrings, []string{".md"}, alias, site.Dir)
	if err != nil {
		t.Fatal(err)
	}
	d = ResolveDependency(Dependency{Reference: "allowed", Line: 2}, "", rp)
	if len(d.Files) != 1 || d.Files[0].Path != legacy.Path("allowed.md") {
		t.Fatalf("selected symlink root: %+v", d)
	}
}

// TestSelectedHugoRootSymlink authorizes the physical directory selected by the
// caller without granting authority to other neighboring directories.
func TestSelectedHugoRootSymlink(t *testing.T) {
	site := testutil.NewRepo(t)
	site.Commit(time.Now(), "site", map[string]string{
		"layouts/shortcodes/safe.html": `{{ readFile "value.txt" }}`,
		"value.txt":                    "allowed support",
	})
	alias := filepath.Join(t.TempDir(), "selected")
	if err := os.Symlink(site.Dir, alias); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	rp, err := NewReusablePatterns(defaultPatternStrings, []string{".html"}, "", alias)
	if err != nil {
		t.Fatal(err)
	}
	d := ResolveDependency(Dependency{Reference: "safe", Line: 2}, "", rp)
	if d.Status != "resolved" || len(d.Files) != 2 || len(d.Issues) != 0 {
		t.Fatalf("selected Hugo root lost authorized support: %+v", d)
	}
}

func TestNoteSupportIssueRedactsPortableAbsoluteReferences(t *testing.T) {
	native := filepath.Join(t.TempDir(), "secret.txt")
	tests := []struct {
		name      string
		reference string
		want      string
	}{
		{name: "native absolute", reference: native, want: "<absolute reference>"},
		{name: "Windows drive backslash", reference: `C:\Users\alice\secret.txt`, want: "<absolute reference>"},
		{name: "Windows drive slash", reference: "C:/Users/alice/secret.txt", want: "<absolute reference>"},
		{name: "UNC backslash", reference: `\\server\share\secret.txt`, want: "<absolute reference>"},
		{name: "UNC slash", reference: "//server/share/secret.txt", want: "<absolute reference>"},
		{name: "rooted Windows path", reference: `\Users\alice\secret.txt`, want: "<absolute reference>"},
		{name: "support kind is retained", reference: `shortcode C:\Users\alice\secret.txt`, want: "shortcode <absolute reference>"},
		{name: "ordinary relative", reference: `../shared\secret.txt`, want: `../shared\secret.txt`},
		{name: "drive-relative remains relative", reference: `C:Users\alice\secret.txt`, want: `C:Users\alice\secret.txt`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := &ReusablePatterns{}
			rp.noteSupportIssue("reusable_support_missing", tt.reference)
			if len(rp.supportIssues) != 1 {
				t.Fatalf("issues = %+v, want one", rp.supportIssues)
			}
			issue := rp.supportIssues[0]
			want := filepath.ToSlash(tt.want)
			if issue.Code != "reusable_support_missing" || issue.Reference != want {
				t.Fatalf("issue = %+v, want code reusable_support_missing and reference %q", issue, want)
			}
		})
	}
}

package analyzer

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/rustydocs/internal/config"
	"github.com/nrynss/rustydocs/internal/testutil"
)

// TestIsDefaultExcludedDir covers the name rules and the nested-repository rule
// of the default exclusions (#69). The name rules are pure; the .git rule needs
// a real directory entry, so each case gets its own temp subdirectory.
func TestIsDefaultExcludedDir(t *testing.T) {
	tests := []struct {
		name string
		// dotGit, when non-empty, creates a ".git" entry in the candidate
		// directory: "dir" for a nested clone, "file" for a worktree or
		// submodule pointer.
		dotGit string
		want   bool
	}{
		{name: "docs"},
		{name: "content"},
		{name: "snippets"},
		{name: "my-node_modules"},  // a segment boundary, not a substring
		{name: "distribution"},     // "dist" is a name, not a prefix
		{name: "builder"},          // likewise for "build"
		{name: ".git", want: true}, // the walk must never enter it
		{name: ".claude", want: true},
		{name: ".cursor", want: true},
		{name: ".github", want: true},
		{name: "node_modules", want: true},
		{name: "vendor", want: true},
		{name: "dist", want: true},
		{name: "build", want: true},
		{name: "worktree", dotGit: "file", want: true},
		{name: "nested-clone", dotGit: "dir", want: true},
		// A submodule is part of the repository under analysis, not a separate
		// project, so the walk descends into it (#69 review).
		{name: "content-sub", dotGit: "submodule"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), tt.name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			switch tt.dotGit {
			case "dir":
				if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(filepath.Join(dir, ".git"),
					[]byte("gitdir: /elsewhere/.git/worktrees/wt\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "submodule":
				if err := os.WriteFile(filepath.Join(dir, ".git"),
					[]byte("gitdir: ../.git/modules/content-sub\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := isDefaultExcludedDir(dir, tt.name); got != tt.want {
				t.Errorf("isDefaultExcludedDir(%q, dotGit=%q) = %v, want %v",
					tt.name, tt.dotGit, got, tt.want)
			}
		})
	}
}

// TestConfigIsDefaultExcludedDir_Edges pins the names that must NOT be treated
// as hidden directories: the walk's own relative spellings.
func TestConfigIsDefaultExcludedDir_Edges(t *testing.T) {
	for _, name := range []string{"", " ", ".", ".."} {
		if config.IsDefaultExcludedDir(name) {
			t.Errorf("IsDefaultExcludedDir(%q) = true, want false", name)
		}
	}
	if got := config.DefaultExcludeDirNames(); !reflect.DeepEqual(got,
		[]string{"build", "dist", "node_modules", "vendor"}) {
		t.Errorf("DefaultExcludeDirNames() = %v", got)
	}
}

// excludesRepo builds the fixture every walk test below shares: a docs tree
// holding one real page plus one junk page in each kind of directory the
// defaults are meant to prune, and one git-ignored page beside them.
//
// Everything is committed, so a file that survives the walk has real history
// and the counts are about exclusion alone, never about missing blame.
func excludesRepo(t *testing.T, now time.Time) *testutil.Repo {
	t.Helper()
	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -10), "docs", map[string]string{
		".gitignore":                  "docs/generated.md\n",
		"docs/real.md":                "# Real\n\nbody\n",
		"docs/.claude/notes.md":       "# Tool notes\n",
		"docs/node_modules/pkg/re.md": "# Vendored\n",
		"docs/vendor/v.md":            "# Vendored\n",
		"docs/dist/d.md":              "# Built\n",
		"docs/build/b.md":             "# Built\n",
	})
	// Written but never committed, which is what being git-ignored means. It is
	// therefore also a file blame cannot answer for: without the ignore filter
	// it becomes one of the spurious "unknown" rows #69 is about.
	repo.Write("docs/generated.md", "# Generated\n")
	return repo
}

// nestedRepo adds a real nested git repository under the docs tree: a separate
// project whose files git.GetGitRootForPath would resolve against a different
// repository. It is the case that cannot be expressed as a directory name.
func nestedRepo(t *testing.T, parent string, now time.Time) {
	t.Helper()
	dir := filepath.Join(parent, "nested")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "nested@example.com"},
		{"config", "user.name", "Nested"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "inner.md"), []byte("# Inner\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := now.Format(time.RFC3339)
	cmd := exec.Command("git", "commit", "-q", "-am", "inner", "--allow-empty")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	add := exec.Command("git", "add", "-A")
	add.Dir = dir
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

// TestAnalyze_DefaultExclusions is the headline of #69: on a tree that is
// mostly not documentation, the walk analyzes only the documentation, and says
// so in counts the CLI can print.
func TestAnalyze_DefaultExclusions(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := excludesRepo(t, now)
	nestedRepo(t, repo.Path("docs"), now.AddDate(0, 0, -3))

	cfg := config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")

	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if got := paths(res); !reflect.DeepEqual(got, []string{"real.md"}) {
		t.Errorf("analyzed files = %v, want [real.md]", got)
	}
	// .claude, node_modules, vendor, dist, build, nested (its own .git).
	if res.DirsSkipped() != 6 {
		t.Errorf("DirsSkipped = %d, want 6", res.DirsSkipped())
	}
	wantNames := []string{".claude", "build", "dist", "nested", "node_modules", "vendor"}
	if got := res.SkippedDirNames(); !reflect.DeepEqual(got, wantNames) {
		t.Errorf("SkippedDirNames = %v, want %v", got, wantNames)
	}
	if res.FilesGitIgnored() != 1 {
		t.Errorf("FilesGitIgnored = %d, want 1 (docs/generated.md)", res.FilesGitIgnored())
	}
	// Exclusion is not the same as failure: nothing that survived is unknown,
	// which is the 1,078-spurious-unknowns half of the issue. The ignored file
	// would have been one of them (see TestAnalyze_NoDefaultExcludes).
	if res.FilesMissingHistory() != 0 {
		t.Errorf("FilesMissingHistory = %d, want 0", res.FilesMissingHistory())
	}
	// The defaults are not user exclusions and must not be counted as them.
	if res.FilesExcluded() != 0 {
		t.Errorf("FilesExcluded = %d, want 0", res.FilesExcluded())
	}
}

// TestAnalyze_NoDefaultExcludes is the override: every junk file comes back,
// and the counts fall to zero because nothing was skipped by default.
func TestAnalyze_NoDefaultExcludes(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := excludesRepo(t, now)

	cfg := config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")
	cfg.NoDefaultExcludes = true

	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	want := []string{
		".claude/notes.md", "build/b.md", "dist/d.md", "generated.md",
		"node_modules/pkg/re.md", "real.md", "vendor/v.md",
	}
	if got := paths(res); !reflect.DeepEqual(got, want) {
		t.Errorf("analyzed files = %v, want %v", got, want)
	}
	if res.DirsSkipped() != 0 || len(res.SkippedDirNames()) != 0 || res.FilesGitIgnored() != 0 {
		t.Errorf("counts should be zero under NoDefaultExcludes: dirs=%d names=%v ignored=%d",
			res.DirsSkipped(), res.SkippedDirNames(), res.FilesGitIgnored())
	}
	// And the ignored file is back, as the "unknown" row the defaults spare
	// the reader.
	if res.FilesMissingHistory() != 1 {
		t.Errorf("FilesMissingHistory = %d, want 1 (the uncommitted, ignored file)",
			res.FilesMissingHistory())
	}
}

// TestAnalyze_UserExclusionsStayAdditive checks that exclude_dirs keeps working
// alongside the defaults, and keeps being counted separately: the two knobs
// answer different questions and their counts must not merge.
func TestAnalyze_UserExclusionsStayAdditive(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -10), "docs", map[string]string{
		"docs/real.md":            "# Real\n",
		"docs/releasenotes/rn.md": "# RN\n",
		"docs/.cursor/c.md":       "# Tool\n",
	})

	cfg := config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")
	cfg.ExcludeDirs = []string{"releasenotes"}

	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got := paths(res); !reflect.DeepEqual(got, []string{"real.md"}) {
		t.Errorf("analyzed files = %v, want [real.md]", got)
	}
	// exclude_dirs prunes the subtree, so rn.md is never visited: the
	// directory is what is counted, not the file (#69 review).
	if res.FilesExcluded() != 0 || res.DirsExcluded() != 1 {
		t.Errorf("user exclusion = FilesExcluded %d / DirsExcluded %d, want 0 and 1 (releasenotes/)",
			res.FilesExcluded(), res.DirsExcluded())
	}
	if res.DirsSkipped() != 1 || !reflect.DeepEqual(res.SkippedDirNames(), []string{".cursor"}) {
		t.Errorf("default exclusions = %d %v, want 1 [.cursor]",
			res.DirsSkipped(), res.SkippedDirNames())
	}

	// And with the defaults off, only the user exclusion applies.
	cfg2 := config.DefaultConfig()
	cfg2.ContentDir = repo.Path("docs")
	cfg2.ExcludeDirs = []string{"releasenotes"}
	cfg2.NoDefaultExcludes = true
	res2, err := Analyze(cfg2)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got := paths(res2); !reflect.DeepEqual(got, []string{".cursor/c.md", "real.md"}) {
		t.Errorf("analyzed files (no defaults) = %v", got)
	}
	// --no-default-excludes turns off the built-in rules, never the user's own
	// list, so the prune still happens.
	if res2.FilesExcluded() != 0 || res2.DirsExcluded() != 1 {
		t.Errorf("user exclusion (no defaults) = FilesExcluded %d / DirsExcluded %d, want 0 and 1",
			res2.FilesExcluded(), res2.DirsExcluded())
	}
}

// TestAnalyze_NonGitTreeStillWalks is the graceful-degradation case: a content
// tree that is not a repository at all has no ignore rules to consult, so
// nothing is filtered and its files are reported unknown exactly as before.
func TestAnalyze_NonGitTreeStillWalks(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	dir := t.TempDir()
	for rel, body := range map[string]string{
		"real.md":           "# Real\n",
		"ignored-name.md":   "# Also real\n",
		"node_modules/n.md": "# Vendored\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.DefaultConfig()
	cfg.ContentDir = dir

	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// The name-based rules still apply (they need no git); the ignore filter
	// simply finds nothing to say and drops nothing.
	if got := paths(res); !reflect.DeepEqual(got, []string{"ignored-name.md", "real.md"}) {
		t.Errorf("analyzed files = %v, want [ignored-name.md real.md]", got)
	}
	if res.FilesGitIgnored() != 0 {
		t.Errorf("FilesGitIgnored = %d, want 0 outside a repository", res.FilesGitIgnored())
	}
	if res.FilesMissingHistory() != 2 {
		t.Errorf("FilesMissingHistory = %d, want 2 (still reported unknown)", res.FilesMissingHistory())
	}
}

// TestAnalyze_ContentRootIsNeverPruned guards the one exception: the tree the
// user asked for is analyzed even when it is itself a repository root (the
// overwhelmingly common `--content-dir .`) or a dot-directory.
func TestAnalyze_ContentRootIsNeverPruned(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -10), "docs", map[string]string{
		"top.md":            "# Top\n",
		".hidden/inside.md": "# Hidden\n",
	})

	// The repository root itself holds .git, and must still be scanned.
	cfg := config.DefaultConfig()
	cfg.ContentDir = repo.Dir
	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got := paths(res); !reflect.DeepEqual(got, []string{"top.md"}) {
		t.Errorf("analyzed files at repo root = %v, want [top.md]", got)
	}

	// Pointed straight at a dot-directory, that directory is the content root
	// and is scanned rather than pruned.
	cfg2 := config.DefaultConfig()
	cfg2.ContentDir = repo.Path(".hidden")
	res2, err := Analyze(cfg2)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got := paths(res2); !reflect.DeepEqual(got, []string{"inside.md"}) {
		t.Errorf("analyzed files in dot content root = %v, want [inside.md]", got)
	}
	if res2.DirsSkipped() != 0 {
		t.Errorf("DirsSkipped = %d, want 0 (the root is not pruned)", res2.DirsSkipped())
	}
}

// paths returns the analyzed files' relative paths, already sorted by the
// analyzer.
func paths(res *Results) []string {
	out := make([]string, 0, len(res.Files))
	for _, f := range res.Files {
		out = append(out, f.RelativePath)
	}
	return out
}

// TestAnalyze_TrackedFileIsNotIgnored pins the --no-index decision in
// git.CheckIgnore: a file that is tracked is not ignored however the patterns
// read, and tracked files are precisely the ones with the blame history
// rustydocs analyzes. Dropping one because a stale pattern happens to match it
// would silently remove real documentation from the report.
func TestAnalyze_TrackedFileIsNotIgnored(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -10), "seed", map[string]string{
		"docs/real.md": "# Real\n",
	})
	// Force-add a file the ignore rules cover, the way a real repo ends up
	// carrying one, then start ignoring it.
	repo.Write("docs/tracked.md", "# Tracked anyway\n")
	cmd := exec.Command("git", "add", "-f", filepath.FromSlash("docs/tracked.md"))
	cmd.Dir = repo.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add -f: %v\n%s", err, out)
	}
	repo.Commit(now.AddDate(0, 0, -9), "track", map[string]string{
		".gitignore": "docs/tracked.md\n",
	})

	cfg := config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")
	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got := paths(res); !reflect.DeepEqual(got, []string{"real.md", "tracked.md"}) {
		t.Errorf("analyzed files = %v, want [real.md tracked.md]", got)
	}
	if res.FilesGitIgnored() != 0 {
		t.Errorf("FilesGitIgnored = %d, want 0 (the file is tracked)", res.FilesGitIgnored())
	}
}

// gitIn runs a git command in dir, failing the test on error.
func gitIn(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// TestAnalyze_SubmoduleIsScanned is the regression test for the
// nested-repository rule (#69 review). A submodule is not a different project:
// it is a subdirectory of the repository under analysis, listed in its
// .gitmodules, and a docs site that keeps a shared content tree there means
// every word of it. Pruning it dropped that documentation silently, with
// --no-default-excludes (which also switches off the dot-dir, vendored and
// gitignore rules) as the only way back.
//
// The submodule's pages are committed only in the submodule, and at a date that
// appears nowhere in the parent, so a page that is scanned *and* dated proves
// the per-directory git-root cache resolved it against the submodule's own
// repository — the capability CLAUDE.md advertises.
func TestAnalyze_SubmoduleIsScanned(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	subDate := now.AddDate(0, 0, -200)
	parentDate := now.AddDate(0, 0, -10)

	// The repository that will be embedded.
	sub := testutil.NewRepo(t)
	sub.Commit(subDate, "shared content", map[string]string{
		"sub1.md": "# Sub one\n\nbody\n",
		"sub2.md": "# Sub two\n\nbody\n",
		"sub3.md": "# Sub three\n\nbody\n",
	})

	parent := testutil.NewRepo(t)
	parent.Commit(parentDate, "docs", map[string]string{
		"docs/real.md": "# Real\n\nbody\n",
	})
	// file:// transport is refused by default in submodule commands since git
	// 2.38; the override is scoped to this one command.
	gitIn(t, parent.Dir, nil, "-c", "protocol.file.allow=always",
		"submodule", "add", "-q", sub.Dir, "docs/content-sub")
	stamp := parentDate.Format(time.RFC3339)
	gitIn(t, parent.Dir, []string{"GIT_AUTHOR_DATE=" + stamp, "GIT_COMMITTER_DATE=" + stamp},
		"commit", "-q", "-m", "add submodule")

	// The fixture is only meaningful if git really made a submodule pointer.
	dotGit := filepath.Join(parent.Dir, "docs", "content-sub", ".git")
	info, err := os.Lstat(dotGit)
	if err != nil || info.IsDir() {
		t.Skipf("this git does not use a .git file for submodule checkouts (%v)", err)
	}

	cfg := config.DefaultConfig()
	cfg.ContentDir = parent.Path("docs")
	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	want := []string{"content-sub/sub1.md", "content-sub/sub2.md", "content-sub/sub3.md", "real.md"}
	if got := paths(res); !reflect.DeepEqual(got, want) {
		t.Fatalf("analyzed files = %v, want %v", got, want)
	}
	if res.DirsSkipped() != 0 {
		t.Errorf("DirsSkipped = %d (%v), want 0: a submodule is not a nested project",
			res.DirsSkipped(), res.SkippedDirNames())
	}
	if res.FilesMissingHistory() != 0 {
		t.Errorf("FilesMissingHistory = %d, want 0: the submodule's files have history "+
			"in the submodule's own repository", res.FilesMissingHistory())
	}
	// And the date really comes from the submodule's repository: it is 200 days
	// old and exists in no commit of the parent.
	for _, f := range res.Files {
		if !strings.HasPrefix(f.RelativePath, "content-sub/") {
			continue
		}
		if f.EffectiveLastUpdated == nil || !f.EffectiveLastUpdated.Equal(subDate) {
			t.Errorf("%s dated %v, want the submodule's own %s",
				f.RelativePath, f.EffectiveLastUpdated, subDate)
		}
	}
}

// TestAnalyze_NestedStandaloneRepoStillPruned is the other side of the same
// rule: walking into a submodule must not have opened the door to a clone or a
// linked worktree someone happens to have checked out inside the docs tree.
// Those really are different projects.
func TestAnalyze_NestedStandaloneRepoStillPruned(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	pinNow(t, now)

	repo := testutil.NewRepo(t)
	repo.Commit(now.AddDate(0, 0, -10), "docs", map[string]string{
		"docs/real.md": "# Real\n\nbody\n",
	})
	nestedRepo(t, repo.Path("docs"), now.AddDate(0, 0, -3))

	// A linked worktree is a checkout root too, and is pointed at by a .git
	// *file* exactly as a submodule is — the two are told apart by where the
	// pointer leads, not by its kind.
	wt := repo.Path("docs/wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+repo.Dir+"/.git/worktrees/wt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "elsewhere.md"), []byte("# Elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.ContentDir = repo.Path("docs")
	res, err := Analyze(cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if got := paths(res); !reflect.DeepEqual(got, []string{"real.md"}) {
		t.Errorf("analyzed files = %v, want [real.md]", got)
	}
	wantNames := []string{"nested", "wt"}
	if got := res.SkippedDirNames(); !reflect.DeepEqual(got, wantNames) {
		t.Errorf("SkippedDirNames = %v, want %v", got, wantNames)
	}
}

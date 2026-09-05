package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Resolver selects how a captured reusable reference is turned into a file.
type Resolver string

const (
	// ResolverNone means the profile has no include mechanism: nothing is
	// resolved, and with no ReusablePatterns nothing is even detected.
	ResolverNone Resolver = "none"
	// ResolverHugo resolves a shortcode name to layouts/shortcodes/<name>.html
	// under the project root and traces the data files the template reads.
	ResolverHugo Resolver = "hugo"
	// ResolverPath (a direct path relative to the profile root) arrives with
	// the Mintlify profile (#7).
)

// Built-in profile names.
const (
	// ProfileMarkdown is the baseline profile every other profile extends and
	// the fallback when auto-detection finds no other project marker.
	ProfileMarkdown = "markdown"
	// ProfileHugo is the Hugo site profile (shortcodes + MDX components).
	ProfileHugo = "hugo"
)

// Profile describes how a documentation tool lays out its content: which
// files are documentation, how the project root is located, and how (if at
// all) reusable/included content is referenced and resolved. Profiles only
// supply defaults; explicit user configuration always wins (see ApplyProfile).
type Profile struct {
	Name        string
	Description string
	// ContentExtensions are the file extensions analyzed as documentation.
	ContentExtensions []string
	// RootMarkers are names searched upward from content_dir to locate the
	// project root; nil = the profile has no root concept and is never
	// auto-detected. Each marker has a kind: a name ending in "/" (e.g.
	// "layouts/") matches only a directory of that name, any other name (e.g.
	// "docs.json") matches only a regular file. A regular file named "layouts"
	// therefore does not make a tree a Hugo site. A marker may be a
	// slash-separated relative path ("config/_default/hugo.toml"); it is
	// stat'ed relative to each candidate directory and the kind rule applies
	// to its last segment.
	RootMarkers []string
	// ReusablePatterns are regexes with one capture group (the reusable name);
	// nil = reusable detection disabled.
	ReusablePatterns []string
	// ReusableExtensions are the extensions tried when resolving a reusable
	// name to a file.
	ReusableExtensions []string
	// Resolver names the resolution strategy the profile intends. It is
	// forward scaffolding and is read nowhere yet: resolution today is driven
	// by whether hugoRoot / the reusables dir are set when the analyzer calls
	// parser.NewReusablePatterns. Its first consumer will be the direct-path
	// resolver for the Mintlify profile (#7).
	Resolver Resolver
}

// hugoReusablePatterns is the single source of truth for the Hugo profile's
// reusable-reference regexes (parser.DefaultReusablePatterns builds from it).
var hugoReusablePatterns = []string{
	// Hugo shortcodes: {{< name >}}, {{% name %}}, {{< name param >}}, etc.
	`\{\{[<%]\s*([a-zA-Z][\w/-]*)\s*[^%>]*[%>]\}\}`,
	// MDX/JSX components: <Component>, <Component />, <Component prop="val">
	`<([A-Z][a-zA-Z0-9]*)\s*[^>]*/?>`,
}

// builtinProfiles is the profile registry. Order matters for auto-detection
// only as a tie-breaker: detectProfile walks up from content_dir one level at
// a time and the nearest level holding any profile's marker wins; when two
// profiles' markers sit at the same level, the earlier one here is chosen.
// The markdown profile has no markers and is the fallback.
var builtinProfiles = []Profile{
	{
		Name: ProfileMarkdown,
		Description: "Plain Markdown (CommonMark/GFM): .md and .markdown files, ATX '#' headers, " +
			"no include mechanism (reusable detection off). Default when nothing else is detected.",
		ContentExtensions: []string{".md", ".markdown"},
		Resolver:          ResolverNone,
	},
	{
		Name: ProfileHugo,
		Description: "Hugo site: .md, .markdown and .mdx content, Hugo shortcode and MDX component " +
			"detection, shortcodes resolved from layouts/shortcodes and each " +
			"themes/<theme>/layouts/shortcodes. Auto-detected from a layouts/ or themes/ directory, " +
			"a hugo.{toml,yaml,json} file, or a config/_default/ Hugo config at or above content_dir, " +
			"searching no further than the enclosing git repository.",
		ContentExtensions: []string{".md", ".markdown", ".mdx"},
		// layouts/ alone is not enough: git does not track empty directories,
		// so a fresh clone of a site that keeps its shortcodes in a theme has
		// no layouts/ dir. Hugo's own config file names are unambiguous
		// markers, as is a themes/ directory. Top-level config.toml/config.yaml
		// are deliberately left out because too many other tools use them, but
		// under config/_default/ (Hugo's split-config layout) the generic names
		// are unambiguous, so both hugo.* and config.* are accepted there.
		// Sites matching none of these markers must pass --profile hugo or set
		// hugo_root.
		RootMarkers: []string{
			"layouts/", "themes/",
			"hugo.toml", "hugo.yaml", "hugo.json",
			"config/_default/hugo.toml", "config/_default/hugo.yaml", "config/_default/hugo.json",
			"config/_default/config.toml", "config/_default/config.yaml", "config/_default/config.json",
		},
		ReusablePatterns:   hugoReusablePatterns,
		ReusableExtensions: []string{".md", ".mdx", ".html"},
		Resolver:           ResolverHugo,
	},
}

// clone returns a deep copy so callers can mutate slices without touching the
// registry.
func (p Profile) clone() Profile {
	p.ContentExtensions = cloneStrings(p.ContentExtensions)
	p.RootMarkers = cloneStrings(p.RootMarkers)
	p.ReusablePatterns = cloneStrings(p.ReusablePatterns)
	p.ReusableExtensions = cloneStrings(p.ReusableExtensions)
	return p
}

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s...)
}

// Profiles returns the names of all built-in profiles, sorted.
func Profiles() []string {
	names := make([]string, 0, len(builtinProfiles))
	for _, p := range builtinProfiles {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return names
}

// AllProfiles returns copies of all built-in profiles, sorted by name.
func AllProfiles() []Profile {
	out := make([]Profile, 0, len(builtinProfiles))
	for _, p := range builtinProfiles {
		out = append(out, p.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LookupProfile returns a copy of the built-in profile with the given name.
// Names are matched case-insensitively and with surrounding whitespace trimmed.
func LookupProfile(name string) (Profile, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, p := range builtinProfiles {
		if p.Name == name {
			return p.clone(), true
		}
	}
	return Profile{}, false
}

// DefaultProfile returns the markdown profile: the baseline used when no
// profile is selected and nothing is detected.
func DefaultProfile() Profile {
	p, _ := LookupProfile(ProfileMarkdown)
	return p
}

// mustProfile returns a built-in profile or panics; for registry-internal use
// where the name is a compile-time constant.
func mustProfile(name string) Profile {
	p, ok := LookupProfile(name)
	if !ok {
		panic(fmt.Sprintf("config: built-in profile %q missing from registry", name))
	}
	return p
}

// walkUp calls visit with contentDir (made absolute) and then each ancestor
// in turn, stopping when visit returns true, when the level just visited is the
// root of a git repository, or when the filesystem root has been visited.
// contentDir is made absolute first so a relative path is searched all the way
// up, not just to the working directory.
//
// The repository bound keeps auto-detection inside the checkout: a repo that
// happens to live under a directory named themes/ or layouts/ must not be
// mistaken for a Hugo site rooted at that directory's parent. The repository
// root itself is examined (a marker sitting next to .git still matches); only
// the levels above it are skipped. A submodule checkout is deliberately not a
// bound - the walk continues up into the parent repository, because a Hugo site
// whose content/ is a submodule keeps its layouts/ and hugo.toml one level up
// (see isRepoRoot). When no .git exists anywhere up the chain the walk reaches
// the filesystem root, as it always has.
func walkUp(contentDir string, visit func(dir string) bool) {
	dir := filepath.Clean(contentDir)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	for {
		if visit(dir) {
			return
		}
		if isRepoRoot(dir) {
			// Do not search outside the enclosing repository.
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root
			return
		}
		dir = parent
	}
}

// isRepoRoot reports whether dir is a repository root the walk must not leave.
//
// A .git *directory* (a normal clone) always is. A .git *file* is a pointer of
// the form "gitdir: <path>" and has two very different meanings:
//
//   - a linked worktree, <repo>/.git/worktrees/<name>, where dir really is a
//     checkout root and the walk must stop; and
//   - a submodule checkout, <parent>/.git/modules/<path>, where dir is only a
//     subdirectory of the parent repository. Hugo sites commonly keep content/
//     as a submodule, and the site's layouts/ and hugo.toml live one level up,
//     so the walk has to continue through it.
//
// Anything that cannot be read or parsed stops the walk (conservative: an
// unreadable pointer is treated as a checkout root rather than an invitation to
// walk out into unrelated directories). Lstat is used first so a symlinked .git
// counts too: when it points at a directory the read below fails and the walk
// stops.
func isRepoRoot(dir string) bool {
	path := filepath.Join(dir, ".git")
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	return !gitFileIsSubmodule(string(data))
}

// gitFileIsSubmodule reports whether the contents of a .git file point into a
// parent repository's .git/modules/ directory, i.e. the directory holding that
// file is a submodule checkout rather than a repository root of its own.
//
// Only the first line is read; git writes "gitdir: <path>", absolute or
// relative ("gitdir: ../.git/modules/content"). The two kinds nest in both
// directions, so the decision is made on the *tail* of the path, not on which
// segment happens to follow ".git":
//
//	/r/.git/worktrees/wt                     worktree  -> false (stop)
//	../.git/modules/content                  submodule -> true  (continue)
//	../../p/.git/worktrees/wt/modules/content submodule inside a linked
//	                                         worktree  -> true  (continue)
//	/r/.git/modules/sub/worktrees/wt         worktree inside a submodule
//	                                         -> false (stop)
//
// git always appends the administrative suffix last, so the innermost kind is
// the one whose name ends the path: it is a worktree checkout when the final
// two segments are "worktrees/<name>", and otherwise a submodule checkout when
// any segment after the ".git" segment is "modules" (requiring the ".git"
// prefix keeps a pointer that merely lives under some directory named
// "modules" from being misread). Anything else (an empty path, unparsable
// junk) reports false, so the caller stops the walk.
//
// Separators are normalised because a pointer written on Windows may use
// backslashes; surrounding whitespace and a CRLF line ending are tolerated.
func gitFileIsSubmodule(content string) bool {
	line, _, _ := strings.Cut(content, "\n")
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	if !ok {
		return false
	}
	target := strings.TrimSpace(rest)
	if target == "" {
		return false
	}
	segments := strings.Split(strings.ReplaceAll(filepath.ToSlash(target), `\`, "/"), "/")
	// Innermost kind wins: a trailing "worktrees/<name>" is a linked worktree,
	// whatever it is nested inside.
	if n := len(segments); n >= 2 && segments[n-2] == "worktrees" && segments[n-1] != "" {
		return false
	}
	for i := 0; i < len(segments)-1; i++ {
		if segments[i] != ".git" {
			continue
		}
		for _, seg := range segments[i+1:] {
			if seg == "modules" {
				return true
			}
		}
		return false
	}
	return false
}

// hasMarker reports whether dir contains any of the markers, honouring each
// marker's kind: a name ending in "/" must be a directory, any other name must
// be a regular file (see Profile.RootMarkers). A marker holding "/" separators
// is a path relative to dir; it is converted to the host separator before
// stat'ing.
func hasMarker(dir string, markers []string) bool {
	for _, m := range markers {
		name, wantDir := markerKind(m)
		if name == "" {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			continue
		}
		if info.IsDir() == wantDir {
			return true
		}
	}
	return false
}

// markerKind splits a marker into the slash-separated relative path to stat
// and whether its last segment must be a directory ("layouts/" -> "layouts",
// true; "docs.json" -> "docs.json", false; "config/_default/hugo.toml" ->
// unchanged, false). Only a trailing "/" carries meaning; interior separators
// are kept as path segments.
func markerKind(marker string) (name string, wantDir bool) {
	name = strings.TrimSpace(marker)
	if strings.HasSuffix(name, "/") {
		return strings.TrimRight(name, "/"), true
	}
	return name, false
}

// DetectRoot walks up from contentDir looking for any of the markers (a
// directory when the marker ends in "/", otherwise a regular file; a marker
// may be a slash-separated relative path — see Profile.RootMarkers) and
// returns the nearest directory that contains one, or "" when no match is
// found. The walk is bounded by the nearest enclosing git repository: it stops
// after examining the directory that holds .git (a submodule checkout excepted
// - there the walk continues into the parent repository), and only reaches the
// filesystem root when there is no repository above contentDir (see walkUp).
func DetectRoot(contentDir string, markers []string) string {
	if len(markers) == 0 {
		return ""
	}
	var found string
	walkUp(contentDir, func(dir string) bool {
		if hasMarker(dir, markers) {
			found = dir
			return true
		}
		return false
	})
	return found
}

// detectNearest walks up from contentDir one level at a time and, at each
// level, checks every marker-bearing candidate in the given order. The first
// level where any candidate's marker is present decides; a tie within a level
// resolves to the earlier candidate. It returns a copy of the winning profile,
// the directory it was found at, and false when no marker exists anywhere up
// the (repository-bounded, see walkUp) chain. A nearer marker therefore always
// beats a farther one, whatever the candidates' order — e.g. a Mintlify
// docs.json inside a repo whose root also has a Hugo layouts/ directory
// selects Mintlify.
func detectNearest(contentDir string, candidates []Profile) (Profile, string, bool) {
	var (
		found Profile
		root  string
		ok    bool
	)
	walkUp(contentDir, func(dir string) bool {
		for _, p := range candidates {
			if len(p.RootMarkers) == 0 {
				continue
			}
			if hasMarker(dir, p.RootMarkers) {
				found, root, ok = p.clone(), dir, true
				return true
			}
		}
		return false
	})
	return found, root, ok
}

// detectProfile auto-selects a profile for contentDir: an explicit hugo_root
// implies hugo; otherwise the registry profiles with RootMarkers are probed
// with detectNearest (nearest marker wins, registry order breaks ties, and the
// search never leaves the enclosing git repository — see walkUp). It returns
// the profile and the root it was detected at ("" for the markdown fallback).
func detectProfile(contentDir, hugoRoot string) (Profile, string) {
	if hugoRoot != "" {
		return mustProfile(ProfileHugo), hugoRoot
	}
	if contentDir != "" {
		if p, root, ok := detectNearest(contentDir, builtinProfiles); ok {
			return p, root
		}
	}
	return DefaultProfile(), ""
}

// ApplyProfile resolves the profile (explicit c.Profile, else auto-detected
// from c.ContentDir) into c.ResolvedProfile and fills in every setting the
// user left empty from that profile. Call it after merging file + CLI config
// and before Normalize. Explicit user values always win:
//
//   - ContentExtensions: canonicalised in place with NormalizeExtensions so the
//     stored list is exactly what the walk matches on; empty -> profile's (or
//     the hugo profile's under the legacy reusables-dir flow described below,
//     which predates profiles and analyzed .mdx). Non-empty on entry sets
//     ExtensionsFromUser, the only reliable signal that the allowlist in force
//     is a user override - the legacy flow makes it differ from the profile's
//     list too.
//   - Reusables.Patterns: empty -> profile's (the markdown profile has none, so
//     detection stays off). Backward compatibility: a reusables dir with no
//     patterns (the legacy reusables_dir / --reusables-dir flow) on a profile
//     without patterns gets the hugo profile's pattern list *and* content
//     extensions, so plain repos that pointed at a reusables directory keep
//     detecting shortcodes/components in .md and .mdx files as before.
//   - Reusables.Extensions: empty -> profile's.
//   - HugoRoot: empty and the profile has RootMarkers -> the detected root.
func (c *Config) ApplyProfile() error {
	// Canonicalise before anything reads the list, including the check below:
	// an allowlist of nothing but blanks ("--extensions ''") is no override at
	// all, and must fall through to the profile's defaults rather than leave
	// the walk with an empty set.
	c.ContentExtensions = NormalizeExtensions(c.ContentExtensions)

	// Record where the extension allowlist came from before any default is
	// filled in. Guarded on the profile not yet being resolved so a second
	// ApplyProfile (the analyzer's guard) cannot mistake the defaults this
	// call wrote for a user override.
	if c.ResolvedProfile.Name == "" {
		c.ExtensionsFromUser = len(c.ContentExtensions) > 0
	}

	var (
		p    Profile
		root string
	)
	if strings.TrimSpace(c.Profile) != "" {
		var ok bool
		p, ok = LookupProfile(c.Profile)
		if !ok {
			return fmt.Errorf("unknown profile %q (valid profiles: %s)",
				c.Profile, strings.Join(Profiles(), ", "))
		}
		c.ProfileAuto = false
		root = c.HugoRoot
		if root == "" && len(p.RootMarkers) > 0 && c.ContentDir != "" {
			root = DetectRoot(c.ContentDir, p.RootMarkers)
		}
	} else {
		p, root = detectProfile(c.ContentDir, c.HugoRoot)
		c.ProfileAuto = true
	}
	c.ResolvedProfile = p

	// Deprecated top-level reusables_dir (and --reusables-dir) is an alias for
	// reusables.dir; merge here so the fallback below sees one value.
	if c.Reusables.Dir == "" && c.ReusablesDir != "" {
		c.Reusables.Dir = c.ReusablesDir
	}

	// Legacy reusables-dir flow: a reusables dir was pointed at, the resolved
	// profile has no include mechanism of its own and no patterns were
	// configured. That flow predates profiles and was built for Hugo
	// shortcodes / MDX components, so it restores the hugo profile's pattern
	// list *and* its content extensions (dropping .mdx here would silently stop
	// analyzing the very files these setups use).
	legacyReusablesDir := c.Reusables.Dir != "" &&
		len(c.Reusables.Patterns) == 0 &&
		len(p.ReusablePatterns) == 0

	if len(c.ContentExtensions) == 0 {
		exts := p.ContentExtensions
		if legacyReusablesDir {
			exts = mustProfile(ProfileHugo).ContentExtensions
		}
		c.ContentExtensions = NormalizeExtensions(exts)
	}
	if len(c.Reusables.Patterns) == 0 {
		switch {
		case len(p.ReusablePatterns) > 0:
			c.Reusables.Patterns = cloneStrings(p.ReusablePatterns)
		case legacyReusablesDir:
			c.Reusables.Patterns = cloneStrings(mustProfile(ProfileHugo).ReusablePatterns)
		}
	}
	if len(c.Reusables.Extensions) == 0 {
		exts := p.ReusableExtensions
		if len(exts) == 0 && len(c.Reusables.Patterns) > 0 {
			exts = mustProfile(ProfileHugo).ReusableExtensions
		}
		c.Reusables.Extensions = cloneStrings(exts)
	}
	if c.HugoRoot == "" && len(p.RootMarkers) > 0 {
		c.HugoRoot = root
	}
	return nil
}

// NormalizeExtensions canonicalises a content-extension allowlist into the
// exact form the analyzer's directory walk matches on: lowercased, trimmed,
// dot-prefixed, blanks dropped, duplicates removed, input order preserved.
// It returns nil when nothing survives, so callers can keep testing len() == 0
// for "not set".
//
// ApplyProfile applies it to Config.ContentExtensions in place, which is what
// makes the stored config agree with the walk — the banner, the stderr
// warnings and the JSON report's config.content_extensions all echo that field
// and would otherwise show the raw user input ("mdx", ".MD") rather than the
// allowlist actually scanned (#11).
func NormalizeExtensions(exts []string) []string {
	if len(exts) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(exts))
	out := make([]string, 0, len(exts))
	for _, e := range exts {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		if _, ok := seen[e]; ok {
			continue
		}
		seen[e] = struct{}{}
		out = append(out, e)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// KnownContentExtensions returns the sorted union of the ContentExtensions of
// every built-in profile — the set of extensions rustydocs recognises as
// documentation under *some* profile.
//
// It exists so the analyzer can tell "this file is not documentation" (an
// image, a .txt, a .json) from "this file is documentation the active profile
// does not analyze" (an .mdx tree running under the markdown profile), and
// report only the latter. See Results.FilesSkippedByExtension (#11).
func KnownContentExtensions() []string {
	all := make([]string, 0, 4)
	for _, p := range builtinProfiles {
		all = append(all, p.ContentExtensions...)
	}
	out := NormalizeExtensions(all)
	sort.Strings(out)
	return out
}

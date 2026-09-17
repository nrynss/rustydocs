package config

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// ResolverPath treats the capture as a filesystem path rather than a name.
	// A capture starting with "/" is resolved against the project root; one
	// starting with "./" or "../" against the directory of the referencing
	// file first; and a bare name or bare relative path — the form Mintlify
	// projects actually use — against the project's snippets directory
	// ("snippets/", then "_snippets/"), then the root, then the referencing
	// file's directory. A path that escapes the root is ignored, and a capture
	// with no extension is tried against ReusableExtensions in order. Used by
	// the Mintlify profile, whose <Snippet file="…" /> carries the path
	// outright with no indirection to trace (#7). See
	// parser.ReusablePatterns.directPathBases for the full order.
	ResolverPath Resolver = "path"
)

// Built-in profile names.
const (
	// ProfileMarkdown is the baseline profile every other profile extends and
	// the fallback when auto-detection finds no other project marker.
	ProfileMarkdown = "markdown"
	// ProfileHugo is the Hugo site profile (shortcodes + MDX components).
	ProfileHugo = "hugo"
	// ProfileMintlify is the Mintlify docs profile (<Snippet file="…" />).
	ProfileMintlify = "mintlify"
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
	// to its last segment. A marker may additionally carry a content predicate
	// (see markerPredicates).
	RootMarkers []string
	// markerPredicates optionally constrains individual RootMarkers by
	// content: a marker listed here matches only when the file found at that
	// path also satisfies its predicate. Markers absent from the map match on
	// name and kind alone.
	//
	// It exists because a file name is weak evidence. "docs.json" is
	// Mintlify's config file, but it is also a plausible name for any number
	// of unrelated files, and a false match silently downgrades a project to
	// the wrong profile — a Hugo site that happens to carry a docs.json would
	// stop tracing shortcodes. Directory markers ("layouts/") have no
	// contents to inspect and are never predicated.
	//
	// Unexported: only this registry defines predicates, and keeping a func
	// out of the exported struct keeps Profile comparable with
	// reflect.DeepEqual and free of surprises for anything that marshals it.
	// Profile.DetectRoot is the only exported way to locate a root, so
	// predicates cannot be bypassed by accident.
	markerPredicates map[string]markerPredicate
	// ReusablePatterns are regexes with one capture group (the reusable name);
	// nil = reusable detection disabled.
	ReusablePatterns []string
	// ReusableExtensions are the extensions tried when resolving a reusable
	// name to a file.
	ReusableExtensions []string
	// Resolver names the resolution strategy: how a captured reference is
	// turned into a file whose git history is folded into the referencing
	// section. The analyzer passes it to parser.NewReusablePatternsFor,
	// which dispatches on it — ResolverHugo traces shortcode templates under
	// the project root, ResolverPath treats the capture as a path relative to
	// that root, ResolverNone resolves nothing. The legacy reusables_dir
	// lookup is independent of it and still applies whenever a reusables
	// directory is configured.
	Resolver Resolver
	// ImportMap turns on the MDX import-map layer: the parser reads each page's
	// `import X from "…"` / `import { A, B } from "…"` statements, resolves the
	// paths with this profile's Resolver, and attributes a section's use of
	// <X /> to the imported file's freshness. It is a layer on top of
	// ResolverPath, not a replacement — <Snippet file="…" /> keeps resolving
	// alongside it.
	//
	// Only .md and .mdx imports are followed. A .jsx / .js / .css import is
	// detected and deliberately skipped, and so is a capitalised tag that no
	// import introduced: neither is an unresolved include, both are out of
	// scope by design. Folding a React component's commit date into a section
	// would only ever make the section look *fresher*, so restyling one shared
	// component would mark every page importing it as recently updated — the
	// precise signal this tool exists to protect (#68).
	ImportMap bool
}

// markerPredicate reports whether the file found at a marker's path really is
// the config file the profile is looking for. It is only ever called with a
// path that already matched the marker's name and kind.
//
// The two ways of saying "no" are deliberately distinct. A malformed or simply
// unrelated file returns (false, nil): that is not a problem, it is just not a
// match, and detection carries on in silence. A file that could not be *read*
// at all — permissions, an I/O error — returns (false, err): it still does not
// select the profile, but the caller surfaces the error, because a chmod 000
// docs.json silently downgrading a real Mintlify project to the markdown
// profile is the kind of failure a user has no way to diagnose (#7).
type markerPredicate func(path string) (bool, error)

// markerWarnFunc receives a marker candidate that could not be read. It is
// how the walk reports a predicate's read failure without printing: the config
// package never writes to stderr, so ApplyProfile collects these on
// Config.Warnings and the CLI prints them.
type markerWarnFunc func(profileName, marker, path string, err error)

// mintlifyConfigKeys are top-level keys that mark a JSON document as a
// Mintlify configuration. Mintlify's own docs.json requires "theme", "name",
// "colors" and "navigation", and mint.json (the legacy name) requires "name",
// "navigation" and "colors", so any real config carries several of these.
//
// "name" is deliberately *not* in the set: it is the one key of that list that
// is common to half the JSON files in existence (package.json's, for one), so
// accepting it alone would defeat the point of validating at all.
var mintlifyConfigKeys = map[string]bool{
	"navigation": true,
	"theme":      true,
	"colors":     true,
	"logo":       true,
	"favicon":    true,
	"tabs":       true,
	"anchors":    true,
}

// maxMintlifyConfigBytes caps how much of a candidate docs.json / mint.json is
// read before the predicate gives up. A real Mintlify config is a few
// kilobytes; 1 MiB is generous even for a site with a huge navigation tree.
//
// The cap is what actually bounds memory. Streaming the document key by key
// stops the *parse* early, but each top-level value is still buffered whole
// before its key can be judged, so a docs.json whose first key holds a 200 MB
// string would otherwise allocate 200 MB just to decide it is not a Mintlify
// config. Reading through an io.LimitReader turns that into a truncated
// document, which fails to decode and is simply not a match.
const maxMintlifyConfigBytes = 1 << 20

// isMintlifyConfig reports whether path holds something recognisably like a
// Mintlify docs.json / mint.json: a JSON object carrying a "$schema" that
// mentions mintlify, or any of mintlifyConfigKeys at the top level.
//
// The guarantee is bounded work, not constant work: at most
// maxMintlifyConfigBytes are read, and the scan stops at the first key that
// decides, so at worst one top-level value up to that cap is buffered. A file
// larger than the cap is read only up to it — a deciding key found before the
// cut still matches, and anything after it is truncated away and therefore
// fails to decode. Anything that is not a JSON object — a directory, an array,
// truncated or malformed JSON — is not a match.
//
// The error return is reserved for "could not read this file at all"
// (permissions, I/O); a file that reads fine but is not a Mintlify config is
// (false, nil). See markerPredicate.
func isMintlifyConfig(path string) (bool, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(bufio.NewReader(io.LimitReader(f, maxMintlifyConfigBytes)))
	tok, err := dec.Token()
	if err != nil {
		return false, nil
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return false, nil
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return false, nil
		}
		key, _ := keyTok.(string)
		// The value is always consumed before the key is judged, so a
		// truncated document ("{\"navigation\":") fails rather than passing on
		// the strength of a key whose value never arrived.
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return false, nil
		}
		if mintlifyConfigKeys[key] {
			return true, nil
		}
		if key == "$schema" {
			var schema string
			if json.Unmarshal(value, &schema) == nil &&
				strings.Contains(strings.ToLower(schema), "mintlify") {
				return true, nil
			}
		}
	}
	return false, nil
}

// MDXComponentPattern captures the tag name of an MDX/JSX component usage:
// <Component>, <Component />, <Component prop="val">. Closing tags do not match
// (the "<" is followed by "/"), which is what is wanted — one capture per
// element is enough.
//
// It is shared by every profile that reads component usage. What a capture
// *means* differs by profile: to hugo it is a shortcode name to look up, to a
// profile with an import map it is a symbol that only counts when an import
// introduced it (see Profile.ImportMap). The regex is the same either way, and
// having one copy keeps the two from drifting (#68).
const MDXComponentPattern = `<([A-Z][a-zA-Z0-9]*)\s*[^>]*/?>`

// hugoReusablePatterns is the single source of truth for the Hugo profile's
// reusable-reference regexes (parser.DefaultReusablePatterns builds from it).
var hugoReusablePatterns = []string{
	// Hugo shortcodes: {{< name >}}, {{% name %}}, {{< name param >}}, etc.
	`\{\{[<%]\s*([a-zA-Z][\w/-]*)\s*[^%>]*[%>]\}\}`,
	MDXComponentPattern,
}

// mintlifyReusablePatterns captures the two ways a Mintlify page includes
// shared content.
//
// The first two capture the *path* in <Snippet file="aws-config.mdx" />, which
// the path resolver looks up under the project's snippets directory (or against
// the project root when the capture is root-absolute). Both MDX quote styles
// are legal, so there are two patterns rather than one with two alternatives:
// reusable detection reads capture group 1 of each pattern, and an alternation
// would leave one group empty on every match. "<Snippet\b" (rather than a bare
// "<Snippet") keeps a hypothetical <SnippetGroup file="…"> — a different
// element with different semantics — from being read as a snippet include.
//
// The third captures component usage, for the import map (see ImportMap). It is
// the form that actually appears in the wild: measured over a production
// Mintlify site, <Snippet file=…> occurred zero times and every reusable
// reference was an MDX import rendered as <X /> (#68). It could not be enabled
// before the import map existed, because on its own it captures the name of
// every <Card /> and <Tabs> on the page, none of which names a file; with the
// map, a capture that no import introduced is *skipped* rather than reported
// unresolved (see parser.ResolveReusable).
var mintlifyReusablePatterns = []string{
	`<Snippet\b[^>]*\bfile="([^"]+)"`,
	`<Snippet\b[^>]*\bfile='([^']+)'`,
	MDXComponentPattern,
}

// builtinProfiles is the profile registry. Order matters for auto-detection
// only as a tie-breaker: detectProfile walks up from content_dir one level at
// a time and the nearest level holding any profile's marker wins; when two
// profiles' markers sit at the same level, the earlier one here is chosen.
// The markdown profile has no markers and is the fallback.
//
// hugo precedes mintlify: at the same level, the Hugo markers are the stronger
// evidence. A Hugo site's layouts/ or themes/ directory, or its hugo.toml, is
// unambiguous, whereas a docs.json sitting next to them could belong to
// anything — and picking mintlify there would silently turn Hugo shortcode
// tracing off on a site that needs it. Mintlify's markers additionally have to
// pass a content predicate (see isMintlifyConfig), so a file merely *named*
// docs.json no longer selects the profile at all. A tie only arises when both
// markers sit in the same directory; whenever a Mintlify docs tree is nested
// below a Hugo marker (or vice versa) the nearer marker wins regardless of
// this order.
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
		// Sites matching none of these markers must pass --profile hugo.
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
	{
		Name: ProfileMintlify,
		Description: "Mintlify docs: .md and .mdx content, ATX '#' headers, snippet includes " +
			"(<Snippet file=\"foo.mdx\" />) and MDX imports (import X from \"/snippets/foo.mdx\", " +
			"used as <X />) resolved as paths under snippets/ or _snippets/ at " +
			"the project root; .jsx/.js/.css imports are deliberately skipped. " +
			"Auto-detected from a docs.json (current) or mint.json (legacy) file whose contents " +
			"look like a Mintlify config, at or above content_dir, searching no further than the " +
			"enclosing git repository.",
		ContentExtensions: []string{".md", ".mdx"},
		// Both are regular files (no trailing "/"): docs.json is the current
		// Mintlify config, mint.json the legacy name. The directory holding one
		// is the project root that snippet paths resolve against. Both are
		// validated by content as well as by name, so an unrelated docs.json
		// (or an unparsable one) does not select the profile — see
		// markerPredicates and isMintlifyConfig.
		RootMarkers: []string{"docs.json", "mint.json"},
		markerPredicates: map[string]markerPredicate{
			"docs.json": isMintlifyConfig,
			"mint.json": isMintlifyConfig,
		},
		ReusablePatterns:   mintlifyReusablePatterns,
		ReusableExtensions: []string{".mdx", ".md"},
		Resolver:           ResolverPath,
		ImportMap:          true,
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
// (see IsRepoRoot). When no .git exists anywhere up the chain the walk reaches
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
		if IsRepoRoot(dir) {
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

// IsRepoRoot reports whether dir is the root of a repository of its own — a
// boundary neither the profile root walk nor the content walk may cross.
//
// A .git *directory* (a normal clone) always is. A .git *file* is a pointer of
// the form "gitdir: <path>" and has two very different meanings:
//
//   - a linked worktree, <repo>/.git/worktrees/<name>, where dir really is a
//     checkout root; and
//   - a submodule checkout, <parent>/.git/modules/<path>, where dir is only a
//     subdirectory of the parent repository. Hugo sites commonly keep content/
//     as a submodule, and the site's layouts/ and hugo.toml live one level up,
//     so the walk has to continue through it.
//
// Both walks want the same answer, which is why this is exported rather than
// copied: walkUp continues up through a submodule to find the enclosing
// project's root, and analyzer's content walk descends into one because a
// submodule full of documentation is documentation this run should analyze
// (#69 review). A standalone nested repository is a boundary for both.
//
// Anything that cannot be read or parsed counts as a root (conservative: an
// unreadable pointer is treated as a checkout root rather than an invitation to
// walk out into, or down into, unrelated directories). Lstat is used first so a
// symlinked .git counts too: when it points at a directory the read below fails
// and the answer is "root".
func IsRepoRoot(dir string) bool {
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
//
// preds may carry a content predicate per marker (keyed by the marker exactly
// as it appears in the list); a marker with one matches only when the file
// found also satisfies it. Pass nil for name-and-kind matching only.
//
// warn (may be nil) is called with the marker whose predicate could not read
// the candidate file. Such a marker still does not match — detection is
// unchanged — but the caller gets to tell the user why a project that looks
// like it should have been detected was not (see markerPredicate).
func hasMarker(dir string, markers []string, preds map[string]markerPredicate, profileName string, warn markerWarnFunc) bool {
	for _, m := range markers {
		name, wantDir := markerKind(m)
		if name == "" {
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(name))
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.IsDir() != wantDir {
			continue
		}
		if pred := preds[m]; pred != nil {
			ok, err := pred(path)
			if err != nil {
				if warn != nil {
					warn(profileName, m, path, err)
				}
				continue
			}
			if !ok {
				continue
			}
		}
		return true
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

// DetectRoot finds the profile's project root the way auto-detection does: the
// nearest ancestor of contentDir holding one of the profile's RootMarkers (a
// directory when the marker ends in "/", otherwise a regular file; a marker
// may be a slash-separated relative path — see Profile.RootMarkers), with each
// marker's content predicate applied. Returns "" for a profile with no
// markers, or when no marker is found.
//
// The walk is bounded by the nearest enclosing git repository: it stops after
// examining the directory that holds .git (a submodule checkout excepted -
// there the walk continues into the parent repository), and only reaches the
// filesystem root when there is no repository above contentDir (see walkUp).
//
// This is deliberately the only exported entry point: a marker-list form that
// skipped the predicates would let a caller reintroduce the bug the predicates
// exist to prevent (an unrelated docs.json selecting the Mintlify profile).
// warn (may be nil) receives markers whose predicate could not read the
// candidate file; see hasMarker.
func (p Profile) DetectRoot(contentDir string, warn markerWarnFunc) string {
	return detectRoot(contentDir, p.RootMarkers, p.markerPredicates, p.Name, warn)
}

func detectRoot(contentDir string, markers []string, preds map[string]markerPredicate, profileName string, warn markerWarnFunc) string {
	if len(markers) == 0 {
		return ""
	}
	var found string
	walkUp(contentDir, func(dir string) bool {
		if hasMarker(dir, markers, preds, profileName, warn) {
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
// resolves to the earlier candidate. Each candidate's marker content
// predicates apply, so a file that merely has a marker's name does not select
// its profile. It returns a copy of the winning profile, the directory it was
// found at, and false when no marker exists anywhere up the
// (repository-bounded, see walkUp) chain. A nearer marker therefore always
// beats a farther one, whatever the candidates' order — e.g. a Mintlify
// docs.json inside a repo whose root also has a Hugo layouts/ directory
// selects Mintlify.
func detectNearest(contentDir string, candidates []Profile, warn markerWarnFunc) (Profile, string, bool) {
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
			if hasMarker(dir, p.RootMarkers, p.markerPredicates, p.Name, warn) {
				found, root, ok = p.clone(), dir, true
				return true
			}
		}
		return false
	})
	return found, root, ok
}

// detectProfile auto-selects a profile for contentDir: the registry profiles
// with RootMarkers are probed with detectNearest (nearest marker wins,
// registry order breaks ties, and the search never leaves the enclosing git
// repository — see walkUp). It returns the profile, the root it was detected
// at, and whether a marker was found at all; the fallback is the markdown
// profile with no root.
//
// Detection is marker-driven and nothing else. A project root the user
// supplied is honoured for *resolution* whatever profile is selected, but it
// never biases the selection: a --project-root on a Mintlify tree used to
// select hugo, whose component pattern then captured "Snippet" as a name and
// resolved nothing.
func detectProfile(contentDir string, warn markerWarnFunc) (Profile, string, bool) {
	if contentDir != "" {
		if p, root, ok := detectNearest(contentDir, builtinProfiles, warn); ok {
			return p, root, true
		}
	}
	return DefaultProfile(), "", false
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
//   - ProjectRoot: empty and the profile has RootMarkers -> the detected root.
//     The deprecated "hugo_root" key is folded into it first (see
//     foldLegacyRoot); "project_root" / --project-root wins when both are set.
//     A root the *user* supplied is validated: it must exist and be a
//     directory, or ApplyProfile fails (see validateExplicitRoot). A supplied
//     root never influences which profile is selected — the one exception is
//     the legacy fallback described at its call site below.
//
// It also resets and repopulates Warnings with any non-fatal diagnostic this
// call produced — an unreadable candidate marker, and the deprecation notice
// for a "hugo_root" key (whether it supplied the root or was overridden by
// "project_root"); the caller prints them.
func (c *Config) ApplyProfile() error {
	// Fold the deprecated "hugo_root" spelling into ProjectRoot so everything
	// below sees one field, remembering which key supplied the value. Guarded
	// on the profile not yet being resolved, exactly as ExtensionsFromUser is
	// below: a second ApplyProfile (the analyzer's guard) would otherwise read
	// the root this call *detected* as one the user supplied.
	if c.ResolvedProfile.Name == "" {
		c.foldLegacyRoot()
	}

	// Warnings belong to this call: a second ApplyProfile (the analyzer's
	// guard) must not double them up.
	c.Warnings = nil
	// The deprecated key earns a warning whenever it is present at all, not
	// only when it supplied the root. When both spellings are set the legacy
	// one is silently dropped by foldLegacyRoot, and saying nothing there was
	// the worst of the three outcomes: a config carrying a stale "hugo_root"
	// pointing somewhere else looked like it was in force (#7 review pass 4).
	switch {
	case c.rootSource == rootSourceLegacyHugoRoot:
		c.Warnings = append(c.Warnings, fmt.Sprintf(
			"config key \"hugo_root\" is deprecated and will be removed in a future "+
				"release; rename it to \"project_root\" (CLI: --project-root). "+
				"The project root is %q, unchanged.", c.ProjectRoot))
	case c.legacyHugoRoot != "":
		c.Warnings = append(c.Warnings, fmt.Sprintf(
			"config key \"hugo_root\" is deprecated and was ignored: --project-root / "+
				"\"project_root\" is also set and wins. The project root is %q; "+
				"remove \"hugo_root\" (it points at %q).", c.ProjectRoot, c.legacyHugoRoot))
	}
	warn := func(profileName, marker, path string, err error) {
		c.Warnings = append(c.Warnings, fmt.Sprintf(
			"candidate root marker %q (profile %q) at %s could not be read (%v); "+
				"that marker was skipped, so it could not contribute to profile detection",
			marker, profileName, path, err))
	}

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
	c.RootFromUser = c.rootSource != ""

	// A root the user named is a promise about the filesystem; check it before
	// anything relies on it. Detection never produces a missing root (it only
	// ever returns a directory it just stat'ed), so this cannot fire for an
	// auto-detected one. Failing here beats resolving nothing and reporting
	// every include "unknown" with no diagnostic at all (#7).
	if c.RootFromUser {
		if err := validateExplicitRoot(c.ProjectRoot, c.rootSource); err != nil {
			return err
		}
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
		root = c.ProjectRoot
		if root == "" && len(p.RootMarkers) > 0 && c.ContentDir != "" {
			root = p.DetectRoot(c.ContentDir, warn)
		}
	} else {
		var detected bool
		p, root, detected = detectProfile(c.ContentDir, warn)
		// Legacy compatibility, and nothing more. Before the root became
		// profile-neutral, a config carrying "hugo_root" and no "profile"
		// always got the hugo profile, because that key was how you said "this
		// is a Hugo site". A real Hugo site still auto-detects from its own
		// markers, so the only configs that would regress are those whose
		// hugo_root points at a tree with no Hugo marker at all — keep them
		// working by selecting hugo there.
		//
		// Deliberately not extended to "project_root" / --project-root: the
		// current spelling says where to resolve from, never what the project
		// is, so on a Mintlify tree it must leave detection alone (#7).
		if !detected && c.rootSource == rootSourceLegacyHugoRoot {
			p, root = mustProfile(ProfileHugo), c.ProjectRoot
		}
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
	if c.ProjectRoot == "" && len(p.RootMarkers) > 0 {
		c.ProjectRoot = root
	}
	return nil
}

// Root-source labels: where the project root in force came from, as the
// diagnostics name it. The legacy label still contains the bare "hugo_root"
// spelling so an error about a bad root names the key the user actually wrote.
const (
	rootSourceProjectRoot    = `--project-root / "project_root"`
	rootSourceLegacyHugoRoot = `the deprecated "hugo_root" key`
)

// foldLegacyRoot resolves the two spellings of the project root into
// Config.ProjectRoot and records which one supplied it in Config.rootSource.
// "project_root" / --project-root wins when both are set: a user who writes
// the current key means it.
//
// It is idempotent — ApplyProfile may run twice (the analyzer calls it for a
// Config built directly) and the second run must not mistake the value it
// folded in for one the user wrote under the current spelling, which would
// lose both the deprecation warning and the legacy hugo fallback.
func (c *Config) foldLegacyRoot() {
	if c.rootSource != "" {
		return
	}
	switch {
	case c.ProjectRoot != "":
		c.rootSource = rootSourceProjectRoot
	case c.legacyHugoRoot != "":
		c.ProjectRoot = c.legacyHugoRoot
		c.rootSource = rootSourceLegacyHugoRoot
	}
}

// validateExplicitRoot checks a project root the user supplied. source names
// where it came from (rootSourceProjectRoot or rootSourceLegacyHugoRoot) so
// the error says which knob to fix.
//
// os.Stat (not Lstat) is used deliberately: a symlink pointing at a real
// directory is a perfectly good project root, and only a dangling one — which
// stats as "does not exist" — is a mistake.
func validateExplicitRoot(root, source string) error {
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("project root %q (from %s) does not exist", root, source)
		}
		return fmt.Errorf("project root %q (from %s) cannot be read: %w", root, source, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("project root %q (from %s) is not a directory", root, source)
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

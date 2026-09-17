package parser

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// importStatementPattern captures an ES-module import at the top of an MDX
// page: group 1 is the import clause (everything between "import" and "from"),
// group 2 the module path.
//
// The clause excludes ";", "<" and ">" so a malformed or unterminated import
// cannot run away across the page and swallow the JSX below it, but it does
// allow newlines, because a named import listing several symbols is routinely
// written over several lines. Side-effect imports (`import "./x.css"`) have no
// "from" and no bindings, so they correctly do not match.
var importStatementPattern = regexp.MustCompile(
	`(?m)^[ \t]*import[ \t\r\n]+([^;<>]*?)[ \t\r\n]+from[ \t\r\n]*["']([^"']+)["']`)

// identifierPattern matches a JavaScript binding name, so that junk between the
// commas of a malformed import never becomes a symbol.
var identifierPattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// importContentExtensions are the imports the map follows: documentation, whose
// commit date legitimately belongs to the section that includes it.
//
// Everything else — .jsx, .js, .css, and any extension not listed — is detected
// and deliberately skipped. This is the load-bearing rule of #68, and it is an
// allowlist rather than a list of skips so a new asset kind is out of scope by
// default rather than by omission. The reason is asymmetry: freshness folding
// only ever makes a section look *fresher*. On the measured corpus one
// component, yaml-table.jsx, was imported by 650 pages; resolving it would mark
// every one of them recently updated the next time someone changed its
// styling, destroying the signal exactly where the tool is supposed to provide
// it. Under-reporting freshness is the safe failure direction.
var importContentExtensions = map[string]struct{}{
	".md":  {},
	".mdx": {},
}

// importTarget is what one import resolved to.
//
// The three states are distinct and all three matter. A resolved path folds its
// history into the section. skipped means the import names something that is
// not documentation: out of scope by design, and explicitly *not* an unresolved
// include, because reporting 1,115 React-component imports as broken would be
// as bad as resolving them. An empty path with skipped false is a content
// import that resolved to no file — a genuine defect, reported unknown.
type importTarget struct {
	path    string
	skipped bool
	// named is the file the import *pointed at*, as an absolute path derived
	// from the module specifier, whether or not anything is there. It is set
	// only for a content import that resolved to nothing, and exists for
	// labelling: without it an unresolved import is reported under its bare
	// symbol ("Card"), which is exactly the collapsing DisplayName exists to
	// prevent — two pages importing different files as "Card" would share one
	// row (#68 review).
	named string
}

// isImportableContent reports whether an import path names documentation the
// map should follow. An extensionless path is treated as content: it is what a
// bare "./intro" import means in an MDX tree, and resolveDirectPath will try
// the reusable extensions against it.
func isImportableContent(ref string) bool {
	base := ref
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		base = ref[i+1:]
	}
	ext := strings.ToLower(filepath.Ext(base))
	if ext == "" {
		return true
	}
	_, ok := importContentExtensions[ext]
	return ok
}

// isBareModuleSpecifier reports whether an import path names a package rather
// than a file in the project: in ES module resolution, anything that is not
// root-absolute ("/…") or explicitly relative ("./…", "../…") is a package
// specifier — "react", "@mintlify/components", "@astrojs/starlight/components".
//
// Such an import is skipped, never reported unresolved. It is not a file in the
// docs tree, so failing to find one is not a defect; and it is the form that
// the profiles waiting on this layer lean on hardest, since a Starlight or
// Docusaurus page imports its components from a package on nearly every page.
// Without this rule those imports would be extensionless, hence look like
// content, and every one of them would be counted as a broken include.
func isBareModuleSpecifier(ref string) bool {
	ref = strings.TrimSpace(ref)
	return ref != "" &&
		!strings.HasPrefix(ref, "/") &&
		!strings.HasPrefix(ref, "./") &&
		!strings.HasPrefix(ref, "../")
}

// importsFor returns the symbol -> target map for one page, building it on
// first use and memoizing it.
//
// It is keyed by source file rather than built once because a ReusablePatterns
// is created per analyzed file (see analyzer.analyzeFile) but is also asked
// about the pages that page's own includes live in. The map is deliberately
// built from the *whole* file: imports sit at the top of a page while sections
// are chunked separately, so a section-scoped scan would see the usage and
// never the import.
//
// No locking: a ReusablePatterns belongs to one worker for the duration of one
// file, exactly as filePaths and shortcodeCache already assume.
func (rp *ReusablePatterns) importsFor(sourceFile string) map[string]importTarget {
	if rp.importCache == nil {
		rp.importCache = make(map[string]map[string]importTarget, 1)
	}
	if m, ok := rp.importCache[sourceFile]; ok {
		return m
	}
	m := rp.buildImportMap(sourceFile)
	rp.importCache[sourceFile] = m
	return m
}

// buildImportMap reads sourceFile and resolves every import in it. A file that
// cannot be read yields an empty map: nothing is imported, so every component
// on the page is out of scope, which is the same answer as a page with no
// imports.
//
// The first import of a symbol wins, mirroring how the analyzer keys reusables
// by their first occurrence; a page that binds the same name twice is invalid
// JavaScript anyway.
func (rp *ReusablePatterns) buildImportMap(sourceFile string) map[string]importTarget {
	out := make(map[string]importTarget)
	if sourceFile == "" {
		return out
	}
	data, err := os.ReadFile(filepath.Clean(sourceFile))
	if err != nil {
		return out
	}
	src := string(data)
	fences := fencedSpans(src)
	for _, m := range importStatementPattern.FindAllStringSubmatchIndex(src, -1) {
		if inSpans(fences, m[0]) {
			continue
		}
		target := rp.resolveImportPath(src[m[4]:m[5]], sourceFile)
		for _, sym := range importedSymbols(src[m[2]:m[3]]) {
			if _, exists := out[sym]; !exists {
				out[sym] = target
			}
		}
	}
	return out
}

// fenceLinePattern matches a fenced code-block delimiter: CommonMark allows up
// to three spaces of indent, then three or more backticks or tildes, then an
// optional info string ("```mdx").
var fenceLinePattern = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")

// fencedSpans returns the byte ranges of content that sit inside a fenced code
// block, fence lines included, in ascending order.
//
// Why the import scanner cares: an import shown *as an example* inside a fence
// is documentation about an import, not one. Following it folds the imported
// file's commit date into the section, which makes the page look FRESHER than
// it is — the one direction #68 is built never to fail in — and, because the
// first import of a symbol wins, a decoy in a fence also shadows the real
// import below it.
//
// Scope: this is the import scanner's blind spot only. The header regex has the
// same one (a "#" line inside a fence read as a heading) and it is NOT fixed
// here — that is #27, whose answer is to replace the regex with a real
// CommonMark parser rather than to grow a second ad-hoc fence tracker.
//
// The rules implemented are CommonMark's, minus what cannot matter to an import
// on its own line: an opening fence is three or more backticks or tildes with
// at most three spaces of indent; a backtick fence's info string may not itself
// contain a backtick; a closing fence is the same character, at least as long,
// and carries no info string; and a fence that is never closed runs to the end
// of the document.
func fencedSpans(content string) [][2]int {
	var (
		spans     [][2]int
		open      bool
		fenceChar byte
		fenceLen  int
		spanStart int
	)
	for pos := 0; pos < len(content); {
		lineEnd, next := len(content), len(content)
		if nl := strings.IndexByte(content[pos:], '\n'); nl >= 0 {
			lineEnd = pos + nl
			next = lineEnd + 1
		}
		if m := fenceLinePattern.FindStringSubmatch(content[pos:lineEnd]); m != nil {
			char, length := m[1][0], len(m[1])
			info := strings.TrimSpace(m[2])
			switch {
			case open:
				if char == fenceChar && length >= fenceLen && info == "" {
					spans = append(spans, [2]int{spanStart, next})
					open = false
				}
			case char == '`' && strings.Contains(info, "`"):
				// Not an opening fence: "`` `x` ``" and friends.
			default:
				open, fenceChar, fenceLen, spanStart = true, char, length, pos
			}
		}
		pos = next
	}
	if open {
		spans = append(spans, [2]int{spanStart, len(content)})
	}
	return spans
}

// inSpans reports whether offset falls inside any of the (ascending, disjoint)
// ranges.
func inSpans(spans [][2]int, offset int) bool {
	for _, s := range spans {
		if offset < s[0] {
			return false
		}
		if offset < s[1] {
			return true
		}
	}
	return false
}

// resolveImportPath turns one module path into a target. A leading "/" is
// project-root relative and a "./" or "../" path is relative to the importing
// page, which is exactly what resolveDirectPath already implements — including
// the containment check that keeps an import from reaching outside the project
// root. That reuse is the point: the import map is a symbol-to-path layer over
// the existing resolver, not a second resolver.
func (rp *ReusablePatterns) resolveImportPath(ref, sourceFile string) importTarget {
	if isBareModuleSpecifier(ref) || !isImportableContent(ref) {
		return importTarget{skipped: true}
	}
	resolved, ok := rp.resolveDirectPath(ref, sourceFile)
	if !ok {
		return importTarget{named: rp.namedImportPath(ref, sourceFile)}
	}
	return importTarget{path: resolved}
}

// namedImportPath returns the absolute path an unresolved content import names,
// for labelling only — nothing is stat'ed, because the whole point is that the
// file is not there. The two spellings resolveDirectPath honours for an import
// are honoured here too: a leading "/" is project-root relative, and "./" or
// "../" is relative to the importing page. Anything the caller could not place
// (no root, or a relative import with no known page) yields "", and the report
// falls back to the raw capture.
//
// Escapes are not filtered here: DisplayName already rejects a path that lands
// outside the project root, and it is the only caller.
func (rp *ReusablePatterns) namedImportPath(ref, sourceFile string) string {
	ref = strings.TrimSpace(ref)
	if rootRel, ok := strings.CutPrefix(ref, "/"); ok {
		if root := rp.resolvedRootPath(); root != "" && rootRel != "" {
			return filepath.Join(root, filepath.FromSlash(rootRel))
		}
		return ""
	}
	if ref == "" || sourceFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(sourceFile), filepath.FromSlash(ref))
}

// importedSymbols returns the local names an import clause binds: the default
// binding, each named specifier, and a namespace alias. "as" renames are
// resolved to the local name, because that is what the page writes as a tag.
//
//	Button                  -> [Button]
//	{ A, B as C }           -> [A, C]
//	Default, { A }          -> [A, Default]
//	* as NS                 -> [NS]
func importedSymbols(clause string) []string {
	var syms []string
	rest := clause
	if i := strings.Index(clause, "{"); i >= 0 {
		if j := strings.LastIndex(clause, "}"); j > i {
			for _, spec := range strings.Split(clause[i+1:j], ",") {
				if s := localBinding(spec); s != "" {
					syms = append(syms, s)
				}
			}
			rest = clause[:i] + clause[j+1:]
		}
	}
	for _, spec := range strings.Split(rest, ",") {
		if s := localBinding(spec); s != "" {
			syms = append(syms, s)
		}
	}
	return syms
}

// localBinding returns the name one specifier introduces into the page's scope,
// or "" when the specifier is not a shape this understands. Anything
// unrecognised is dropped rather than guessed at: a wrong symbol would silently
// attribute one file's freshness to another.
func localBinding(spec string) string {
	fields := strings.Fields(strings.TrimSpace(spec))
	switch len(fields) {
	case 1:
		// "Button", or a bare "*" with no alias, which binds nothing usable.
	case 3:
		// "A as B", "* as NS".
		if fields[1] != "as" {
			return ""
		}
	default:
		return ""
	}
	name := fields[len(fields)-1]
	if !identifierPattern.MatchString(name) {
		return ""
	}
	return name
}

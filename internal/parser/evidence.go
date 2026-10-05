package parser

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/nrynss/rustydocs/internal/git"
)

// Dependency is one rendered reference occurrence, with every supporting file
// visited by the existing resolver. Locations are one-based byte columns.
type Dependency struct {
	Reference    string
	Line, Column int
	Status       string
	Files        []DependencyFile
	Issues       []SupportIssue
}

// DependencyFile retains one supporting file, its Git evidence and working-tree hash.
type DependencyFile struct {
	Path        string
	Info        *git.FileInfo
	Fingerprint string
}

// Fingerprint normalizes CRLF to LF, removes leading/trailing blank lines and terminal
// newlines, then hashes UTF-8 bytes with SHA-256. Internal whitespace is retained.
func Fingerprint(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

var sideEffectImport = regexp.MustCompile(`(?m)^[ \t]*import[ \t]+["'][^"'\n]+["']`)

// Mask complete import declarations only; preserve line positions and rendered
// content following them. Fenced examples are documentation, not imports.
func maskImports(content string) (string, map[int]bool) {
	ignored := map[int]bool{}
	masked := []byte(content)
	fences := fencedSpans(content)
	spans := importStatementPattern.FindAllStringIndex(content, -1)
	spans = append(spans, sideEffectImport.FindAllStringIndex(content, -1)...)
	for _, span := range spans {
		if inSpans(fences, span[0]) {
			continue
		}
		end := span[1]
		for end < len(content) && (content[end] == ' ' || content[end] == '\t' || content[end] == ';' || content[end] == '\r') {
			end++
		}
		// Ambiguous syntax stays visible rather than swallowing JSX/prose.
		if end < len(content) && content[end] != '\n' {
			continue
		}
		line := 1 + strings.Count(content[:span[0]], "\n")
		for i := span[0]; i < end; i++ {
			ignored[line] = true
			if content[i] == '\n' {
				line++
			} else {
				masked[i] = ' '
			}
		}
	}
	return string(masked), ignored
}

// enrichChunks attaches heading identities, own-content hashes and located references.
func enrichChunks(chunks []Chunk, original string, rp *ReusablePatterns) {
	lines := strings.Split(original, "\n")
	own := original
	if rp != nil && rp.importMap {
		own, _ = maskImports(original)
	}
	ownLines := strings.Split(own, "\n")
	referenceContent := own
	if rp != nil && (rp.capabilities.MaskFencedChunking || rp.capabilities.SkipFencedCaptures) {
		masked := []byte(referenceContent)
		for _, span := range fencedSpans(referenceContent) {
			for i := span[0]; i < span[1]; i++ {
				if masked[i] != '\n' {
					masked[i] = ' '
				}
			}
		}
		referenceContent = string(masked)
	}
	referenceLines := strings.Split(referenceContent, "\n")
	type heading struct {
		level int
		title string
	}
	var stack []heading
	occurrences := map[string]int{}
	for i := range chunks {
		c := &chunks[i]
		if c.IsHeader && c.Level > 0 {
			for len(stack) > 0 && stack[len(stack)-1].level >= c.Level {
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, heading{c.Level, c.Title})
		}
		c.HeadingPath = make([]string, 0, len(stack))
		for _, h := range stack {
			c.HeadingPath = append(c.HeadingPath, h.title)
		}
		key, _ := json.Marshal(c.HeadingPath)
		kind := "section"
		if !c.IsHeader {
			kind = "paragraph"
			if len(stack) == 0 {
				kind = "preamble"
			}
		}
		base := string(key) + ":" + kind
		occurrences[base]++
		c.LogicalKey = base + ":" + strconv.Itoa(occurrences[base])
		start, end := max(0, c.StartLine-1), min(len(lines), c.EndLine)
		if start > end {
			continue
		}
		c.Fingerprint = Fingerprint(strings.Join(ownLines[start:end], "\n"))
		c.Dependencies = FindDependencies(strings.Join(referenceLines[start:end], "\n"), c.StartLine, rp)
		// Retain preexisting capture semantics while chunking always masks fences.
		c.Reusables = FindReusables(strings.Join(referenceLines[start:end], "\n"), rp)
	}
}

// FindDependencies locates rendered reference captures while retaining their original spelling.
func FindDependencies(content string, startLine int, rp *ReusablePatterns) []Dependency {
	out := []Dependency{}
	if rp == nil {
		rp = DefaultReusablePatterns()
	}
	var fences [][2]int
	if rp.capabilities.SkipFencedCaptures {
		fences = fencedSpans(content)
	}
	seen := map[string]bool{}
	for i, pattern := range rp.patterns {
		for _, m := range pattern.FindAllStringSubmatchIndex(content, -1) {
			if len(m) < 4 || m[2] < 0 || inSpans(fences, m[0]) {
				continue
			}
			ref := content[m[2]:m[3]]
			if rp.capabilities.SkipURLCaptures && strings.Contains(ref, "://") {
				continue
			}
			if !rp.isComponentPattern(i) {
				rp.noteIncludeCapture(ref)
			}
			key := strconv.Itoa(m[2]) + ":" + ref
			if seen[key] {
				continue
			}
			seen[key] = true
			before := content[:m[2]]
			col := m[2] - strings.LastIndex(before, "\n")
			out = append(out, Dependency{Reference: ref, Line: startLine + strings.Count(before, "\n"), Column: col, Files: []DependencyFile{}})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Column < out[j].Column
	})
	return out
}

// ResolveDependency uses precisely the existing resolution/freshness strategy.
func ResolveDependency(d Dependency, source string, rp *ReusablePatterns) Dependency {
	if rp == nil {
		d.Status = "unresolved"
		return d
	}
	rp.traceFiles = nil
	rp.traceRoots = map[string]string{}
	rp.supportIssues = nil
	d.Files = nil
	d.Issues = nil
	rp.collectTrace = true
	defer func() { rp.collectTrace = false }()
	_, status := ResolveReusable(d.Reference, source, rp)
	d.Status = map[Resolution]string{ResolutionResolved: "resolved", ResolutionUnresolved: "unresolved", ResolutionSkipped: "skipped"}[status]

	sort.Strings(rp.traceFiles)
	previous := ""
	for _, path := range rp.traceFiles {
		if path == previous {
			continue
		}
		previous = path
		// Reauthorize at the fingerprint boundary and read the physical target.
		target, status := supportingTarget(path, rp.traceRoots[path])
		if status != "" {
			rp.noteSupportIssue("reusable_support_"+status, "supporting file")
			continue
		}
		data, err := os.ReadFile(target)
		if err != nil {
			rp.noteSupportIssue("reusable_support_missing", "supporting file")
			continue
		}
		fi, _ := rp.cache.FileLastModified(target)
		d.Files = append(d.Files, DependencyFile{Path: path, Info: fi, Fingerprint: Fingerprint(string(data))})
	}
	d.Issues = append([]SupportIssue{}, rp.supportIssues...)
	if len(d.Issues) > 0 && d.Status == "resolved" {
		d.Status = "partial"
	}
	return d
}

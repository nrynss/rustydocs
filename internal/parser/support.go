package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nrynss/rustydocs/internal/git"
)

// SupportIssue qualifies a rendered dependency whose supporting evidence is
// rejected or unavailable. Reference is source spelling, never a discovered path.
type SupportIssue struct {
	Code      string
	Reference string
}

// isAbsoluteReference recognizes absolute path spellings independently of the
// host OS. Support references are authored in site files and may use Windows
// syntax even when rustydocs runs on Unix.
func isAbsoluteReference(reference string) bool {
	if filepath.IsAbs(reference) || strings.HasPrefix(reference, "\\") || strings.HasPrefix(reference, "//") {
		return true
	}
	if len(reference) >= 3 &&
		((reference[0] >= 'a' && reference[0] <= 'z') || (reference[0] >= 'A' && reference[0] <= 'Z')) &&
		reference[1] == ':' && (reference[2] == '/' || reference[2] == '\\') {
		return true
	}
	return false
}

// supportingTarget authorizes a physical target against one selected scope.
// Hugo template-derived reads use the project root; legacy lookups use only
// their explicitly configured directory. Git membership grants no permission.
func supportingTarget(path, root string) (string, string) {
	if root == "" {
		return "", "rejected"
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "missing"
	}
	physicalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", "missing"
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "missing"
	}
	target, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if !relWithin(absRoot, abs) && !relWithin(physicalRoot, abs) {
			return "", "rejected"
		}
		return "", "missing"
	}
	if !relWithin(physicalRoot, target) {
		return "", "rejected"
	}
	return target, ""
}

// noteSupportIssue preserves incomplete evidence without leaking absolute paths.
func (rp *ReusablePatterns) noteSupportIssue(code, reference string) {
	prefix := ""
	for _, kind := range []string{"partial ", "shortcode ", "theme "} {
		if strings.HasPrefix(reference, kind) {
			prefix = kind
			reference = strings.TrimPrefix(reference, kind)
			break
		}
	}
	if isAbsoluteReference(reference) {
		reference = "<absolute reference>"
	}
	reference = prefix + filepath.ToSlash(reference)
	if isAbsoluteReference(reference) {
		reference = "<absolute reference>"
	}
	issue := SupportIssue{Code: code, Reference: reference}
	for _, existing := range rp.supportIssues {
		if existing == issue {
			return
		}
	}
	rp.supportIssues = append(rp.supportIssues, issue)
}

// supportingFile bounds concrete template references before content or Git reads.
func (rp *ReusablePatterns) supportingFile(path, root, reference string, reportMissing bool) (string, bool) {
	target, status := supportingTarget(path, root)
	if status != "" {
		if status == "rejected" || reportMissing {
			rp.noteSupportIssue("reusable_support_"+status, reference)
		}
		return "", false
	}
	st, err := os.Stat(target)
	if err != nil || st.IsDir() || !rp.caseExactUnder(root, path) {
		if reportMissing {
			rp.noteSupportIssue("reusable_support_missing", reference)
		}
		return "", false
	}
	return target, true
}

// tracedFileInfoUnder records only authorized physical supporting targets.
func (rp *ReusablePatterns) tracedFileInfoUnder(path, root string) (*git.FileInfo, error) {
	target, ok := rp.supportingFile(path, root, "supporting file", false)
	if !ok {
		return nil, fmt.Errorf("supporting file unavailable or outside authorized root")
	}
	if rp.collectTrace {
		rp.traceFiles = append(rp.traceFiles, target)
		if rp.traceRoots == nil {
			rp.traceRoots = make(map[string]string)
		}
		rp.traceRoots[target] = root
	}
	return rp.cache.FileLastModified(target)
}

// mostRecentFileUnder collects history within the caller's single resolver scope.
func (rp *ReusablePatterns) mostRecentFileUnder(paths []string, root string) *git.FileInfo {
	var recent *git.FileInfo
	for _, path := range paths {
		info, err := rp.tracedFileInfoUnder(path, root)
		if err == nil && info != nil && (recent == nil || info.LastModified.After(recent.LastModified)) {
			recent = info
		}
	}
	return recent
}

package config

import (
	"sort"
	"strings"
)

// defaultExcludeDirNames are directory names skipped by default: build and
// dependency trees that are never documentation, and that a docs repo
// nonetheless carries in bulk. Dot-directories are handled separately (see
// IsDefaultExcludedDir) because there is an open-ended supply of them — .git,
// .claude, .cursor, .github, .venv — and listing them one by one would mean
// chasing every new tool.
//
// Measured on a production Mintlify site, the walk without these scanned 4,032
// files in 48.5s and reported 1,078 as having no git history; 494 of those
// files were documentation (#69).
var defaultExcludeDirNames = []string{
	"build",
	"dist",
	"node_modules",
	"vendor",
}

// DefaultExcludeDirNames returns the non-dot directory names excluded by
// default, sorted. It is the list the CLI's diagnostics and the README speak
// about; the predicate that actually decides is IsDefaultExcludedDir.
func DefaultExcludeDirNames() []string {
	out := append([]string(nil), defaultExcludeDirNames...)
	sort.Strings(out)
	return out
}

// IsDefaultExcludedDir reports whether a directory of this base name is pruned
// from the content walk by the default exclusions: any dot-directory, or one of
// DefaultExcludeDirNames.
//
// "." and ".." are deliberately not excluded — they name the walk's own
// starting point rather than a hidden directory — and neither is a bare empty
// name.
func IsDefaultExcludedDir(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	for _, d := range defaultExcludeDirNames {
		if name == d {
			return true
		}
	}
	return false
}

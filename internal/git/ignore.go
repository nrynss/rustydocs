package git

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// CheckIgnore returns the subset of paths that git ignores, as a set keyed by
// exactly the strings that were passed in. paths are interpreted relative to
// dir (or absolute); dir must be inside the repository to consult.
//
// Why shell out rather than match .gitignore patterns in-process: git is
// already a hard dependency of rustydocs — without it there is no blame and no
// analysis at all — so `git check-ignore` costs one extra subprocess for the
// whole run, and gets exactly right a great deal that a hand-rolled matcher
// gets subtly wrong: nested .gitignore files at every level, negation rules and
// their ordering, directory-only patterns, `**` semantics, core.excludesFile,
// $GIT_DIR/info/exclude, and the precedence between all of them. A partial
// reimplementation would silently analyze (or silently drop) the files it
// disagreed with git about, which is the worst failure mode available here. The
// standard-library-only rule concerns Go dependencies, and this adds none.
//
// The index is deliberately consulted (no --no-index): a file that is tracked
// is not ignored however the patterns read, and it is exactly the tracked files
// that have the blame history rustydocs analyzes.
//
// Errors are the caller's cue to skip ignore filtering entirely rather than to
// fail: a tree that is not a git repository is a supported case (its files are
// reported "unknown"), and there is nothing to ignore there.
func CheckIgnore(dir string, paths []string) (map[string]bool, error) {
	ignored := make(map[string]bool)
	if len(paths) == 0 {
		return ignored, nil
	}

	// --stdin with -z takes NUL-separated pathnames and echoes back, also
	// NUL-separated, exactly those that are ignored — in the spelling they were
	// given, which is what lets the caller match results to its own paths.
	cmd := exec.Command("git", "check-ignore", "--stdin", "-z")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Exit status 1 means "none of the given paths are ignored", which is a
		// perfectly good answer; anything else (128: not a repository, git
		// missing, an unreadable exclude file) is a real failure.
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, err
		}
	}

	for _, p := range strings.Split(stdout.String(), "\x00") {
		if p != "" {
			ignored[p] = true
		}
	}
	return ignored, nil
}

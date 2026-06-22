// Package git provides git utilities for analyzing file history.
package git

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	cachedGitRoot    string
	cachedGitRootErr error
	gitRootOnce      sync.Once
)

// LineInfo contains information about a single line from git blame.
type LineInfo struct {
	LineNumber  int
	Author      string
	AuthorEmail string
	Timestamp   time.Time
	CommitHash  string
	Content     string
}

// FileInfo contains information about a file from git log.
type FileInfo struct {
	Path         string
	LastModified time.Time
	LastAuthor   string
	LastCommit   string
}

// GetFileLastModified returns the last modification info for a file using git log.
func GetFileLastModified(filePath string) (*FileInfo, error) {
	// Get git root from the file's directory (not the current working directory)
	// This is important when analyzing files from different repositories
	gitRoot, err := GetGitRootForPath(filePath)
	if err != nil {
		// Fall back to file's directory
		gitRoot = filepath.Dir(filePath)
	}

	// Make path relative to git root if absolute
	relPath := filePath
	if filepath.IsAbs(filePath) && gitRoot != "" {
		if rel, err := filepath.Rel(gitRoot, filePath); err == nil {
			relPath = rel
		}
	}

	cmd := exec.Command("git", "log", "-1", "--format=%H%n%an%n%ai", "--", relPath)
	cmd.Dir = gitRoot

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 3 {
		return nil, nil
	}

	// Parse date like "2024-01-15 10:30:45 -0800"
	timestamp, err := time.Parse("2006-01-02 15:04:05 -0700", lines[2])
	if err != nil {
		// Try without timezone, but ensure string is long enough
		if len(lines[2]) < 19 {
			return nil, fmt.Errorf("invalid timestamp format: %q", lines[2])
		}
		timestamp, err = time.Parse("2006-01-02 15:04:05", lines[2][:19])
		if err != nil {
			return nil, err
		}
	}

	return &FileInfo{
		Path:         filePath,
		LastModified: timestamp,
		LastAuthor:   lines[1],
		LastCommit:   lines[0],
	}, nil
}

// GetBlameInfo returns per-line blame information for a file.
// Uses streaming to handle large files without loading entire output into memory.
func GetBlameInfo(filePath string) ([]LineInfo, error) {
	cmd := exec.Command("git", "blame", "--line-porcelain", filePath)
	cmd.Dir = filepath.Dir(filePath)

	// Capture stderr for better error messages
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start git blame: %w", err)
	}

	// Parse output as it streams
	linesInfo, parseErr := parseBlameFromReader(stdout)

	// Wait for command to complete
	if err := cmd.Wait(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return nil, fmt.Errorf("git blame failed: %s", errMsg)
		}
		return nil, fmt.Errorf("git blame failed: %w", err)
	}

	return linesInfo, parseErr
}

// parseBlameFromReader parses git blame output from an io.Reader (streaming).
func parseBlameFromReader(r interface{ Read([]byte) (int, error) }) ([]LineInfo, error) {
	var linesInfo []LineInfo
	scanner := bufio.NewScanner(r)
	// Raise the token limit above bufio's 64KB default so a single long line
	// (data URIs, long tables, minified blobs) doesn't make Scan() stop early
	// with bufio.ErrTooLong and drop the entire file's blame.
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	currentLine := make(map[string]string)
	lineNumber := 0

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "\t") {
			// This is the actual content line
			content := line[1:] // Remove leading tab
			lineNumber++

			if authorTime, ok := currentLine["author-time"]; ok {
				ts, err := strconv.ParseInt(authorTime, 10, 64)
				if err == nil {
					linesInfo = append(linesInfo, LineInfo{
						LineNumber:  lineNumber,
						Author:      currentLine["author"],
						AuthorEmail: currentLine["author-mail"],
						Timestamp:   time.Unix(ts, 0),
						CommitHash:  currentLine["commit"],
						Content:     content,
					})
				}
			}
			currentLine = make(map[string]string)
		} else if line != "" {
			// A blame entry's header line is "<sha> <orig> <final> [group]";
			// its first field is the commit hash. Every other porcelain line is
			// a "key value" pair (author, author-time, summary, filename, ...).
			parts := strings.SplitN(line, " ", 2)
			if isCommitHash(parts[0]) {
				currentLine["commit"] = parts[0]
			} else if len(parts) == 2 {
				currentLine[parts[0]] = parts[1]
			}
		}
	}

	return linesInfo, scanner.Err()
}

// parseBlameOutput parses a complete git blame output buffer. Thin wrapper over
// the streaming parser so the parsing logic lives in exactly one place.
func parseBlameOutput(output []byte) ([]LineInfo, error) {
	return parseBlameFromReader(bytes.NewReader(output))
}

// isCommitHash reports whether s is a git object name (40-hex SHA-1 or 64-hex
// SHA-256). Used to recognize the header line of a blame porcelain entry, whose
// first field is the commit hash followed by line numbers.
func isCommitHash(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// GetGitRoot returns the root directory of the git repository (cached).
// Note: This uses the current working directory, which may not be correct
// for files in different repositories. Use GetGitRootForPath for files.
func GetGitRoot() (string, error) {
	gitRootOnce.Do(func() {
		cmd := exec.Command("git", "rev-parse", "--show-toplevel")
		output, err := cmd.Output()
		if err != nil {
			cachedGitRootErr = err
			return
		}
		cachedGitRoot = strings.TrimSpace(string(output))
	})
	return cachedGitRoot, cachedGitRootErr
}

// gitRootCache caches git roots per directory to avoid repeated git calls.
var (
	gitRootCache   = make(map[string]string)
	gitRootCacheMu sync.RWMutex
)

// GetGitRootForPath returns the git root for a specific file path.
// This is important when the file might be in a different repository
// than the current working directory.
func GetGitRootForPath(filePath string) (string, error) {
	dir := filepath.Dir(filePath)
	if !filepath.IsAbs(dir) {
		absDir, err := filepath.Abs(dir)
		if err == nil {
			dir = absDir
		}
	}

	// Check cache first
	gitRootCacheMu.RLock()
	if root, ok := gitRootCache[dir]; ok {
		gitRootCacheMu.RUnlock()
		return root, nil
	}
	gitRootCacheMu.RUnlock()

	// Run git rev-parse from the file's directory
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	root := strings.TrimSpace(string(output))

	// Cache the result
	gitRootCacheMu.Lock()
	gitRootCache[dir] = root
	gitRootCacheMu.Unlock()

	return root, nil
}

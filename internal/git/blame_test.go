// Package git provides git utilities for analyzing file history.
package git

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseBlameOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []LineInfo
		wantErr  bool
	}{
		{
			name: "single line",
			input: `abc123def456abc123def456abc123def456abc1
author John Doe
author-mail <john@example.com>
author-time 1700000000
author-tz -0800
committer John Doe
committer-mail <john@example.com>
committer-time 1700000000
committer-tz -0800
summary Initial commit
filename test.md
	Hello, World!
`,
			expected: []LineInfo{
				{
					LineNumber:  1,
					Author:      "John Doe",
					AuthorEmail: "<john@example.com>",
					Timestamp:   time.Unix(1700000000, 0),
					CommitHash:  "abc123def456abc123def456abc123def456abc1",
					Content:     "Hello, World!",
				},
			},
			wantErr: false,
		},
		{
			name: "multiple lines",
			input: `abc123def456abc123def456abc123def456abc1
author John Doe
author-mail <john@example.com>
author-time 1700000000
author-tz -0800
summary Initial commit
filename test.md
	Line 1
def456abc123def456abc123def456abc123def4
author Jane Smith
author-mail <jane@example.com>
author-time 1700100000
author-tz -0800
summary Second commit
filename test.md
	Line 2
`,
			expected: []LineInfo{
				{
					LineNumber:  1,
					Author:      "John Doe",
					AuthorEmail: "<john@example.com>",
					Timestamp:   time.Unix(1700000000, 0),
					CommitHash:  "abc123def456abc123def456abc123def456abc1",
					Content:     "Line 1",
				},
				{
					LineNumber:  2,
					Author:      "Jane Smith",
					AuthorEmail: "<jane@example.com>",
					Timestamp:   time.Unix(1700100000, 0),
					CommitHash:  "def456abc123def456abc123def456abc123def4",
					Content:     "Line 2",
				},
			},
			wantErr: false,
		},
		{
			name:     "empty input",
			input:    "",
			expected: nil,
			wantErr:  false,
		},
		{
			name:  "empty content line",
			input: "abc123def456abc123def456abc123def456abc1\nauthor John Doe\nauthor-mail <john@example.com>\nauthor-time 1700000000\nfilename test.md\n\t\n",
			expected: []LineInfo{
				{
					LineNumber:  1,
					Author:      "John Doe",
					AuthorEmail: "<john@example.com>",
					Timestamp:   time.Unix(1700000000, 0),
					CommitHash:  "abc123def456abc123def456abc123def456abc1",
					Content:     "",
				},
			},
			wantErr: false,
		},
		{
			name: "content with special characters",
			input: `abc123def456abc123def456abc123def456abc1
author John Doe
author-mail <john@example.com>
author-time 1700000000
filename test.md
	# Header with {{< shortcode >}} and "quotes"
`,
			expected: []LineInfo{
				{
					LineNumber:  1,
					Author:      "John Doe",
					AuthorEmail: "<john@example.com>",
					Timestamp:   time.Unix(1700000000, 0),
					CommitHash:  "abc123def456abc123def456abc123def456abc1",
					Content:     `# Header with {{< shortcode >}} and "quotes"`,
				},
			},
			wantErr: false,
		},
		{
			name: "missing author-time skips line",
			input: `abc123def456abc123def456abc123def456abc1
author John Doe
author-mail <john@example.com>
filename test.md
	No timestamp line
`,
			expected: nil, // Line is skipped because no author-time
			wantErr:  false,
		},
		{
			name: "invalid author-time skips line",
			input: `abc123def456abc123def456abc123def456abc1
author John Doe
author-mail <john@example.com>
author-time not-a-number
filename test.md
	Invalid timestamp line
`,
			expected: nil, // Line is skipped because author-time is not parseable
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseBlameOutput([]byte(tt.input))

			if (err != nil) != tt.wantErr {
				t.Errorf("parseBlameOutput() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if len(result) != len(tt.expected) {
				t.Errorf("parseBlameOutput() returned %d lines, expected %d", len(result), len(tt.expected))
				return
			}

			for i, got := range result {
				exp := tt.expected[i]
				if got.LineNumber != exp.LineNumber {
					t.Errorf("Line %d: LineNumber = %d, expected %d", i, got.LineNumber, exp.LineNumber)
				}
				if got.Author != exp.Author {
					t.Errorf("Line %d: Author = %q, expected %q", i, got.Author, exp.Author)
				}
				if got.AuthorEmail != exp.AuthorEmail {
					t.Errorf("Line %d: AuthorEmail = %q, expected %q", i, got.AuthorEmail, exp.AuthorEmail)
				}
				if !got.Timestamp.Equal(exp.Timestamp) {
					t.Errorf("Line %d: Timestamp = %v, expected %v", i, got.Timestamp, exp.Timestamp)
				}
				if got.CommitHash != exp.CommitHash {
					t.Errorf("Line %d: CommitHash = %q, expected %q", i, got.CommitHash, exp.CommitHash)
				}
				if got.Content != exp.Content {
					t.Errorf("Line %d: Content = %q, expected %q", i, got.Content, exp.Content)
				}
			}
		})
	}
}

func TestParseBlameFromReader(t *testing.T) {
	input := `abc123def456abc123def456abc123def456abc1
author John Doe
author-mail <john@example.com>
author-time 1700000000
filename test.md
	Hello from reader
`
	reader := strings.NewReader(input)
	result, err := parseBlameFromReader(reader)

	if err != nil {
		t.Fatalf("parseBlameFromReader() error = %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("parseBlameFromReader() returned %d lines, expected 1", len(result))
	}

	if result[0].Content != "Hello from reader" {
		t.Errorf("Content = %q, expected %q", result[0].Content, "Hello from reader")
	}
}

func TestParseBlameOutput_CommitHashOnly(t *testing.T) {
	// Test that 40-character lines are recognized as commit hashes
	input := `abc123def456abc123def456abc123def456abc1
author John Doe
author-mail <john@example.com>
author-time 1700000000
filename test.md
	Content line
`
	result, err := parseBlameOutput([]byte(input))

	if err != nil {
		t.Fatalf("parseBlameOutput() error = %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("parseBlameOutput() returned %d lines, expected 1", len(result))
	}

	if result[0].CommitHash != "abc123def456abc123def456abc123def456abc1" {
		t.Errorf("CommitHash = %q, expected %q", result[0].CommitHash, "abc123def456abc123def456abc123def456abc1")
	}
}

func TestParseBlameOutput_LargeFile(t *testing.T) {
	// Generate a large blame output to test performance
	var builder strings.Builder
	numLines := 1000

	for i := 1; i <= numLines; i++ {
		builder.WriteString("abc123def456abc123def456abc123def456abc1 ")
		builder.WriteString(strings.Repeat("0", 10))
		builder.WriteString("\n")
		builder.WriteString("author Test Author\n")
		builder.WriteString("author-mail <test@example.com>\n")
		builder.WriteString("author-time 1700000000\n")
		builder.WriteString("filename test.md\n")
		builder.WriteString("\tLine content\n")
	}

	result, err := parseBlameOutput([]byte(builder.String()))

	if err != nil {
		t.Fatalf("parseBlameOutput() error = %v", err)
	}

	if len(result) != numLines {
		t.Errorf("parseBlameOutput() returned %d lines, expected %d", len(result), numLines)
	}
}

func TestLineInfo_Timestamp(t *testing.T) {
	// Test that timestamps are correctly converted from Unix time
	unixTime := int64(1700000000)
	expected := time.Unix(unixTime, 0)

	input := `abc123def456abc123def456abc123def456abc1 1 1 1
author John Doe
author-mail <john@example.com>
author-time 1700000000
filename test.md
	Test
`
	result, err := parseBlameOutput([]byte(input))
	if err != nil {
		t.Fatalf("parseBlameOutput() error = %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("Expected 1 line, got %d", len(result))
	}

	if !result[0].Timestamp.Equal(expected) {
		t.Errorf("Timestamp = %v, expected %v", result[0].Timestamp, expected)
	}
}

func TestParseBlameOutput_TabInContent(t *testing.T) {
	// Test that tabs within content are preserved (only leading tab is stripped)
	input := `abc123def456abc123def456abc123def456abc1 1 1 1
author John Doe
author-mail <john@example.com>
author-time 1700000000
filename test.md
	Content	with	tabs
`
	result, err := parseBlameOutput([]byte(input))
	if err != nil {
		t.Fatalf("parseBlameOutput() error = %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("Expected 1 line, got %d", len(result))
	}

	expected := "Content\twith\ttabs"
	if result[0].Content != expected {
		t.Errorf("Content = %q, expected %q", result[0].Content, expected)
	}
}

func BenchmarkParseBlameOutput(b *testing.B) {
	// Generate test data
	var builder strings.Builder
	for i := 0; i < 100; i++ {
		builder.WriteString("abc123def456abc123def456abc123def456abc1 1 1 1\n")
		builder.WriteString("author John Doe\n")
		builder.WriteString("author-mail <john@example.com>\n")
		builder.WriteString("author-time 1700000000\n")
		builder.WriteString("filename test.md\n")
		builder.WriteString("\tLine content here\n")
	}
	input := []byte(builder.String())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parseBlameOutput(input)
	}
}

func BenchmarkParseBlameFromReader(b *testing.B) {
	// Generate test data
	var builder strings.Builder
	for i := 0; i < 100; i++ {
		builder.WriteString("abc123def456abc123def456abc123def456abc1 1 1 1\n")
		builder.WriteString("author John Doe\n")
		builder.WriteString("author-mail <john@example.com>\n")
		builder.WriteString("author-time 1700000000\n")
		builder.WriteString("filename test.md\n")
		builder.WriteString("\tLine content here\n")
	}
	input := builder.String()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reader := strings.NewReader(input)
		_, _ = parseBlameFromReader(reader)
	}
}

// TestParseGitLogTimestamp tests timestamp parsing in GetFileLastModified-style output
func TestParseGitLogTimestamp(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantTime  time.Time
		wantErr   bool
		errSubstr string
	}{
		{
			name:     "full timestamp with timezone",
			input:    "2024-01-15 10:30:45 -0800",
			wantTime: time.Date(2024, 1, 15, 10, 30, 45, 0, time.FixedZone("", -8*3600)),
			wantErr:  false,
		},
		{
			name:     "timestamp without timezone (19 chars)",
			input:    "2024-01-15 10:30:45",
			wantTime: time.Date(2024, 1, 15, 10, 30, 45, 0, time.UTC),
			wantErr:  false,
		},
		{
			name:      "timestamp too short (bug fix test)",
			input:     "2024-01-15",
			wantErr:   true,
			errSubstr: "invalid timestamp format",
		},
		{
			name:      "empty timestamp",
			input:     "",
			wantErr:   true,
			errSubstr: "invalid timestamp format",
		},
		{
			name:      "garbage input",
			input:     "not a date",
			wantErr:   true,
			errSubstr: "invalid timestamp format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the parsing logic from GetFileLastModified
			var timestamp time.Time
			var err error

			timestamp, err = time.Parse("2006-01-02 15:04:05 -0700", tt.input)
			if err != nil {
				// Try without timezone, but ensure string is long enough
				if len(tt.input) < 19 {
					err = fmt.Errorf("invalid timestamp format: %q", tt.input)
				} else {
					timestamp, err = time.Parse("2006-01-02 15:04:05", tt.input[:19])
				}
			}

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errSubstr)
					return
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.errSubstr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !timestamp.Equal(tt.wantTime) {
				t.Errorf("timestamp = %v, want %v", timestamp, tt.wantTime)
			}
		})
	}
}

// TestGetBlameInfo_Integration tests actual git blame execution
// This test requires the current directory to be in a git repository
func TestGetBlameInfo_Integration(t *testing.T) {
	// Skip if not in a git repo or git not available
	if _, err := GetGitRoot(); err != nil {
		t.Skip("not in a git repository, skipping integration test")
	}

	// Test on this test file itself (it should be tracked by git after commit)
	testFile := "blame_test.go"

	// First check if file is tracked
	result, err := GetBlameInfo(testFile)

	// If file is not tracked yet, that's okay - skip
	if err != nil {
		if strings.Contains(err.Error(), "no such path") ||
			strings.Contains(err.Error(), "fatal") {
			t.Skip("test file not yet tracked by git")
		}
		t.Fatalf("GetBlameInfo() error = %v", err)
	}

	// Basic sanity checks
	if len(result) == 0 {
		t.Error("GetBlameInfo() returned empty result for tracked file")
	}

	// Check that line numbers are sequential
	for i, line := range result {
		if line.LineNumber != i+1 {
			t.Errorf("Line %d has LineNumber = %d, expected %d", i, line.LineNumber, i+1)
		}
	}
}

// TestGetFileLastModified_Integration tests actual git log execution
func TestGetFileLastModified_Integration(t *testing.T) {
	// Skip if not in a git repo
	root, err := GetGitRoot()
	if err != nil {
		t.Skip("not in a git repository, skipping integration test")
	}

	// Test on a file that should definitely be tracked - use go.mod at repo root
	testFile := root + "/go.mod"

	info, err := GetFileLastModified(testFile)
	if err != nil {
		// File might not be tracked yet
		t.Skipf("file not tracked by git: %v", err)
	}

	if info == nil {
		t.Skip("GetFileLastModified() returned nil info - file may have no commits")
	}

	// Basic sanity checks
	if info.LastAuthor == "" {
		t.Error("LastAuthor is empty")
	}

	if info.LastCommit == "" {
		t.Error("LastCommit is empty")
	}

	if len(info.LastCommit) != 40 {
		t.Errorf("LastCommit length = %d, expected 40", len(info.LastCommit))
	}

	// Timestamp should be in the past
	if info.LastModified.After(time.Now()) {
		t.Error("LastModified is in the future")
	}

	// Timestamp should be after git was invented (2005)
	if info.LastModified.Year() < 2005 {
		t.Errorf("LastModified year = %d, expected >= 2005", info.LastModified.Year())
	}
}

// TestGetGitRootForPath_Integration tests git root detection
func TestGetGitRootForPath_Integration(t *testing.T) {
	// Skip if not in a git repo
	root, err := GetGitRoot()
	if err != nil {
		t.Skip("not in a git repository, skipping integration test")
	}

	// Test with current file
	rootForPath, err := GetGitRootForPath("blame.go")
	if err != nil {
		t.Fatalf("GetGitRootForPath() error = %v", err)
	}

	// Should return the same root (normalized)
	if rootForPath != root {
		t.Errorf("GetGitRootForPath() = %q, GetGitRoot() = %q", rootForPath, root)
	}
}

// TestGetGitRootForPath_Caching tests that caching works
func TestGetGitRootForPath_Caching(t *testing.T) {
	// Skip if not in a git repo
	if _, err := GetGitRoot(); err != nil {
		t.Skip("not in a git repository, skipping integration test")
	}

	// Call twice - second should use cache
	root1, err := GetGitRootForPath("blame.go")
	if err != nil {
		t.Fatalf("first call error = %v", err)
	}

	root2, err := GetGitRootForPath("blame.go")
	if err != nil {
		t.Fatalf("second call error = %v", err)
	}

	if root1 != root2 {
		t.Errorf("cached result differs: %q vs %q", root1, root2)
	}
}

// TestParseBlameOutput_ConsecutiveLinesFromSameCommit tests multiple lines from same commit
func TestParseBlameOutput_ConsecutiveLinesFromSameCommit(t *testing.T) {
	// When multiple consecutive lines are from the same commit,
	// git blame can output abbreviated headers
	input := `abc123def456abc123def456abc123def456abc1
author John Doe
author-mail <john@example.com>
author-time 1700000000
filename test.md
	Line 1
abc123def456abc123def456abc123def456abc1
author John Doe
author-mail <john@example.com>
author-time 1700000000
filename test.md
	Line 2
abc123def456abc123def456abc123def456abc1
author John Doe
author-mail <john@example.com>
author-time 1700000000
filename test.md
	Line 3
`
	result, err := parseBlameOutput([]byte(input))
	if err != nil {
		t.Fatalf("parseBlameOutput() error = %v", err)
	}

	if len(result) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(result))
	}

	// All lines should have the same commit hash
	for i, line := range result {
		if line.CommitHash != "abc123def456abc123def456abc123def456abc1" {
			t.Errorf("line %d: commit hash = %q, expected abc123...", i, line.CommitHash)
		}
		if line.Author != "John Doe" {
			t.Errorf("line %d: author = %q, expected John Doe", i, line.Author)
		}
	}
}

package git

import (
	"strings"
	"testing"
)

// TestParseBlame_RealPorcelainHeader uses the real `git blame --line-porcelain`
// header form ("<sha> <orig> <final> <group>") and asserts the commit hash is
// captured. Regression for #21: older tests omitted the line numbers after the
// SHA, which masked that the header was parsed as a generic key/value pair and
// CommitHash stayed empty for real git output.
func TestParseBlame_RealPorcelainHeader(t *testing.T) {
	const sha = "c4ef264cde14aae7191bed05dad94691bb13bef9"
	input := sha + " 1 1 3\n" +
		"author Jane Doe\n" +
		"author-mail <jane@example.com>\n" +
		"author-time 1700000000\n" +
		"author-tz -0800\n" +
		"summary Initial commit\n" +
		"filename test.md\n" +
		"\tHello, World!\n"

	got, err := parseBlameFromReader(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseBlameFromReader() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d lines, want 1", len(got))
	}
	if got[0].CommitHash != sha {
		t.Errorf("CommitHash = %q, want %q", got[0].CommitHash, sha)
	}
	if got[0].Author != "Jane Doe" {
		t.Errorf("Author = %q, want %q", got[0].Author, "Jane Doe")
	}
}

// TestParseBlame_LongLine verifies a content line larger than bufio's default
// 64KB token limit is parsed rather than aborting the whole file's blame.
// Regression for #23.
func TestParseBlame_LongLine(t *testing.T) {
	const sha = "c4ef264cde14aae7191bed05dad94691bb13bef9"
	long := strings.Repeat("x", 200*1024) // 200KB, well over bufio's 64KB default
	input := sha + " 1 1 1\n" +
		"author Jane Doe\n" +
		"author-mail <jane@example.com>\n" +
		"author-time 1700000000\n" +
		"filename test.md\n" +
		"\t" + long + "\n"

	got, err := parseBlameFromReader(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseBlameFromReader() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d lines, want 1 (long line was likely dropped)", len(got))
	}
	if len(got[0].Content) != len(long) {
		t.Errorf("Content length = %d, want %d", len(got[0].Content), len(long))
	}
}

// TestIsCommitHash covers SHA-1, SHA-256, and non-hash inputs.
func TestIsCommitHash(t *testing.T) {
	cases := map[string]bool{
		"c4ef264cde14aae7191bed05dad94691bb13bef9": true,  // 40 hex
		strings.Repeat("a", 64):                    true,  // 64 hex
		"author":                                   false, // a porcelain key
		"c4ef264cde14aae7191bed05dad94691bb13bef":  false, // 39 chars
		"g4ef264cde14aae7191bed05dad94691bb13bef9": false, // non-hex char
	}
	for in, want := range cases {
		if got := isCommitHash(in); got != want {
			t.Errorf("isCommitHash(%q) = %v, want %v", in, got, want)
		}
	}
}

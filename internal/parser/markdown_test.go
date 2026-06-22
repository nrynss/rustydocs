package parser

import (
	"strings"
	"testing"
)

// TestParseChunks_CRLFHeaders verifies that Windows (CRLF) line endings do not
// leave a trailing carriage return in section titles. Regression test for #5.
func TestParseChunks_CRLFHeaders(t *testing.T) {
	content := "# Title One\r\nsome text\r\n\r\n## Section Two\r\nmore text\r\n"

	chunks := ParseSections(content, nil, DefaultReusablePatterns())
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}

	for _, c := range chunks {
		if strings.ContainsRune(c.Title, '\r') {
			t.Errorf("title %q contains a carriage return", c.Title)
		}
	}

	if chunks[0].Title != "Title One" {
		t.Errorf("chunk[0].Title = %q, want %q", chunks[0].Title, "Title One")
	}
	if chunks[1].Title != "Section Two" {
		t.Errorf("chunk[1].Title = %q, want %q", chunks[1].Title, "Section Two")
	}
}

// TestParseChunks_CRLFPreservesLineNumbers verifies CRLF normalization does not
// shift section start lines (git-blame alignment must be preserved).
func TestParseChunks_CRLFPreservesLineNumbers(t *testing.T) {
	crlf := "# A\r\nx\r\n## B\r\ny\r\n"
	lf := "# A\nx\n## B\ny\n"

	c1 := ParseSections(crlf, nil, DefaultReusablePatterns())
	c2 := ParseSections(lf, nil, DefaultReusablePatterns())

	if len(c1) != len(c2) {
		t.Fatalf("chunk count differs: crlf=%d lf=%d", len(c1), len(c2))
	}
	for i := range c1 {
		if c1[i].StartLine != c2[i].StartLine {
			t.Errorf("chunk[%d] start line: crlf=%d lf=%d", i, c1[i].StartLine, c2[i].StartLine)
		}
	}
}

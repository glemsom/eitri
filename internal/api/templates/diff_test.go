package templates

import (
	"strings"
	"testing"
)

func TestDiffText_Identical(t *testing.T) {
	content := "hello\nworld\n"
	result := diffText(content, content)

	lines := strings.Split(strings.TrimSuffix(result, "\n"), "\n")
	for i, line := range lines {
		if len(line) == 0 {
			continue
		}
		if line[0] != ' ' {
			t.Errorf("line %d: expected context line ' %s', got %q", i, line[1:], line)
		}
	}
}

func TestDiffText_AddedLine(t *testing.T) {
	old := "line1\nline2\n"
	new := "line1\nline2\nline3\n"

	result := diffText(old, new)
	if !strings.Contains(result, "+line3") {
		t.Errorf("expected added line '+line3' in diff, got:\n%s", result)
	}
}

func TestDiffText_RemovedLine(t *testing.T) {
	old := "line1\nline2\nline3\n"
	new := "line1\nline3\n"

	result := diffText(old, new)
	if !strings.Contains(result, "-line2") {
		t.Errorf("expected removed line '-line2' in diff, got:\n%s", result)
	}
}

func TestDiffText_ChangedLine(t *testing.T) {
	old := "hello\nworld\n"
	new := "hello\nthere\n"

	result := diffText(old, new)
	if !strings.Contains(result, "-world") {
		t.Errorf("expected '-world' in diff, got:\n%s", result)
	}
	if !strings.Contains(result, "+there") {
		t.Errorf("expected '+there' in diff, got:\n%s", result)
	}
}

func TestDiffText_EmptyOld(t *testing.T) {
	result := diffText("", "new content\n")
	if !strings.Contains(result, "+new content") {
		t.Errorf("expected added line '+new content' in diff, got:\n%s", result)
	}
}

func TestDiffText_EmptyNew(t *testing.T) {
	result := diffText("old content\n", "")
	if !strings.Contains(result, "-old content") {
		t.Errorf("expected removed line '-old content' in diff, got:\n%s", result)
	}
}

func TestSplitLines_WithTrailingNewline(t *testing.T) {
	lines := splitLines("hello\nworld\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (hello, world, empty), got %d: %v", len(lines), lines)
	}
	if lines[0] != "hello" || lines[1] != "world" || lines[2] != "" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestSplitLines_WithoutTrailingNewline(t *testing.T) {
	lines := splitLines("hello\nworld")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "hello" || lines[1] != "world" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestSplitLines_Empty(t *testing.T) {
	lines := splitLines("")
	if len(lines) != 1 || lines[0] != "" {
		t.Errorf("expected [\"\"], got %v", lines)
	}
}

func TestEscapeDiff(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"a & b", "a &amp; b"},
		{"<tag>", "&lt;tag&gt;"},
		{"a > b", "a &gt; b"},
	}
	for _, tt := range tests {
		got := escapeDiff(tt.input)
		if got != tt.expected {
			t.Errorf("escapeDiff(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

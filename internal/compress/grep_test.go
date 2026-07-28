package compress

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestCompressGrep_TinyOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{"empty", ""},
		{"one line", "foo.go:1:content"},
		{"two lines", "a.go:1:x\nb.go:2:y"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compressGrep(tt.output)
			if got != nil {
				t.Errorf("compressGrep(%q) = %v, want nil", tt.output, *got)
			}
		})
	}
}

func TestCompressGrep_NonGrepOutput(t *testing.T) {
	// Lines without file:line:content pattern should return nil.
	tests := []string{
		"hello world\nthis is not grep output\nfoo bar",
		"some random text\nwithout any colons\nor paths",
		"Binary file foo.bin matches\nanother line",
		"path without dot or slash:1:content",
	}
	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			got := compressGrep(tt)
			if got != nil {
				t.Errorf("compressGrep(%q) = %v, want nil", tt, *got)
			}
		})
	}
}

func TestCompressGrep_BinaryFilesSkipped(t *testing.T) {
	// Binary file lines should be skipped, but valid lines should still parse.
	output := `Binary file foo.bin matches
src/main.go:10:func main() {
src/main.go:11:    fmt.Println("hello")
Binary file images/logo.png matches
src/util.go:5:func helper() {`

	got := compressGrep(output)
	if got == nil {
		t.Fatal("compressGrep returned nil, expected compressed output")
	}

	if strings.Contains(*got, "Binary file") {
		t.Errorf("compressed output should not contain binary file lines:\n%s", *got)
	}
	if !strings.Contains(*got, "main.go") {
		t.Errorf("compressed output should contain main.go:\n%s", *got)
	}
	if !strings.Contains(*got, "util.go") {
		t.Errorf("compressed output should contain util.go:\n%s", *got)
	}
}

func TestCompressGrep_MultiFileMultiMatch(t *testing.T) {
	output := `src/main.go:10:func main() {
src/main.go:11:    fmt.Println("hello")
src/main.go:15:    run()
src/main.go:20:    process()
src/main.go:21:    cleanup()
src/util.go:5:func helper() {
src/util.go:8:    return result
src/util.go:12:func utilFunc() {
src/util.go:15:func validate() {
src/util.go:18:    return nil
src/README.md:1:# Project
src/README.md:3:## Usage
src/README.md:5:## Installation
src/README.md:10:## License`

	got := compressGrep(output)
	if got == nil {
		t.Fatal("compressGrep returned nil, expected compressed output")
	}

	// Check summary line.
	if !strings.HasPrefix(*got, "14 matches in 3 files:") {
		t.Errorf("expected summary line '14 matches in 3 files:', got:\n%s", *got)
	}

	// Check that files appear sorted by match count (descending).
	// main.go: 5, util.go: 5, README.md: 4
	if !strings.Contains(*got, "src/main.go (5 matches)") {
		t.Errorf("expected main.go with 5 matches:\n%s", *got)
	}
	if !strings.Contains(*got, "src/util.go (5 matches)") {
		t.Errorf("expected util.go with 5 matches:\n%s", *got)
	}
	if !strings.Contains(*got, "src/README.md (4 matches)") {
		t.Errorf("expected README.md with 4 matches:\n%s", *got)
	}

	// Check line numbers are present.
	if !strings.Contains(*got, "  10: func main() {") {
		t.Errorf("expected line 10 content:\n%s", *got)
	}
	if !strings.Contains(*got, "  5: func helper() {") {
		t.Errorf("expected line 5 content:\n%s", *got)
	}
}

func TestCompressGrep_LargeOutputCappedAt5PerFile(t *testing.T) {
	// More than 200 total matches → cap per file at 5.
	var sb strings.Builder
	for i := 1; i <= 30; i++ {
		sb.WriteString("src/main.go:")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString(":line content number ")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteByte('\n')
	}
	// Add some other files to stay under the 3-line minimum but create
	// enough total matches.
	for i := 1; i <= 180; i++ {
		sb.WriteString("src/util.go:")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString(":util line ")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteByte('\n')
	}

	output := sb.String()
	got := compressGrep(output)
	if got == nil {
		t.Fatal("compressGrep returned nil, expected compressed output")
	}

	// Should have >200 matches, so cap per file at 5.
	if !strings.Contains(*got, "... +25 more") {
		// main.go has 30 matches, capped at 5 → +25 more.
		t.Errorf("expected '+25 more' for main.go capped at 5:\n%s", *got)
	}
	if !strings.Contains(*got, "... +175 more") {
		// util.go has 180 matches, capped at 5 → +175 more.
		t.Errorf("expected '+175 more' for util.go capped at 5:\n%s", *got)
	}
}

func TestCompressGrep_SmallOutputCappedAt10PerFile(t *testing.T) {
	// ≤200 total matches → cap per file at 10.
	var sb strings.Builder
	for i := 1; i <= 15; i++ {
		sb.WriteString("src/main.go:")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString(":line ")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteByte('\n')
	}
	output := sb.String()
	got := compressGrep(output)
	if got == nil {
		t.Fatal("compressGrep returned nil, expected compressed output")
	}

	// main.go has 15 matches, total ≤200, so cap at 10 → +5 more.
	if !strings.Contains(*got, "... +5 more") {
		t.Errorf("expected '+5 more' for main.go capped at 10:\n%s", *got)
	}

	// First 10 lines should be shown.
	for i := 1; i <= 10; i++ {
		expected := fmt.Sprintf("  %d: line %d", i, i)
		if !strings.Contains(*got, expected) {
			t.Errorf("expected line %d in output:\n%s", i, *got)
		}
	}
}

func TestCompressGrep_SingleMatchPerFileManyFiles(t *testing.T) {
	// 30 files with 1 match each → headers cost more than they save → nil.
	var sb strings.Builder
	for i := 1; i <= 30; i++ {
		sb.WriteString(fmt.Sprintf("src/file%d.go:%d:content %d\n", i, i, i))
	}
	output := sb.String()
	got := compressGrep(output)
	if got != nil {
		t.Errorf("expected nil for 30 files with 1 match each (anti-inflation), got:\n%s", *got)
	}
}

func TestCompressGrep_LongLinesTruncated(t *testing.T) {
	longContent := ""
	for i := 0; i < 150; i++ {
		longContent += "word "
	}
	output := fmt.Sprintf("src/main.go:10:%s\nsrc/main.go:20:%s\nsrc/util.go:5:short content", longContent, longContent)

	got := compressGrep(output)
	if got == nil {
		t.Fatal("compressGrep returned nil, expected compressed output")
	}

	// The long content should be truncated at 120 chars.
	// Look for match content lines that start with "word ".
	foundTruncated := false
	for _, line := range strings.Split(*got, "\n") {
		if strings.Contains(line, "word ") && !strings.HasPrefix(line, "...") && !strings.HasPrefix(line, "  ") {
			continue
		}
		// Match content lines start with "  N: ".
		if strings.HasPrefix(line, "  ") && strings.Contains(line, ": ") {
			// Find content after ": ".
			parts := strings.SplitN(line, ": ", 2)
			if len(parts) == 2 && strings.HasPrefix(parts[1], "word ") && strings.HasSuffix(parts[1], "…") {
				foundTruncated = true
			}
		}
	}
	if !foundTruncated {
		t.Errorf("expected long content to be truncated with …:\n%s", *got)
	}
}

func TestCompressGrep_Determinism(t *testing.T) {
	output := `src/main.go:10:func main() {
src/main.go:11:    fmt.Println("hello")
src/main.go:15:    run()
src/main.go:20:    process()
src/main.go:21:    cleanup()
src/util.go:5:func helper() {
src/util.go:8:    return result
src/util.go:12:func utilFunc() {
src/util.go:15:func validate() {
src/main.go:25:    deploy()
src/README.md:1:# Project
src/README.md:3:## Usage
src/README.md:5:## Installation`

	first := compressGrep(output)
	if first == nil {
		t.Fatal("compressGrep returned nil on first call")
	}

	for i := 0; i < 10; i++ {
		got := compressGrep(output)
		if got == nil {
			t.Fatalf("compressGrep returned nil on iteration %d", i)
		}
		if *got != *first {
			t.Fatalf("non-deterministic output on iteration %d:\nfirst:\n%s\niteration:\n%s", i, *first, *got)
		}
	}
}

func TestCompressGrep_AntiInflation(t *testing.T) {
	// Very small output returns nil.
	got := compressGrep("a.go:1:hello\nb.go:2:world")
	if got != nil {
		t.Errorf("expected nil for small output, got %v", *got)
	}

	// For compressed output, ensure token estimate never exceeds original.
	// Larger dataset to make compression beneficial.
	var sb strings.Builder
	for i := 1; i <= 20; i++ {
		sb.WriteString(fmt.Sprintf("src/main.go:%d:    line of code number %d\n", i, i))
	}
	for i := 1; i <= 15; i++ {
		sb.WriteString(fmt.Sprintf("src/util.go:%d:    util function line %d\n", i, i))
	}
	for i := 1; i <= 10; i++ {
		sb.WriteString(fmt.Sprintf("src/README.md:%d:## Section %d\n", i, i))
	}
	output := sb.String()

	compressed := compressGrep(output)
	if compressed == nil {
		t.Fatal("expected non-nil output for compressible input")
	}

	origTokens := len(output) / 4
	compTokens := len(*compressed) / 4
	if compTokens >= origTokens {
		t.Errorf("compressed output uses more or equal tokens (%d >= %d)\noriginal (%d chars):\n%s\ncompressed (%d chars):\n%s",
			compTokens, origTokens, len(output), output, len(*compressed), *compressed)
	}
}

func TestCompressGrep_LineNumbersSorted(t *testing.T) {
	output := `src/main.go:30:third line content
src/main.go:10:first line here
src/main.go:20:second line
src/main.go:5:zeroth line entry
src/main.go:40:fourth entry
src/main.go:3:another early line
src/main.go:25:midway through
src/main.go:35:later line content`

	got := compressGrep(output)
	if got == nil {
		t.Fatal("compressGrep returned nil")
	}

	// Lines should be sorted by line number within each file.
	line3Idx := strings.Index(*got, "another early")
	line5Idx := strings.Index(*got, "zeroth line")
	line10Idx := strings.Index(*got, "first line")
	line20Idx := strings.Index(*got, "second line")
	line25Idx := strings.Index(*got, "midway")
	line30Idx := strings.Index(*got, "third line")
	line35Idx := strings.Index(*got, "later line")
	line40Idx := strings.Index(*got, "fourth entry")

	if line3Idx < 0 || line5Idx < 0 || line10Idx < 0 || line20Idx < 0 ||
		line25Idx < 0 || line30Idx < 0 || line35Idx < 0 || line40Idx < 0 {
		t.Fatalf("expected all lines in output:\n%s", *got)
	}

	if !(line3Idx < line5Idx && line5Idx < line10Idx && line10Idx < line20Idx &&
		line20Idx < line25Idx && line25Idx < line30Idx && line30Idx < line35Idx &&
		line35Idx < line40Idx) {
		t.Errorf("lines not sorted by line number:\n%s", *got)
	}
}

func TestCompressGrep_NoColonInPath(t *testing.T) {
	// Lines with no '.' or '/' in path before first colon should be ignored.
	output := `Makefile:10:all: build
src/main.go:5:func main() {
src/main.go:6:    fmt.Println("hello")
src/main.go:8:    run()
src/main.go:9:    process()
src/main.go:10:    cleanup()
src/util.go:3:package util
src/util.go:5:func Helper() {
src/util.go:7:    return nil
src/util.go:9:func publicFunc() {`

	got := compressGrep(output)
	if got == nil {
		t.Fatal("compressGrep returned nil")
	}

	// "Makefile:10:all: build" has colons but "Makefile" has no '/' or '.'.
	// It should be skipped.
	if strings.Contains(*got, "Makefile") {
		t.Errorf("Makefile without path separator should be skipped:\n%s", *got)
	}
	if !strings.Contains(*got, "main.go") {
		t.Errorf("expected main.go in output:\n%s", *got)
	}
	if !strings.Contains(*got, "util.go") {
		t.Errorf("expected util.go in output:\n%s", *got)
	}
}

func TestCompressGrep_OnlyThreeFilesAboveThreshold(t *testing.T) {
	// A moderate number of files (5) with enough matches each should compress.
	var sb strings.Builder
	for i := 1; i <= 6; i++ {
		sb.WriteString(fmt.Sprintf("src/main.go:%d:line %d\n", i, i))
	}
	for i := 1; i <= 5; i++ {
		sb.WriteString(fmt.Sprintf("src/util.go:%d:util %d\n", i, i))
	}
	for i := 1; i <= 4; i++ {
		sb.WriteString(fmt.Sprintf("src/config.go:%d:config %d\n", i, i))
	}
	sb.WriteString("src/README.md:1:# Project")
	output := sb.String()
	got := compressGrep(output)
	if got == nil {
		t.Fatal("expected compression for 16 matches in 4 files")
	}
}

func TestCompressGrep_ParseGrepLine(t *testing.T) {
	tests := []struct {
		line    string
		wantOK  bool
		want    grepLine
	}{
		{"src/main.go:10:func main() {", true, grepLine{"src/main.go", 10, "func main() {"}},
		{"./main.go:5:package main", true, grepLine{"./main.go", 5, "package main"}},
		{"/abs/path/file.go:20:content here", true, grepLine{"/abs/path/file.go", 20, "content here"}},
		{"foo/bar.go:1:x", true, grepLine{"foo/bar.go", 1, "x"}},
		{"file.go:1:content", true, grepLine{"file.go", 1, "content"}},
		{"no/separator:1:x", true, grepLine{"no/separator", 1, "x"}},
		{"Binary file foo.bin matches", false, grepLine{}},
		{"not-a-path", false, grepLine{}},
		{"path:abc:content", false, grepLine{}}, // line num not int
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got, ok := parseGrepLine(tt.line)
			if ok != tt.wantOK {
				t.Errorf("parseGrepLine(%q) ok = %v, want %v", tt.line, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("parseGrepLine(%q) = %+v, want %+v", tt.line, got, tt.want)
			}
		})
	}
}

func TestCompressGrep_TruncateLine(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"hello world", 5, "hello…"},
		{"exact", 5, "exact"},
		{"", 10, ""},
		{"a", 1, "a"},
		{"ab", 1, "a…"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := truncateLine(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateLine(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestCompressGrep_SummaryLinePrefix(t *testing.T) {
	// Need enough matches so compressed format saves tokens vs original.
	var sb strings.Builder
	for i := 1; i <= 10; i++ {
		sb.WriteString(fmt.Sprintf("src/main.go:%d:    line of code #%d\n", i, i))
	}
	for i := 1; i <= 8; i++ {
		sb.WriteString(fmt.Sprintf("src/util.go:%d:    helper line %d\n", i, i))
	}
	for i := 1; i <= 6; i++ {
		sb.WriteString(fmt.Sprintf("src/README.md:%d:## Section %d\n", i, i))
	}
	output := sb.String()

	got := compressGrep(output)
	if got == nil {
		t.Fatal("compressGrep returned nil")
	}

	// First line should be "N matches in M files:".
	lines := strings.SplitN(*got, "\n", 2)
	if len(lines) < 2 {
		t.Fatalf("output too short:\n%s", *got)
	}
	firstLine := lines[0]
	if !strings.HasPrefix(firstLine, "24 matches in 3 files:") {
		t.Errorf("expected '24 matches in 3 files:', got: %q", firstLine)
	}
}

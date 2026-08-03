package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/voocel/litellm"
)

func TestGrep_Schema(t *testing.T) {
	tool := NewGrepTool("/tmp")
	if tool.Name() != "grep" {
		t.Errorf("Name = %q, want 'grep'", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description should not be empty")
	}
	schema := tool.JSONSchema()
	if schema == nil {
		t.Fatal("JSONSchema is nil")
	}
	if !json.Valid(schema) {
		t.Error("JSONSchema is not valid JSON")
	}
}

func TestGrep_InvalidArgs(t *testing.T) {
	tool := NewGrepTool("/tmp")
	_, err := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestGrep_EmptyPattern(t *testing.T) {
	tool := NewGrepTool("/tmp")
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	if !strings.Contains(block.Text, "pattern is required") {
		t.Errorf("expected 'pattern is required' error, got %q", block.Text)
	}
}

func TestGrep_InvalidRegex(t *testing.T) {
	tool := NewGrepTool("/tmp")
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"[invalid"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("result.IsError = false, want true")
	}
}

func TestGrep_SuccessfulMatch(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"hello.go":    "package main\nfunc main() { println(\"hello\") }\n",
		"world.go":    "package main\nfunc main() { println(\"world\") }\n",
		"other.txt":   "this is a text file with hello in it\n",
		"sub/deep.go": "package sub\nfunc Hello() {}\n",
	}
	for name, content := range files {
		fp := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewGrepTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"hello"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}

	// Should find "hello" in: hello.go (line 2), other.txt (line 1), sub/deep.go (line 2, "Hello" case-sensitive)
	// "hello" in hello.go:2, other.txt:1 (both lowercase hello)
	// "Hello" in sub/deep.go:2 won't match because case-sensitive
	expectedLines := []string{"hello.go:2:func main() { println(\"hello\") }", "other.txt:1:this is a text file with hello in it"}
	for _, el := range expectedLines {
		if !strings.Contains(block.Text, el) {
			t.Errorf("expected output to contain %q, got:\n%s", el, block.Text)
		}
	}
	// sub/deep.go:2 has "Hello" with capital H, case-sensitive search for "hello" should NOT match
	if strings.Contains(block.Text, "sub/deep.go") {
		t.Errorf("output should not contain sub/deep.go for case-sensitive 'hello' search")
	}
}

func TestGrep_NoMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"zzznonexistent"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	// No matches: blocks may be nil or contain empty text
	if len(result.Blocks) > 0 {
		block, ok := result.Blocks[0].(litellm.TextBlock)
		if !ok {
			t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
		}
		if block.Text != "" {
			t.Errorf("expected empty output, got %q", block.Text)
		}
	}
}

func TestGrep_FilePatternFilter(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"greeting.go": "package main\nfunc hi() { println(\"hello\") }\n",
		"greeting.py": "def hi():\n    print(\"hello\")\n",
		"data.txt":    "hello world\n",
		"cmd/main.go": "package main\nfunc main() { println(\"hello\") }\n",
	}
	for name, content := range files {
		fp := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tool := NewGrepTool(dir)
	// Search for "hello" only in *.go files
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"hello","file_pattern":"*.go"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}

	// Should match greeting.go (root) and cmd/main.go (nested)
	if !strings.Contains(block.Text, "greeting.go") {
		t.Errorf("expected greeting.go in output, got:\n%s", block.Text)
	}
	if !strings.Contains(block.Text, "cmd/main.go") {
		t.Errorf("expected cmd/main.go in output (nested *.go), got:\n%s", block.Text)
	}
	if strings.Contains(block.Text, "greeting.py") {
		t.Errorf("output should not contain greeting.py (file_pattern=*.go)")
	}
	if strings.Contains(block.Text, "data.txt") {
		t.Errorf("output should not contain data.txt (file_pattern=*.go)")
	}
}

func TestGrep_OutputTruncation(t *testing.T) {
	dir := t.TempDir()

	// Create a file with many matching lines to exceed 2 KiB
	var lines []string
	for i := 0; i < 5000; i++ {
		lines = append(lines, "match line content here")
	}
	content := strings.Join(lines, "\n")
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"match"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}

	// Output should end with the truncation marker
	if !strings.HasSuffix(block.Text, "... (output truncated at 2 KiB)") {
		t.Errorf("expected output to be truncated, got length %d, suffix %q", len(block.Text), block.Text[len(block.Text)-40:])
	}
}

func TestGrep_HiddenDirExclusion(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden", "secret.go"), []byte("package main\nfunc secret() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "visible.go"), []byte("package main\nfunc visible() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"func"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}

	// visible.go should match, .hidden/secret.go should be excluded
	if !strings.Contains(block.Text, "visible.go") {
		t.Errorf("expected visible.go in output, got:\n%s", block.Text)
	}
	if strings.Contains(block.Text, "secret.go") {
		t.Errorf("output should not contain secret.go from hidden directory")
	}
}

func TestGrep_VendorExclusion(t *testing.T) {
	dir := t.TempDir()
	vendorDir := filepath.Join(dir, "vendor")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendorDir, "dep.go"), []byte("package dep\nfunc depFunc() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"func"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}

	// Only main.go should be visible, vendor/ excluded
	if !strings.Contains(block.Text, "main.go") {
		t.Errorf("expected main.go in output, got:\n%s", block.Text)
	}
	if strings.Contains(block.Text, "dep.go") {
		t.Errorf("output should not contain dep.go from vendor directory")
	}
}

func TestGrep_ContextLines(t *testing.T) {
	dir := t.TempDir()
	content := []string{
		"line1",
		"line2",
		"line3 match",
		"line4",
		"line5",
		"line6 match",
		"line7",
	}
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte(strings.Join(content, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("context zero produces same output as before", func(t *testing.T) {
		tool := NewGrepTool(dir)
		result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"match","context":0}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Error("result.IsError = true, want false")
		}
		block, ok := result.Blocks[0].(litellm.TextBlock)
		if !ok {
			t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
		}
		// context 0: only match lines, no > prefix
		lines := strings.Split(strings.TrimSpace(block.Text), "\n")
		if len(lines) != 2 {
			t.Errorf("expected 2 match lines, got %d: %v", len(lines), lines)
		}
		for _, line := range lines {
			if strings.HasPrefix(line, ">") {
				t.Errorf("context=0 should not have > prefix: %q", line)
			}
		}
	})

	t.Run("context two returns surrounding lines", func(t *testing.T) {
		tool := NewGrepTool(dir)
		result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"match","context":2}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Error("result.IsError = true, want false")
		}
		block, ok := result.Blocks[0].(litellm.TextBlock)
		if !ok {
			t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
		}
		lines := strings.Split(strings.TrimSpace(block.Text), "\n")
		if len(lines) < 4 {
			t.Errorf("expected at least 4 lines (matches + context), got %d", len(lines))
		}
		// Match lines should have > prefix
		hasMatchPrefix := false
		for _, line := range lines {
			if strings.HasPrefix(line, ">test.txt:") {
				hasMatchPrefix = true
				break
			}
		}
		if !hasMatchPrefix {
			t.Errorf("expected at least one line with > prefix for match line, got:\n%s", block.Text)
		}
		// Context lines should not have > prefix
		hasContextLine := false
		for _, line := range lines {
			if strings.HasPrefix(line, "test.txt:") && !strings.HasPrefix(line, ">") {
				hasContextLine = true
				break
			}
		}
		if !hasContextLine {
			t.Errorf("expected at least one context line without > prefix, got:\n%s", block.Text)
		}
	})

	t.Run("context respects output cap", func(t *testing.T) {
		dir2 := t.TempDir()
		// Create many lines with long content so context output exceeds 2 KiB
		var bigLines []string
		for i := 0; i < 5000; i++ {
			bigLines = append(bigLines, fmt.Sprintf("line-%d-%s", i, strings.Repeat("x", 80)))
		}
		// Every line is a match
		if err := os.WriteFile(filepath.Join(dir2, "big.txt"), []byte(strings.Join(bigLines, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}

		tool := NewGrepTool(dir2)
		result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"^line-","context":2}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Error("result.IsError = true, want false")
		}
		block, ok := result.Blocks[0].(litellm.TextBlock)
		if !ok {
			t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
		}
		if !strings.Contains(block.Text, "truncated at 2 KiB") {
			t.Errorf("expected truncation marker in output, got length %d", len(block.Text))
		}
	})

	t.Run("context cap counts final rendered bytes only", func(t *testing.T) {
		dir2 := t.TempDir()
		var bigLines []string
		for i := 1; i <= 200; i++ {
			if i%4 == 2 {
				bigLines = append(bigLines, fmt.Sprintf("needle line %03d %s", i, strings.Repeat("x", 40)))
			} else {
				bigLines = append(bigLines, fmt.Sprintf("filler line %03d %s", i, strings.Repeat("x", 40)))
			}
		}
		if err := os.WriteFile(filepath.Join(dir2, "big.txt"), []byte(strings.Join(bigLines, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}

		tool := NewGrepTool(dir2)
		result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"needle","context":1}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Error("result.IsError = true, want false")
		}
		block, ok := result.Blocks[0].(litellm.TextBlock)
		if !ok {
			t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
		}
		if !strings.Contains(block.Text, "truncated at 2 KiB") {
			t.Fatalf("expected truncation marker in output, got length %d", len(block.Text))
		}
		if len(strings.TrimSuffix(block.Text, "... (output truncated at 2 KiB)")) < 1800 {
			t.Errorf("context output truncated too early; got %d bytes before marker", len(strings.TrimSuffix(block.Text, "... (output truncated at 2 KiB)")))
		}
		if !strings.Contains(block.Text, ">big.txt:34:needle line 034") {
			t.Errorf("expected rendered output to reach line 34 before truncation, got:\n%s", block.Text)
		}
	})

	t.Run("context with file_pattern filter", func(t *testing.T) {
		dir3 := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir3, "a.go"), []byte("line1\nline2 match\nline3\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir3, "b.txt"), []byte("line1\nline2 match\nline3\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		tool := NewGrepTool(dir3)
		result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"match","context":1,"file_pattern":"*.go"}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Error("result.IsError = true, want false")
		}
		block, ok := result.Blocks[0].(litellm.TextBlock)
		if !ok {
			t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
		}
		if !strings.Contains(block.Text, "a.go") {
			t.Errorf("expected a.go in output")
		}
		if strings.Contains(block.Text, "b.txt") {
			t.Errorf("output should not contain b.txt")
		}
		if !strings.Contains(block.Text, "a.go:1:line1") {
			t.Errorf("expected context line a.go:1:line1, got:\n%s", block.Text)
		}
		if !strings.Contains(block.Text, "a.go:3:line3") {
			t.Errorf("expected context line a.go:3:line3, got:\n%s", block.Text)
		}
	})
}

func TestGrep_ArgsUnmarshal(t *testing.T) {
	args := json.RawMessage(`{"pattern":"func","file_pattern":"*.go"}`)
	var parsed grepArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Pattern != "func" {
		t.Errorf("Pattern = %q, want 'func'", parsed.Pattern)
	}
	if parsed.FilePattern != "*.go" {
		t.Errorf("FilePattern = %q, want '*.go'", parsed.FilePattern)
	}
}

func TestGrep_ContextWindows(t *testing.T) {
	tests := []struct {
		name      string
		matchNums []int
		numLines  int
		contextN  int
		want      []lineRange
	}{
		{"no matches", nil, 10, 2, nil},
		{"single match mid-file", []int{5}, 10, 2, []lineRange{{2, 6}}},
		{"clamp at file start", []int{1}, 10, 2, []lineRange{{0, 2}}},
		{"clamp at file end", []int{10}, 10, 2, []lineRange{{7, 9}}},
		{"adjacent windows merge", []int{3, 4}, 10, 1, []lineRange{{1, 4}}},
		{"overlapping windows merge", []int{2, 5}, 10, 2, []lineRange{{0, 6}}},
		{"gap keeps windows separate", []int{2, 8}, 10, 1, []lineRange{{0, 2}, {6, 8}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := contextWindows(tt.matchNums, tt.numLines, tt.contextN)
			if len(got) != len(tt.want) {
				t.Fatalf("contextWindows(%v, %d, %d) = %v, want %v", tt.matchNums, tt.numLines, tt.contextN, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("contextWindows(%v, %d, %d) = %v, want %v", tt.matchNums, tt.numLines, tt.contextN, got, tt.want)
				}
			}
		})
	}
}

func TestGrep_CollectGrepLines(t *testing.T) {
	fileLines := []string{"aaa", "bbb", "aaa", "ccc", "aaa", "ddd", "eee"}
	re := regexp.MustCompile("aaa")

	t.Run("no matches yields nil (nothing retained)", func(t *testing.T) {
		if got := collectGrepLines([]string{"x", "y", "z"}, re, 2); got != nil {
			t.Errorf("collectGrepLines with no matches = %v, want nil", got)
		}
	})

	t.Run("context zero returns only match lines", func(t *testing.T) {
		got := collectGrepLines(fileLines, re, 0)
		wantLines := []int{1, 3, 5}
		if len(got) != len(wantLines) {
			t.Fatalf("len = %d, want %d", len(got), len(wantLines))
		}
		for i, ln := range wantLines {
			// context=0 lines have no ">" prefix, so isMatch stays false.
			if got[i].lineNum != ln || got[i].isMatch || got[i].content != "aaa" {
				t.Errorf("line %d = %+v, want lineNum=%d isMatch=false content=aaa", i, got[i], ln)
			}
		}
	})

	t.Run("context keeps only lines within range of a match", func(t *testing.T) {
		got := collectGrepLines(fileLines, re, 1)
		// matches at 1,3,5 merge into one window covering lines 1-6; line 7
		// ("eee") is outside every match's context and must not be retained.
		if len(got) != 6 {
			t.Fatalf("len = %d, want 6: %v", len(got), got)
		}
		for i, l := range got {
			if l.lineNum != i+1 {
				t.Errorf("line %d = %d, want %d", i, l.lineNum, i+1)
			}
		}
		for _, i := range []int{0, 2, 4} {
			if !got[i].isMatch {
				t.Errorf("line %d should be a match", got[i].lineNum)
			}
		}
		for _, i := range []int{1, 3, 5} {
			if got[i].isMatch {
				t.Errorf("line %d should be context, not a match", got[i].lineNum)
			}
		}
	})

	t.Run("context large file does not dump whole file", func(t *testing.T) {
		var manyLines []string
		for i := 0; i < 1000; i++ {
			manyLines = append(manyLines, "filler")
		}
		manyLines[0] = "needle here"
		manyLines[999] = "needle at the end"
		got := collectGrepLines(manyLines, regexp.MustCompile("needle"), 1)
		if len(got) != 4 {
			t.Fatalf("len = %d, want 4 (two lines around each of two matches)", len(got))
		}
		if got[0].lineNum != 1 || got[1].lineNum != 2 || got[2].lineNum != 999 || got[3].lineNum != 1000 {
			t.Errorf("unexpected retained lines: %+v", got)
		}
	})
}

func TestGrep_ContextCapBoundary(t *testing.T) {
	marker := "... (output truncated at 2 KiB)"

	t.Run("rendered output stays within the cap", func(t *testing.T) {
		dir := t.TempDir()
		var bigLines []string
		for i := 0; i < 5000; i++ {
			bigLines = append(bigLines, fmt.Sprintf("needle-%05d-%s", i, strings.Repeat("x", 60)))
		}
		if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Join(bigLines, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}

		tool := NewGrepTool(dir)
		result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"needle","context":2}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Error("result.IsError = true, want false")
		}
		block, ok := result.Blocks[0].(litellm.TextBlock)
		if !ok {
			t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
		}
		if !strings.HasSuffix(block.Text, marker) {
			t.Fatalf("expected truncation marker, got length %d", len(block.Text))
		}
		body := strings.TrimSuffix(block.Text, marker)
		if len(body) > maxGrepOutputBytes {
			t.Errorf("context output exceeds 2 KiB cap: %d bytes", len(body))
		}
		if len(body) < 1800 {
			t.Errorf("context output truncated too early: %d bytes", len(body))
		}
	})

	t.Run("first match line always included, consistent with context zero", func(t *testing.T) {
		dir := t.TempDir()
		huge := strings.Repeat("z", 5*1024)
		if err := os.WriteFile(filepath.Join(dir, "huge.txt"), []byte(huge+"\ntail line\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		tool := NewGrepTool(dir)
		result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"z{100}","context":1}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		block, ok := result.Blocks[0].(litellm.TextBlock)
		if !ok {
			t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
		}
		if !strings.HasSuffix(block.Text, marker) {
			t.Errorf("expected truncation marker in context output")
		}
		ctxBody := strings.TrimSuffix(block.Text, marker)
		if !strings.Contains(ctxBody, ">huge.txt:1:") {
			t.Errorf("expected first (oversized) match line to be included, got %q", ctxBody)
		}

		// context=0 keeps the same single oversized match line.
		result0, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"z{100}","context":0}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		block0, ok := result0.Blocks[0].(litellm.TextBlock)
		if !ok {
			t.Fatalf("block is %T, want TextBlock", result0.Blocks[0])
		}
		if strings.HasSuffix(block0.Text, marker) {
			t.Errorf("context=0 single match should not be marked truncated")
		}
		if ctxBody != ">"+block0.Text {
			t.Errorf("context output should be the match line prefixed with '>', got %q vs %q", ctxBody, block0.Text)
		}
	})
}

func TestGrep_ContextIgnoresFilesWithoutMatches(t *testing.T) {
	dir := t.TempDir()
	// A huge file with no matches must not be retained (or dumped) by the walk.
	huge := strings.Repeat("x", 2*1024*1024)
	if err := os.WriteFile(filepath.Join(dir, "huge-nomatch.txt"), []byte(huge), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "match.txt"), []byte("line1\nneedle here\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	result, err := tool.Call(context.Background(), json.RawMessage(`{"pattern":"needle","context":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("result.IsError = true, want false")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	if !strings.Contains(block.Text, "match.txt:1:line1") {
		t.Errorf("expected context line match.txt:1:line1, got:\n%s", block.Text)
	}
	if !strings.Contains(block.Text, ">match.txt:2:needle here") {
		t.Errorf("expected match line match.txt:2:needle here, got:\n%s", block.Text)
	}
	if !strings.Contains(block.Text, "match.txt:3:line3") {
		t.Errorf("expected context line match.txt:3:line3, got:\n%s", block.Text)
	}
	if strings.Contains(block.Text, "huge-nomatch.txt") {
		t.Errorf("output should not contain the no-match file")
	}
}

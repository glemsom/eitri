package compress

import (
	"strings"
	"testing"
)

func TestCompressFind_LessThanFiveFiles(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{"empty output", ""},
		{"single file", "main.go"},
		{"two files", "main.go\nutil.go"},
		{"three files", "a.go\nb.go\nc.go"},
		{"four files", "a.go\nb.go\nc.go\nd.go"},
		{"five files with only noise", "node_modules/foo\n.git/bar\ntarget/debug/baz\n__pycache__/x\n.DS_Store"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compressFind(tt.output)
			if got != nil {
				t.Errorf("compressFind(%q) = %v, want nil", tt.output, got)
			}
		})
	}
}

func TestCompressFind_Grouping(t *testing.T) {
	output := `./src/main.go
./src/util.go
./src/helpers.rs
./cmd/eitri/main.go
./cmd/eitri/config.go
./README.md`

	got := compressFind(output)
	if got == nil {
		t.Fatal("compressFind returned nil, expected compressed output")
	}

	// Should have header: "5F 3D:"
	if len(*got) < 5 {
		t.Fatalf("result too short: %q", *got)
	}

	// Check that directories appear in order.
	// We expect: ./cmd/eitri, ./src, .
	// Because splitPath strips "./" prefix, directories should be: "cmd/eitri", "src", "."
	if !strings.Contains(*got, "cmd/eitri/") {
		t.Errorf("expected 'cmd/eitri/' in result:\n%s", *got)
	}
	if !strings.Contains(*got, "src/") {
		t.Errorf("expected 'src/' in result:\n%s", *got)
	}
	if !strings.Contains(*got, "./") {
		t.Errorf("expected '.' (root) dir marker in result:\n%s", *got)
	}
}

func TestCompressFind_LeadingDotSlashStripped(t *testing.T) {
	// When paths have "./" prefix, it should be stripped for grouping.
	withDotSlash := `./src/main.go
./src/util.go
./src/helpers.rs
./src/cli.rs
./src/parser.rs
./README.md`
	withoutDotSlash := `src/main.go
src/util.go
src/helpers.rs
src/cli.rs
src/parser.rs
README.md`

	got1 := compressFind(withDotSlash)
	got2 := compressFind(withoutDotSlash)

	if got1 == nil || got2 == nil {
		t.Fatal("both should produce non-nil output")
	}

	// Both should produce the same output (deterministic).
	if *got1 != *got2 {
		t.Errorf("with ./ and without ./ should produce same output\ngot1:\n%s\ngot2:\n%s", *got1, *got2)
	}
}

func TestCompressFind_FilesInRoot(t *testing.T) {
	output := `README.md
LICENSE
Makefile
go.mod
go.sum
main.go`

	got := compressFind(output)
	if got == nil {
		t.Fatal("compressFind returned nil, expected compressed output")
	}

	if !strings.Contains(*got, "./") && !strings.Contains(*got, ". /") {
		t.Errorf("expected '.' (root) dir in result:\n%s", *got)
	}
}

func TestCompressFind_NoiseFiltering(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		skipped bool
	}{
		{"node_modules", "project/node_modules/package/index.js", true},
		{".git", "project/.git/config", true},
		{"target/debug", "project/target/debug/binary", true},
		{"target/release", "project/target/release/binary", true},
		{"__pycache__", "project/__pycache__/module.pyc", true},
		{".svelte-kit", "project/.svelte-kit/generated", true},
		{".next", "project/.next/build", true},
		{"dist", "project/dist/bundle.js", true},
		{".DS_Store", "project/.DS_Store", true},
		{"nested node_modules", "a/b/node_modules/c/d.js", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSkip(tt.path)
			if got != tt.skipped {
				t.Errorf("shouldSkip(%q) = %v, want %v", tt.path, got, tt.skipped)
			}
		})
	}
}

func TestCompressFind_OnlyNoise(t *testing.T) {
	// All noise paths should result in nil (fewer than 5 valid files).
	output := `node_modules/foo
.git/bar
target/debug/baz
__pycache__/x
.svelte-kit/y
.next/z
dist/w
.DS_Store`

	got := compressFind(output)
	if got != nil {
		t.Errorf("output with only noise should return nil, got:\n%s", *got)
	}
}

func TestCompressFind_MixedValidAndNoise(t *testing.T) {
	output := `src/main.go
src/util.go
src/helpers.rs
src/cli.rs
src/parser.rs
node_modules/package/index.js
.git/config
README.md`

	got := compressFind(output)
	if got == nil {
		t.Fatal("compressFind returned nil, expected compressed output")
	}

	// Should not contain noise paths.
	if strings.Contains(*got, "node_modules") {
		t.Errorf("result should not contain node_modules:\n%s", *got)
	}
	if strings.Contains(*got, ".git") {
		t.Errorf("result should not contain .git:\n%s", *got)
	}

	// Should contain valid files.
	if !strings.Contains(*got, "main.go") {
		t.Errorf("result should contain main.go:\n%s", *got)
	}
	if !strings.Contains(*got, "README.md") {
		t.Errorf("result should contain README.md:\n%s", *got)
	}
}

func TestCompressFind_PerDirectoryCap(t *testing.T) {
	// 12 files in one directory + one more in root = 13 total.
	output := `src/a.rs
src/b.rs
src/c.rs
src/d.rs
src/e.rs
src/f.rs
src/g.rs
src/h.rs
src/i.rs
src/j.rs
src/k.rs
src/l.rs
README.md`

	got := compressFind(output)
	if got == nil {
		t.Fatal("compressFind returned nil, expected compressed output")
	}

	// The src directory should show at most 10 files + "... +2 more".
	if !strings.Contains(*got, "+2 more") {
		t.Errorf("expected '+2 more' suffix in capped directory result:\n%s", *got)
	}
}

func TestCompressFind_ExactlyTenFiles(t *testing.T) {
	// Exactly 10 files in one directory + one root file = 11 total.
	output := `src/a.rs
src/b.rs
src/c.rs
src/d.rs
src/e.rs
src/f.rs
src/g.rs
src/h.rs
src/i.rs
src/j.rs
README.md`

	got := compressFind(output)
	if got == nil {
		t.Fatal("compressFind returned nil, expected compressed output")
	}

	// Should show all 10 files without truncation.
	if strings.Contains(*got, "+") {
		t.Errorf("expected no truncation for exactly 10 files:\n%s", *got)
	}
}

func TestCompressFind_SingleDirectoryManyFiles(t *testing.T) {
	// 15 files in one directory, no root files = 15 total.
	output := `src/a.rs
src/b.rs
src/c.rs
src/d.rs
src/e.rs
src/f.rs
src/g.rs
src/h.rs
src/i.rs
src/j.rs
src/k.rs
src/l.rs
src/m.rs
src/n.rs
src/o.rs`

	got := compressFind(output)
	if got == nil {
		t.Fatal("compressFind returned nil, expected compressed output")
	}

	// Should show header like "15F 1D:".
	if !strings.Contains(*got, "15F") {
		t.Errorf("expected header with 15F:\n%s", *got)
	}
	if !strings.Contains(*got, "1D") {
		t.Errorf("expected header with 1D:\n%s", *got)
	}

	// Should have truncation of +5 more.
	if !strings.Contains(*got, "+5 more") {
		t.Errorf("expected '+5 more' for 15 files:\n%s", *got)
	}
}

func TestCompressFind_Determinism(t *testing.T) {
	output := `src/main.go
src/util.go
src/cli.go
src/config.go
src/handler.go
cmd/eitri/main.go
cmd/eitri/root.go
README.md
LICENSE
Makefile`

	// Run multiple times, output must be identical.
	first := compressFind(output)
	if first == nil {
		t.Fatal("compressFind returned nil on first call")
	}

	for i := 0; i < 10; i++ {
		got := compressFind(output)
		if got == nil {
			t.Fatalf("compressFind returned nil on iteration %d", i)
		}
		if *got != *first {
			t.Fatalf("non-deterministic output on iteration %d:\nfirst:\n%s\niteration:\n%s", i, *first, *got)
		}
	}
}

func TestCompressFind_AntiInflation(t *testing.T) {
	// Output that's too small (< 5 files) returns nil (no inflation).
	got := compressFind("a.go\nb.go")
	if got != nil {
		t.Errorf("expected nil for small output, got %v", *got)
	}

	// For compressed output, ensure token estimate never exceeds original.
	// Use enough files in a single directory to show clear compression benefit.
	output := `src/main.go
src/util.go
src/helpers.rs
src/cli.rs
src/parser.rs
src/handler.go
src/middleware.go
src/router.go
src/config.go
src/model.go
src/cache.go
README.md`
	compressed := compressFind(output)
	if compressed == nil {
		t.Fatal("expected non-nil output")
	}

	origTokens := len(output) / 4
	compTokens := len(*compressed) / 4
	if compTokens >= origTokens {
		t.Errorf("compressed output uses more or equal tokens (%d >= %d)\noriginal (%d chars):\n%s\ncompressed (%d chars):\n%s",
			compTokens, origTokens, len(output), output, len(*compressed), *compressed)
	}
}

func TestCompressFind_SplitPath(t *testing.T) {
	tests := []struct {
		path    string
		wantDir string
		wantFile string
	}{
		{"src/util/helpers.rs", "src/util", "helpers.rs"},
		{"main.go", ".", "main.go"},
		{"./main.go", ".", "main.go"},
		{"./src/main.go", "src", "main.go"},
		{"a/b/c/d/file.txt", "a/b/c/d", "file.txt"},
		{"file.txt", ".", "file.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			dir, file := splitPath(tt.path)
			if dir != tt.wantDir {
				t.Errorf("splitPath(%q) dir = %q, want %q", tt.path, dir, tt.wantDir)
			}
			if file != tt.wantFile {
				t.Errorf("splitPath(%q) file = %q, want %q", tt.path, file, tt.wantFile)
			}
		})
	}
}

func TestCompressFind_OutputFormat(t *testing.T) {
	output := `src/main.go
src/util.go
README.md
LICENSE
Makefile
go.mod`

	got := compressFind(output)
	if got == nil {
		t.Fatal("compressFind returned nil")
	}

	// Header format: "NF ND:" where N is a number.
	if !strings.Contains(*got, "F ") && !strings.Contains(*got, "F:") {
		t.Errorf("expected header with file count:\n%s", *got)
	}
	if !strings.Contains(*got, "D:") && !strings.Contains(*got, "D ") {
		t.Errorf("expected header with dir count:\n%s", *got)
	}
}

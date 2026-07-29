package compress

import (
	"fmt"
	"strings"
	"testing"
)

func TestCompressLs_LessThanFiveLines(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{"empty output", ""},
		{"single line", "total 32"},
		{"two lines", "total 32\n-rw-r--r-- 1 user group 100 file.go"},
		{"three lines", "a\nb\nc"},
		{"four lines", "a\nb\nc\nd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compressLs(tt.output)
			if got != nil {
				t.Errorf("compressLs(%q) = %v, want nil", tt.output, *got)
			}
		})
	}
}

func TestCompressLs_LongFormat_MixedFilesAndDirs(t *testing.T) {
	output := `total 32
drwxr-xr-x  5 user group   160 Jul 28 12:00 .
drwxr-xr-x  8 user group   256 Jul 27 10:00 ..
-rw-r--r--  1 user group  4096 Jul 28 12:00 main.go
-rw-r--r--  1 user group   120 Jul 28 11:00 util.go
drwxr-xr-x  2 user group    64 Jul 28 10:00 src
-rw-r--r--  1 user group     0 Jul 28 09:00 empty.txt
-rw-r--r--  1 user group  1500 Jul 28 08:00 .env`

	got := compressLs(output)
	if got == nil {
		t.Fatal("compressLs returned nil, expected compressed output")
	}

	// Files should appear with formatted sizes.
	if !strings.Contains(*got, "main.go  4.1K") {
		t.Errorf("expected 'main.go  4.1K' in output:\n%s", *got)
	}
	if !strings.Contains(*got, "util.go  120") {
		t.Errorf("expected 'util.go  120' in output:\n%s", *got)
	}
	if !strings.Contains(*got, "empty.txt  0") {
		t.Errorf("expected 'empty.txt  0' in output:\n%s", *got)
	}
	if !strings.Contains(*got, ".env  1.5K") {
		t.Errorf("expected '.env  1.5K' in output:\n%s", *got)
	}

	// Directories should appear with trailing slash.
	if !strings.Contains(*got, "src/") {
		t.Errorf("expected 'src/' in output:\n%s", *got)
	}

	// Summary line.
	if !strings.Contains(*got, "4 files, 1 dirs") {
		t.Errorf("expected summary '4 files, 1 dirs' in output:\n%s", *got)
	}

	// Files should appear before dirs, with blank line between.
	lines := strings.Split(strings.TrimSpace(*got), "\n")
	foundBlank := false
	foundDir := false
	for _, line := range lines {
		if line == "" {
			foundBlank = true
		}
		if strings.HasSuffix(line, "/") {
			foundDir = true
		}
	}
	if !foundBlank {
		t.Errorf("expected blank line between files and dirs:\n%s", *got)
	}
	if !foundDir {
		t.Errorf("expected directory entries:\n%s", *got)
	}
}

func TestCompressLs_LongFormat_Dotfiles(t *testing.T) {
	// . and .. should be skipped; .env and .gitignore should be preserved.
	output := `total 16
drwxr-xr-x  4 user group   128 Jul 28 12:00 .
drwxr-xr-x  6 user group   192 Jul 27 10:00 ..
-rw-r--r--  1 user group   200 Jul 28 12:00 .env
-rw-r--r--  1 user group    50 Jul 28 11:00 .gitignore
-rw-r--r--  1 user group  1000 Jul 28 10:00 main.go`

	got := compressLs(output)
	if got == nil {
		t.Fatal("compressLs returned nil, expected compressed output")
	}

	if !strings.Contains(*got, ".env") {
		t.Errorf("expected .env to be preserved in output:\n%s", *got)
	}
	if !strings.Contains(*got, ".gitignore") {
		t.Errorf("expected .gitignore to be preserved in output:\n%s", *got)
	}

	// . and .. should not appear.
	if strings.Contains(*got, "\n./") || strings.Contains(*got, "\n../") {
		t.Errorf(". and .. should be skipped, but found in output:\n%s", *got)
	}
}

func TestCompressLs_LongFormat_HumanReadableSizesPassThrough(t *testing.T) {
	// When ls -lh is used, sizes like 4.0K, 1.2M, 0B should pass through as-is.
	output := `total 20
drwxr-xr-x  3 user group   160 Jul 28 12:00 .
drwxr-xr-x  5 user group   256 Jul 27 10:00 ..
-rw-r--r--  1 user group  4.0K Jul 28 12:00 main.go
-rw-r--r--  1 user group  1.2M Jul 28 11:00 bigfile.bin
-rw-r--r--  1 user group   0B Jul 28 10:00 empty.txt`

	got := compressLs(output)
	if got == nil {
		t.Fatal("compressLs returned nil, expected compressed output")
	}

	if !strings.Contains(*got, "main.go  4.0K") {
		t.Errorf("expected 'main.go  4.0K' (pass-through) in output:\n%s", *got)
	}
	if !strings.Contains(*got, "bigfile.bin  1.2M") {
		t.Errorf("expected 'bigfile.bin  1.2M' (pass-through) in output:\n%s", *got)
	}
	if !strings.Contains(*got, "empty.txt  0B") {
		t.Errorf("expected 'empty.txt  0B' (pass-through) in output:\n%s", *got)
	}
}

func TestCompressLs_LongFormat_TotalLineSkipped(t *testing.T) {
	output := `total 42
-rw-r--r--  1 user group  100 Jul 28 12:00 file.go`

	// Need >= 5 lines for compression to attempt, but only 1 valid file line.
	// With just one file line and no dirs, files+dirs won't be 0, so we get output.
	// Actually let's use a bigger output with enough lines.
	output = `total 42
-rw-r--r--  1 user group  100 Jul 28 12:00 a.go
-rw-r--r--  1 user group  200 Jul 28 12:00 b.go
-rw-r--r--  1 user group  300 Jul 28 12:00 c.go
-rw-r--r--  1 user group  400 Jul 28 12:00 d.go
-rw-r--r--  1 user group  500 Jul 28 12:00 e.go`

	got := compressLs(output)
	if got == nil {
		t.Fatal("compressLs returned nil, expected compressed output")
	}

	// "total" should not appear in compressed output.
	if strings.Contains(*got, "total") {
		t.Errorf("'total' line should be skipped but found in output:\n%s", *got)
	}
}

func TestCompressLs_LongFormat_DotAndDotDotSkipped(t *testing.T) {
	output := `total 24
drwxr-xr-x  4 user group   128 Jul 28 12:00 .
drwxr-xr-x  6 user group   192 Jul 27 10:00 ..
-rw-r--r--  1 user group   500 Jul 28 12:00 main.go
-rw-r--r--  1 user group   300 Jul 28 11:00 util.go
-rw-r--r--  1 user group   200 Jul 28 10:00 extra.go`

	got := compressLs(output)
	if got == nil {
		t.Fatal("compressLs returned nil, expected compressed output")
	}

	// . and .. should be completely absent.
	if strings.Contains(*got, "./") || strings.Contains(*got, "../") {
		t.Errorf("'.' and '..' entries should be skipped but found in output:\n%s", *got)
	}

	// Should have correct file count (3 files, 0 dirs).
	if !strings.Contains(*got, "3 files, 0 dirs") {
		t.Errorf("expected '3 files, 0 dirs' summary:\n%s", *got)
	}
}

func TestCompressLs_ShortFormat_FewerThanTenItems(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{"zero items", ""},
		{"one item", "file1"},
		{"five items", "file1\nfile2\nfile3\nfile4\nfile5"},
		{"nine items", "a b c d e f g h i"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compressLs(tt.output)
			if got != nil {
				t.Errorf("compressLs(%q) = %v, want nil", tt.output, *got)
			}
		})
	}
}

func TestCompressLs_ShortFormat_TenOrMoreItems(t *testing.T) {
	// Need at least 5 newline-separated lines to trigger compression.
	// Avoid lines starting with -, d, l, or "total " (triggers long-format detection).
	output := "x1 x2 x3\ny1 y2 y3\nz1 z2 z3\nw1 w2 w3\nv1 v2 v3"
	got := compressLs(output)
	if got == nil {
		t.Fatal("compressLs returned nil, expected compressed output")
	}
	// Should list items properly.
	if !strings.Contains(*got, "15 files, 0 dirs") {
		t.Errorf("expected '15 files, 0 dirs' summary, got:\n%s", *got)
	}
}

func TestCompressLs_ShortFormat_DirsListedSeparately(t *testing.T) {
	// Items with trailing / should be grouped as dirs.
	// Need at least 5 newline-separated lines.
	// Avoid names starting with -, d, l (triggers long-format detection).
	output := "file1.txt aaa/ file2.txt\nbbb/ file3.txt ccc/\nreadme.md notes.txt\nhello.txt world.go\ntest.go main.rs"
	got := compressLs(output)
	if got == nil {
		t.Fatal("compressLs returned nil, expected compressed output")
	}

	// Dirs should appear before files in short format.
	lines := strings.Split(strings.TrimSpace(*got), "\n")
	dirSection := true
	for _, line := range lines {
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, "/") {
			if !dirSection {
				t.Errorf("dirs should appear before files, but found dir after file section:\n%s", *got)
			}
		} else if !strings.Contains(line, "files, ") {
			// It's a file line (not summary).
			dirSection = false
		}
	}

	if !strings.Contains(*got, "aaa/") || !strings.Contains(*got, "bbb/") || !strings.Contains(*got, "ccc/") {
		t.Errorf("expected aaa/, bbb/, ccc/ in output:\n%s", *got)
	}
}

func TestCompressLs_ShortFormat_WrappingAt70Chars(t *testing.T) {
	// Create enough items with sufficiently long names to trigger wrapping.
	// Need at least 5 newline-separated lines.
	// Avoid lines starting with -, d, l, or "total " (triggers long-format detection).
	output := "aaaaaaaaaa bbbbbbbbbb cccccccccc\neeeeeeeeee ffffffffff gggggggggg\nhhhhhhhhhh iiiiiiiiii jjjjjjjjjj\nkkkkkkkkkk mmmmmmmmmm nnnnnnnnnn\noooooooooo"
	got := compressLs(output)
	if got == nil {
		t.Fatal("compressLs returned nil, expected compressed output")
	}

	// Check that no line (excluding trailing newline and summary) exceeds 70 chars.
	lines := strings.Split(strings.TrimSpace(*got), "\n")
	for _, line := range lines {
		if strings.Contains(line, "files, ") {
			continue
		}
		if line == "" {
			continue
		}
		if len(line) > 70 {
			t.Errorf("line exceeds 70 chars (%d chars): %q", len(line), line)
		}
	}
}

func TestCompressLs_AntiInflation_SmallOutputReturnsNil(t *testing.T) {
	// Very small output (fewer than 5 lines) returns nil.
	tests := []string{
		"",
		"file1",
		"file1\nfile2\nfile3",
	}
	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			got := compressLs(tt)
			if got != nil {
				t.Errorf("compressLs(%q) = %v, want nil", tt, *got)
			}
		})
	}
}

func TestCompressLs_AntiInflation_NotInflated(t *testing.T) {
	// Compressed output must never use more estimated tokens than original.
	output := `total 48
drwxr-xr-x  6 user group   192 Jul 28 12:00 .
drwxr-xr-x  8 user group   256 Jul 27 10:00 ..
-rw-r--r--  1 user group  4096 Jul 28 12:00 main.go
-rw-r--r--  1 user group   120 Jul 28 11:00 util.go
-rw-r--r--  1 user group  2048 Jul 28 10:00 helpers.go
drwxr-xr-x  2 user group    64 Jul 28 09:00 src
drwxr-xr-x  2 user group    64 Jul 28 08:00 pkg
-rw-r--r--  1 user group     0 Jul 28 07:00 empty.txt
-rw-r--r--  1 user group  1500 Jul 28 06:00 .env`

	got := compressLs(output)
	if got == nil {
		t.Fatal("compressLs returned nil, expected compressed output")
	}

	origTokens := len(output) / 4
	compTokens := len(*got) / 4
	if compTokens >= origTokens {
		t.Errorf("compressed output uses more or equal tokens (%d >= %d)\noriginal (%d chars):\n%s\ncompressed (%d chars):\n%s",
			compTokens, origTokens, len(output), output, len(*got), *got)
	}
}

func TestCompressLs_Deterministic(t *testing.T) {
	output := `total 32
drwxr-xr-x  4 user group   160 Jul 28 12:00 .
drwxr-xr-x  6 user group   256 Jul 27 10:00 ..
-rw-r--r--  1 user group  4096 Jul 28 12:00 main.go
-rw-r--r--  1 user group   120 Jul 28 11:00 util.go
drwxr-xr-x  2 user group    64 Jul 28 10:00 src
-rw-r--r--  1 user group  2000 Jul 28 09:00 readme.md
-rw-r--r--  1 user group     0 Jul 28 08:00 empty.txt`

	first := compressLs(output)
	if first == nil {
		t.Fatal("compressLs returned nil on first call")
	}

	for i := 0; i < 10; i++ {
		got := compressLs(output)
		if got == nil {
			t.Fatalf("compressLs returned nil on iteration %d", i)
		}
		if *got != *first {
			t.Fatalf("non-deterministic output on iteration %d:\nfirst:\n%s\niteration:\n%s", i, *first, *got)
		}
	}
}

func TestCompressLs_EdgeCase_EmptyOutput(t *testing.T) {
	got := compressLs("")
	if got != nil {
		t.Errorf("compressLs('') = %v, want nil", *got)
	}
}

func TestCompressLs_EdgeCase_OnlyDirectories(t *testing.T) {
	// Short format with only dirs. Need at least 5 newline-separated lines.
	// Avoid names starting with -, d, l (triggers long-format detection).
	output := "aaa/ bbb/\nccc/ eee/\nfff/ ggg/\nhhh/ iii/\njjj/ kkk/"
	got := compressLs(output)
	if got == nil {
		t.Fatal("compressLs returned nil, expected compressed output")
	}
	if !strings.Contains(*got, "aaa/") {
		t.Errorf("expected aaa/ in output:\n%s", *got)
	}
	if !strings.Contains(*got, "0 files, 10 dirs") {
		t.Errorf("expected '0 files, 10 dirs' summary:\n%s", *got)
	}
}

func TestCompressLs_EdgeCase_OnlyFiles(t *testing.T) {
	// Short format with only files. Need at least 5 newline-separated lines.
	output := "file1 file2\nfile3 file4\nfile5 file6\nfile7 file8\nfile9 file10"
	got := compressLs(output)
	if got == nil {
		t.Fatal("compressLs returned nil, expected compressed output")
	}
	if !strings.Contains(*got, "10 files, 0 dirs") {
		t.Errorf("expected '10 files, 0 dirs' summary:\n%s", *got)
	}
	// No dirs should be listed.
	if strings.Contains(*got, "/") {
		t.Errorf("unexpected directory entries in file-only output:\n%s", *got)
	}
}

func TestCompressLs_EdgeCase_OnlyDirsLongFormat(t *testing.T) {
	// Long format with only directories (no files).
	output := `total 16
drwxr-xr-x  3 user group   160 Jul 28 12:00 .
drwxr-xr-x  5 user group   256 Jul 27 10:00 ..
drwxr-xr-x  2 user group    64 Jul 28 12:00 src
drwxr-xr-x  2 user group    64 Jul 28 11:00 pkg
drwxr-xr-x  2 user group    64 Jul 28 10:00 bin
drwxr-xr-x  2 user group    64 Jul 28 09:00 lib
drwxr-xr-x  2 user group    64 Jul 28 08:00 test`

	got := compressLs(output)
	if got == nil {
		t.Fatal("compressLs returned nil, expected compressed output")
	}

	if !strings.Contains(*got, "src/") {
		t.Errorf("expected 'src/' in output:\n%s", *got)
	}
	if !strings.Contains(*got, "pkg/") {
		t.Errorf("expected 'pkg/' in output:\n%s", *got)
	}
	// Should have 0 files.
	if !strings.Contains(*got, "0 files, 5 dirs") {
		t.Errorf("expected '0 files, 5 dirs' summary:\n%s", *got)
	}
}

func TestCompressLs_EdgeCase_MixedDotfilesAndRegular(t *testing.T) {
	output := `total 24
drwxr-xr-x  4 user group   128 Jul 28 12:00 .
drwxr-xr-x  6 user group   192 Jul 27 10:00 ..
-rw-r--r--  1 user group   100 Jul 28 12:00 .hidden
-rw-r--r--  1 user group   200 Jul 28 11:00 visible.go
-rw-r--r--  1 user group   300 Jul 28 10:00 .config
-rw-r--r--  1 user group   400 Jul 28 09:00 main.go
-rw-r--r--  1 user group   500 Jul 28 08:00 .env`

	got := compressLs(output)
	if got == nil {
		t.Fatal("compressLs returned nil, expected compressed output")
	}

	// All dotfiles should be preserved (except . and ..).
	if !strings.Contains(*got, ".hidden") {
		t.Errorf("expected .hidden in output:\n%s", *got)
	}
	if !strings.Contains(*got, ".config") {
		t.Errorf("expected .config in output:\n%s", *got)
	}
	if !strings.Contains(*got, ".env") {
		t.Errorf("expected .env in output:\n%s", *got)
	}
	if !strings.Contains(*got, "visible.go") {
		t.Errorf("expected visible.go in output:\n%s", *got)
	}
	if !strings.Contains(*got, "main.go") {
		t.Errorf("expected main.go in output:\n%s", *got)
	}
	// . and .. should not appear.
	if strings.Contains(*got, "\n./") || strings.Contains(*got, "\n../") {
		t.Errorf("'.' and '..' should be skipped but found in output:\n%s", *got)
	}
}

func TestCompressLs_ShortFormat_WrappingPreservesItems(t *testing.T) {
	// Ensure no items are lost when wrapping.
	// Need at least 5 newline-separated lines.
	// Avoid names starting with -, d, l (triggers long-format detection).
	output := "item0 item1 item2\nitem3 item4 item5\nitem6 item7 item8\nitem9 item10 item11\nitem12 item13 item14"
	got := compressLs(output)
	if got == nil {
		t.Fatal("compressLs returned nil, expected compressed output")
	}

	// All items should be present.
	for i := 0; i < 15; i++ {
		name := fmt.Sprintf("item%d", i)
		if !strings.Contains(*got, name) {
			t.Errorf("expected %s in output:\n%s", name, *got)
		}
	}
}

func TestCompressLs_LongFormat_NoFilesNoDirs(t *testing.T) {
	// If after filtering only . and .. remain, result should be nil.
	output := `total 8
drwxr-xr-x  3 user group   160 Jul 28 12:00 .
drwxr-xr-x  5 user group   256 Jul 27 10:00 ..`
	got := compressLs(output)
	if got != nil {
		t.Errorf("compressLs with only . and .. should return nil, got %v", *got)
	}
}

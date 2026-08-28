package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "update golden files")

func TestHelpView_snapshot(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")

	for _, name := range supportedThemes {
		t.Run(name, func(t *testing.T) {
			got := helpView()

			goldenDir := filepath.Join("testdata", "help")
			goldenPath := filepath.Join(goldenDir, name+".txt")

			if *updateGolden {
				if err := os.MkdirAll(goldenDir, 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", goldenDir, err)
				}
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden %s: %v", goldenPath, err)
				}
				t.Logf("updated golden %s", goldenPath)
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with -update to create)", goldenPath, err)
			}
			if got != string(want) {
				t.Errorf("helpView(%q) mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, string(want))
			}
		})
	}
}

func TestHelpView_sections(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	for _, want := range []string{"COMMANDS", "KEYBINDINGS", "CONCEPTS"} {
		if !strings.Contains(got, want) {
			t.Errorf("helpView() missing section %q", want)
		}
	}
}

func TestHelpView_markdownHeaders(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	for _, want := range []string{"# COMMANDS", "# KEYBINDINGS", "# CONCEPTS"} {
		if !strings.Contains(got, want) {
			t.Errorf("helpView() missing Markdown header %q", want)
		}
	}
	for _, gone := range []string{"$ COMMANDS", "k KEYBINDINGS", "< CONCEPTS"} {
		if strings.Contains(got, gone) {
			t.Errorf("helpView() must drop legacy section prefix %q", gone)
		}
	}
}

func TestHelpView_codeSpans(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	for _, name := range []string{"/settings", "/copy", "/login", "/help"} {
		if want := "`" + name + "`"; !strings.Contains(got, want) {
			t.Errorf("helpView() missing code span %q", want)
		}
	}
	for _, name := range []string{"tab", "enter", "shift+enter", "?", "pgup/pgdn", "e", "E", "ctrl+e", "ctrl+x", "ctrl+z", "ctrl+s", "ctrl+o"} {
		if want := "`" + name + "`"; !strings.Contains(got, want) {
			t.Errorf("helpView() missing keybinding code span %q", want)
		}
	}
}

func TestHelpView_renderedHeaders(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	out, err := RenderMarkdown(helpView(), 80, "dark")
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	plain := ansiStrip(out)
	for _, want := range []string{"COMMANDS", "KEYBINDINGS", "CONCEPTS"} {
		if !strings.Contains(plain, want) {
			t.Errorf("rendered help missing section %q", want)
		}
	}
}

func TestHelpView_commands(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	for _, cmd := range []string{"/settings", "/copy", "/login", "/help"} {
		if !strings.Contains(got, cmd) {
			t.Errorf("helpView() missing command %q", cmd)
		}
	}
}

func TestHelpView_noSkills(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	if strings.Contains(got, "skill") {
		t.Errorf("helpView() must not mention skills, got:\n%s", got)
	}
}

func TestHelpView_alignedColumns(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	sections := []struct {
		header string
		descs  []string
	}{
		{"COMMANDS", []string{
			"open settings panel", "copy transcript to clipboard",
			"interactive provider login", "show this help message",
		}},
		{"CONCEPTS", []string{
			"e/E or ctrl+e expand or collapse all blocks",
			"tab to focus, enter to expand one block",
			"click and drag to select text",
			"stats, context, and model info",
		}},
	}

	for _, sec := range sections {
		lines := sectionLines(t, got, sec.header)
		assertAligned(t, sec.header, lines, sec.descs)
	}

	categories := []struct {
		name  string
		descs []string
	}{
		{"COMPOSER", []string{
			"navigate completion candidates; recall a prior/next prompt when the completion list is closed",
			"accept highlighted completion",
			"close completion list",
			"cycle block focus when composer is empty",
			"submit draft or toggle focused block when empty",
			"insert newline",
		}},
		{"NAVIGATION", []string{"show help", "scroll history"}},
		{"PANES", []string{"expand all blocks", "collapse all blocks", "toggle expanded view", "narrow pane", "widen pane"}},
		{"ACTIONS", []string{"open settings", "copy transcript"}},
	}
	for _, cat := range categories {
		lines := categoryLines(t, got, cat.name)
		assertAligned(t, cat.name, lines, cat.descs)
	}
}

func assertAligned(t *testing.T, label string, lines, descs []string) {
	t.Helper()
	col := -1
	for _, desc := range descs {
		found := false
		for _, ln := range lines {
			if i := strings.Index(ln, desc); i >= 0 {
				if col == -1 {
					col = i
				} else if i != col {
					t.Errorf("%s: description %q starts at col %d, want %d\nline: %q",
						label, desc, i, col, ln)
				}
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s: description %q not found in section\n%q", label, desc, strings.Join(lines, "\n"))
		}
	}
}

func TestHelpView_keybindingCategories(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	for _, want := range []string{
		"c COMPOSER", "n NAVIGATION", "p PANES", "a ACTIONS",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("helpView() missing keybinding category %q", want)
		}
	}
}

func TestHelpView_keybindingsComplete(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	sec := keybindingsSection(t, got)
	keys := []string{
		"`ctrl+s`", "`ctrl+o`", "`ctrl+e`", "`tab`", "`enter`", "`shift+enter`", "`e`", "`E`",
		"`?`", "`pgup/pgdn`", "`ctrl+x`", "`ctrl+z`",
	}
	for _, k := range keys {
		if n := strings.Count(sec, k); n != 1 {
			t.Errorf("keybinding %q appears %d times in KEYBINDINGS, want exactly once", k, n)
		}
	}
}

func keybindingsSection(t *testing.T, got string) string {
	t.Helper()
	lines := strings.Split(got, "\n")
	started := false
	var out []string
	for _, ln := range lines {
		if strings.Contains(ln, "KEYBINDINGS") {
			started = true
			continue
		}
		if !started {
			continue
		}
		if ln == "" && len(out) == 0 {
			continue // blank line right after the `# KEYBINDINGS` header
		}
		if ln == "" {
			return strings.Join(out, "\n")
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

func categoryLines(t *testing.T, got, name string) []string {
	t.Helper()
	lineOf := func(ln string) bool {
		return strings.HasSuffix(strings.TrimSpace(ln), " "+name)
	}
	lines := strings.Split(got, "\n")
	started := false
	var out []string
	for _, ln := range lines {
		if lineOf(ln) {
			started = true
			continue
		}
		if !started {
			continue
		}
		if ln == "" || lineOf(ln) || strings.Contains(ln, "KEYBINDINGS") ||
			strings.HasPrefix(strings.TrimSpace(ln), "-") {
			return out
		}
		out = append(out, strings.TrimPrefix(ln, "  "))
	}
	return out
}

func sectionLines(t *testing.T, got, header string) []string {
	t.Helper()
	lines := strings.Split(got, "\n")
	started := false
	var out []string
	for _, ln := range lines {
		if strings.Contains(ln, " "+header) {
			started = true
			continue
		}
		if !started {
			continue
		}
		if ln == "" && len(out) == 0 {
			continue
		}
		if ln == "" {
			break
		}
		out = append(out, strings.TrimPrefix(ln, "  "))
	}
	if len(out) == 0 {
		t.Fatalf("section %q not found in help output", header)
	}
	return out
}

func TestHelpView_noAnsiEscape(t *testing.T) {
	got := helpView()
	if strings.Contains(got, "\x1b[") {
		t.Errorf("helpView() must be escape-free plain text, got ANSI:\n%q", got)
	}
}

func TestHelpView_rendersClean(t *testing.T) {
	out, err := RenderMarkdown(helpView(), 80, "dark")
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	if strings.Contains(out, "1;38;") {
		t.Errorf("rendered help exposes raw escape params:\n%q", out)
	}
}

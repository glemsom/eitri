package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "update golden files")

// TestHelpView_snapshot asserts helpView renders correctly for every supported
// theme. Each theme's output is compared against a golden file under
// testdata/help/. Run with -update to regenerate golden files.
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

// TestHelpView_sections asserts the three canonical sections exist with
// their exact header names.
func TestHelpView_sections(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	for _, want := range []string{"COMMANDS", "KEYBINDINGS", "CONCEPTS"} {
		if !strings.Contains(got, want) {
			t.Errorf("helpView() missing section %q", want)
		}
	}
}

// TestHelpView_markdownHeaders asserts the section titles are Markdown `#`
// headers (issue #387) so the transcript's Markdown→ANSI pass renders them in
// the theme's accent color — not the old emoji-prefixed uppercase label rows.
func TestHelpView_markdownHeaders(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	for _, want := range []string{"# COMMANDS", "# KEYBINDINGS", "# CONCEPTS"} {
		if !strings.Contains(got, want) {
			t.Errorf("helpView() missing Markdown header %q", want)
		}
	}
	// The old section emoji prefixes are gone: a header is a `#` line, not a
	// glyph-prefixed run (issue #387).
	for _, gone := range []string{"$ COMMANDS", "k KEYBINDINGS", "< CONCEPTS"} {
		if strings.Contains(got, gone) {
			t.Errorf("helpView() must drop legacy section prefix %q", gone)
		}
	}
}

// TestHelpView_codeSpans asserts command and keybinding names are wrapped in
// backtick code spans (issue #387) so the Markdown pass renders them in code
// style, distinct from their descriptions.
func TestHelpView_codeSpans(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	for _, name := range []string{"/settings", "/copy", "/login", "/help"} {
		if want := "`" + name + "`"; !strings.Contains(got, want) {
			t.Errorf("helpView() missing code span %q", want)
		}
	}
	for _, name := range []string{"tab", "shift+enter", "?", "pgup/pgdn", "ctrl+e", "ctrl+x", "ctrl+z", "ctrl+s", "ctrl+o"} {
		if want := "`" + name + "`"; !strings.Contains(got, want) {
			t.Errorf("helpView() missing keybinding code span %q", want)
		}
	}
}

// TestHelpView_renderedHeaders asserts the stored Markdown headers actually
// survive the transcript's Markdown→ANSI pass, so the section titles render on
// screen instead of being dropped or left literal (issue #387).
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

// TestHelpView_commands lists the four built-in slash commands.
func TestHelpView_commands(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	for _, cmd := range []string{"/settings", "/copy", "/login", "/help"} {
		if !strings.Contains(got, cmd) {
			t.Errorf("helpView() missing command %q", cmd)
		}
	}
}

// TestHelpView_noSkills asserts no skill names leak into the help output.
func TestHelpView_noSkills(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	// The help surface must never list skills — it only documents built-in
	// commands, keybindings, and concepts.
	if strings.Contains(got, "skill") {
		t.Errorf("helpView() must not mention skills, got:\n%s", got)
	}
}

// TestHelpView_alignedColumns asserts each section's descriptions share one
// vertical ruler (issue #385): within a section the key/label cell is padded to
// the widest so every description starts at the same column. Within KEYBINDINGS
// each labeled category has its own shared ruler (issue #386), so alignment is
// checked per category.
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
			"expand/collapse all tool result cards",
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
		{"COMPOSER", []string{"toggle thinking", "insert newline"}},
		{"NAVIGATION", []string{"show help", "scroll history"}},
		{"PANES", []string{"toggle expanded view", "narrow pane", "widen pane"}},
		{"ACTIONS", []string{"open settings", "copy transcript"}},
	}
	for _, cat := range categories {
		lines := categoryLines(t, got, cat.name)
		assertAligned(t, cat.name, lines, cat.descs)
	}
}

// assertAligned asserts every desc appears in lines at the same starting column.
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

// TestHelpView_keybindingCategories asserts the KEYBINDINGS section groups its
// rows under at least three labeled category sub-headers (issue #386), each
// with an ASCII-fallback emoji prefix.
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

// TestHelpView_keybindingsComplete asserts every keybinding appears exactly once
// in the KEYBINDINGS section: none dropped, none duplicated (issue #386).
func TestHelpView_keybindingsComplete(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	sec := keybindingsSection(t, got)
	keys := []string{
		"ctrl+s", "ctrl+o", "ctrl+e", "tab", "shift+enter",
		"?", "pgup/pgdn", "ctrl+x", "ctrl+z",
	}
	for _, k := range keys {
		if n := strings.Count(sec, k); n != 1 {
			t.Errorf("keybinding %q appears %d times in KEYBINDINGS, want exactly once", k, n)
		}
	}
}

// keybindingsSection returns the KEYBINDINGS block (from its header to the
// trailing blank line) so uniqueness/count checks are scoped to the section.
// It skips the blank line that separates the Markdown `#` header from its body
// (issue #387) before collecting rows.
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

// categoryLines returns the keybinding rows between a labeled category header
// and the next category/section boundary, trimming the two-space row indent.
func categoryLines(t *testing.T, got, name string) []string {
	t.Helper()
	lineOf := func(ln string) bool {
		// A category header is `  <glyph> NAME`; the row lines that follow carry
		// the `  key  description` shape and never contain the bare name trailer.
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
		// Terminate at a blank line, the KEYBINDINGS header, or the next category
		// header — whichever closes the current category block.
		if ln == "" || lineOf(ln) || strings.Contains(ln, "KEYBINDINGS") ||
			strings.HasPrefix(strings.TrimSpace(ln), "-") {
			return out
		}
		out = append(out, strings.TrimPrefix(ln, "  "))
	}
	return out
}

// sectionLines returns the row lines of a /help section (content between the
// section header and the following blank separator), trimming the two-space
// indent used by every row. It skips the blank line that follows a Markdown
// `#` header (issue #387) before collecting rows.
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
		// Skip the blank line right after the Markdown `#` header.
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

// TestHelpView_noAnsiEscape asserts the help content is stored as escape-free
// plain text (issue #378): no ANSI sequences at all. Storing raw ANSI in the
// message is what got re-escaped as literal garbage by the Markdown pass and
// leaked into the clipboard copy, so its absence is the fix's contract.
func TestHelpView_noAnsiEscape(t *testing.T) {
	got := helpView()
	if strings.Contains(got, "\x1b[") {
		t.Errorf("helpView() must be escape-free plain text, got ANSI:\n%q", got)
	}
}

// TestHelpView_rendersClean verifies the stored help text survives the
// transcript's Markdown→ANSI pass without producing visible escape garbage:
// the raw `1;38;...` parameter runs that the old ANSI-embedded help exposed.
func TestHelpView_rendersClean(t *testing.T) {
	out, err := RenderMarkdown(helpView(), 80, "dark")
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	if strings.Contains(out, "1;38;") {
		t.Errorf("rendered help exposes raw escape params:\n%q", out)
	}
}

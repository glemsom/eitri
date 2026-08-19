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

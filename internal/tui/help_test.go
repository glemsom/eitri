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
			th := themeFor(name)
			got := helpView(th)

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
	th := renderSurfaceTestTheme()
	got := helpView(th)

	for _, want := range []string{"COMMANDS", "KEYBINDINGS", "CONCEPTS"} {
		if !strings.Contains(got, want) {
			t.Errorf("helpView() missing section %q", want)
		}
	}
}

// TestHelpView_commands lists the four built-in slash commands.
func TestHelpView_commands(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := renderSurfaceTestTheme()
	got := helpView(th)

	for _, cmd := range []string{"/settings", "/copy", "/login", "/help"} {
		if !strings.Contains(got, cmd) {
			t.Errorf("helpView() missing command %q", cmd)
		}
	}
}

// TestHelpView_noSkills asserts no skill names leak into the help output.
func TestHelpView_noSkills(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := renderSurfaceTestTheme()
	got := helpView(th)

	// The help surface must never list skills — it only documents built-in
	// commands, keybindings, and concepts.
	if strings.Contains(got, "skill") {
		t.Errorf("helpView() must not mention skills, got:\n%s", got)
	}
}

// TestHelpView_themeStyles asserts section headers use headerStyle (accent)
// and body items use statusStyle (faint) by checking the real theme renders
// ANSI escape sequences from those styles.
func TestHelpView_themeStyles(t *testing.T) {
	th := defaultTheme
	got := helpView(th)

	// headerStyle renders bold + accent foreground; statusStyle renders faint.
	// On a real theme both produce ANSI escapes. The plain test theme produces
	// no escapes, so we test the real theme here.
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("helpView(defaultTheme) produced no ANSI escapes — headerStyle/statusStyle not applied")
	}
}

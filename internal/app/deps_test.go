package app

import (
	"errors"
	"strings"
	"testing"
)

// lookup stubs the executable-lookup seam: names in found resolve, everything
// else is reported missing.
func lookup(found ...string) func(string) (string, error) {
	ok := make(map[string]bool, len(found))
	for _, f := range found {
		ok[f] = true
	}
	return func(name string) (string, error) {
		if ok[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("executable not found: " + name)
	}
}

func declaredDependencyNames() []string {
	names := make([]string, len(declaredDependencies))
	for i, d := range declaredDependencies {
		names[i] = d.name
	}
	return names
}

func TestCheckDependenciesRequiresXDGOpen(t *testing.T) {
	present := []string{"bwrap", "bash", "rg", "curl", "lynx", "patch", "python3", "git", "jq"}

	err := checkDependencies(lookup(present...))
	if err == nil {
		t.Fatal("checkDependencies() error = nil, want xdg-open to be required")
	}
	de, ok := err.(*DependencyError)
	if !ok {
		t.Fatalf("error type = %T, want *DependencyError", err)
	}
	if strings.Join(de.Missing, ",") != "xdg-open" {
		t.Fatalf("DependencyError.Missing = %v, want [xdg-open]", de.Missing)
	}
	if msg := err.Error(); !strings.Contains(msg, "xdg-utils") {
		t.Fatalf("error %q lacks the xdg-utils installation package", msg)
	}
}

func TestCheckDependenciesAllPresent(t *testing.T) {
	names := declaredDependencyNames()
	if err := checkDependencies(lookup(names...)); err != nil {
		t.Fatalf("checkDependencies() error = %v, want nil when every declared tool is present", err)
	}
}

func TestCheckDependenciesReportsEveryMissingTool(t *testing.T) {
	present := []string{"bwrap", "bash", "rg", "curl"}
	missing := []string{"lynx", "patch", "python3", "git", "jq", "xdg-open"}

	err := checkDependencies(lookup(present...))
	if err == nil {
		t.Fatal("checkDependencies() error = nil, want a fatal missing-dependencies error")
	}
	if !errors.Is(err, ErrMissingDependencies) {
		t.Fatalf("errors.Is(err, ErrMissingDependencies) = false, error = %v", err)
	}
	de, ok := err.(*DependencyError)
	if !ok {
		t.Fatalf("error type = %T, want *DependencyError", err)
	}
	if strings.Join(de.Missing, ",") != strings.Join(missing, ",") {
		t.Fatalf("DependencyError.Missing = %v, want exactly %v (not just the first missing tool)", de.Missing, missing)
	}
}

func TestCheckDependenciesErrorCarriesPerDistroInstallHints(t *testing.T) {
	err := checkDependencies(lookup())
	if err == nil {
		t.Fatal("checkDependencies() error = nil, want a fatal missing-dependencies error")
	}
	msg := err.Error()
	for _, name := range declaredDependencyNames() {
		if !strings.Contains(msg, name) {
			t.Errorf("error %q does not name the missing tool %q", msg, name)
		}
	}
	for _, snippet := range []string{"sudo apt install", "sudo dnf install", "sudo pacman -S"} {
		if !strings.Contains(msg, snippet) {
			t.Errorf("error %q lacks a %q install hint", msg, snippet)
		}
	}
}

func TestCheckDependenciesErrorNamesExecutableAndPackage(t *testing.T) {
	// The executable and its distro package can differ (bwrap → bubblewrap,
	// rg → ripgrep); the error must name both so a human knows what to install.
	err := checkDependencies(lookup())
	if err == nil {
		t.Fatal("checkDependencies() error = nil, want a fatal missing-dependencies error")
	}
	msg := err.Error()
	for _, pair := range [][2]string{{"bwrap", "bubblewrap"}, {"rg", "ripgrep"}} {
		if !strings.Contains(msg, pair[0]) || !strings.Contains(msg, pair[1]) {
			t.Errorf("error %q does not name both executable %q and package %q", msg, pair[0], pair[1])
		}
	}
}

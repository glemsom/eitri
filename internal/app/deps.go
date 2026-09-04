package app

import (
	"errors"
	"fmt"
	"strings"
)

// The declared toolset: the hard substrate (bwrap, bash) plus the declared
// tools (rg, curl, lynx, patch, python3, git, xdg-open) that the single fixed
// system prompt promises unconditionally. A missing name here is fatal at boot:
// the run refuses to start rather than let the agent reach for a tool that
// cannot exist.

// dependency is one declared executable and the distro package that provides
// it; the package name can differ from the executable name (bwrap is shipped
// as bubblewrap, rg as ripgrep).
type dependency struct {
	name    string
	pkgName string
}

var declaredDependencies = []dependency{
	{name: "bwrap", pkgName: "bubblewrap"},
	{name: "bash", pkgName: "bash"},
	{name: "rg", pkgName: "ripgrep"},
	{name: "curl", pkgName: "curl"},
	{name: "lynx", pkgName: "lynx"},
	{name: "patch", pkgName: "patch"},
	{name: "python3", pkgName: "python3"},
	{name: "git", pkgName: "git"},
	{name: "jq", pkgName: "jq"},
	{name: "xdg-open", pkgName: "xdg-utils"},
}

// distroInstallers maps each supported distro family to its package install
// command prefix, so the refusal message can carry a per-distro hint.
var distroInstallers = []struct {
	distro string
	cmd    string
}{
	{"Debian/Ubuntu", "sudo apt install"},
	{"Fedora", "sudo dnf install"},
	{"Arch", "sudo pacman -S"},
}

// ErrMissingDependencies is the sentinel wrapped by *DependencyError when one
// or more declared dependencies are absent at boot: Eitri refuses to run
// because its prompt promises these tools unconditionally.
var ErrMissingDependencies = errors.New("missing declared dependencies")

// DependencyError reports every declared dependency that failed the boot
// executable lookup in a single pass — never just the first miss — each with
// a per-distro install hint.
type DependencyError struct {
	Missing []string
}

// Error renders one line per missing tool, naming the executable, its distro
// package when they differ, and the install command for each distro family.
func (e *DependencyError) Error() string {
	var b strings.Builder
	b.WriteString("missing required tools — Eitri refuses to run unless its declared toolset is installed:")
	for _, name := range e.Missing {
		pkg := name
		for _, d := range declaredDependencies {
			if d.name == name {
				pkg = d.pkgName
				break
			}
		}
		hints := make([]string, 0, len(distroInstallers))
		for _, in := range distroInstallers {
			hints = append(hints, fmt.Sprintf("%s: %s %s", in.distro, in.cmd, pkg))
		}
		label := name
		if pkg != name {
			label = fmt.Sprintf("%s (%s)", name, pkg)
		}
		fmt.Fprintf(&b, "\n  - %s: %s", label, strings.Join(hints, "; "))
	}
	return b.String()
}

// Unwrap exposes the ErrMissingDependencies sentinel to errors.Is.
func (e *DependencyError) Unwrap() error { return ErrMissingDependencies }

// checkDependencies resolves every declared dependency through the injectable
// executable-lookup seam and returns a *DependencyError naming every miss, or
// nil when the full declared toolset is present.
func checkDependencies(lookPath func(name string) (string, error)) error {
	var missing []string
	for _, d := range declaredDependencies {
		if _, err := lookPath(d.name); err != nil {
			missing = append(missing, d.name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &DependencyError{Missing: missing}
}

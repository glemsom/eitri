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
// as bubblewrap, rg as ripgrep). yoloExempt marks a substrate that the
// unsandboxed (--yolo-unsafe) backend bypasses, so it is not required then.
type dependency struct {
	name       string
	pkgName    string
	yoloExempt bool
}

var declaredDependencies = []dependency{
	{name: "bwrap", pkgName: "bubblewrap", yoloExempt: true},
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

var distroInstallers = []struct {
	distro string
	cmd    string
}{
	{"Debian/Ubuntu", "sudo apt install"},
	{"Fedora", "sudo dnf install"},
	{"Arch", "sudo pacman -S"},
}

var ErrMissingDependencies = errors.New("missing declared dependencies")

// DependencyError reports every declared dependency that failed the boot
// executable lookup in a single pass — never just the first miss — each with
// a per-distro install hint.
type DependencyError struct {
	Missing []string
}

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

func (e *DependencyError) Unwrap() error { return ErrMissingDependencies }

func checkDependencies(lookPath func(name string) (string, error), yolo bool) error {
	var missing []string
	for _, d := range declaredDependencies {
		if yolo && d.yoloExempt {
			continue
		}
		if _, err := lookPath(d.name); err != nil {
			missing = append(missing, d.name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &DependencyError{Missing: missing}
}

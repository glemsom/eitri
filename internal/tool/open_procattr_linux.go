//go:build linux

package tool

import "syscall"

// openProcAttr returns the process attribute that detaches xdg-open into its
// own process group so a SIGINT/SIGTERM to the foreground group (e.g. Ctrl+C)
// never kills the freshly-spawned browser (ADR-0026).
func openProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

//go:build !linux

package tool

import "syscall"

// openProcAttr is a no-op on non-Linux platforms; the Linux launcher is the only
// supported path (ADR-0026).
func openProcAttr() *syscall.SysProcAttr {
	return nil
}

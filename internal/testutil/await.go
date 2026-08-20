// Package testutil holds small shared helpers for the test suite.
package testutil

import "time"

// DefaultTimeout bounds how long Await waits for a signal before failing the test.
const DefaultTimeout = 3 * time.Second

// Fataler is the slice of testing.TB that Await needs.
type Fataler interface {
	Fatalf(format string, args ...any)
	Helper()
}

// Await blocks until sig fires, then returns.
func Await(f Fataler, name string, sig <-chan struct{}) {
	f.Helper()
	await(f, name, sig, DefaultTimeout)
}

// await is Await with an explicit timeout so the timeout path can be exercised with a short deadline.
func await(f Fataler, name string, sig <-chan struct{}, timeout time.Duration) {
	f.Helper()
	select {
	case <-sig:
	case <-time.After(timeout):
		f.Fatalf("timed out after %s waiting for %s", timeout, name)
	}
}

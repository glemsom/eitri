// Package testutil holds small shared helpers for the test suite.
//
// It lives under internal/ so engine and tui tests can import it for the same
// await semantics instead of each re-deriving a chan-select + timeout.
package testutil

import "time"

// DefaultTimeout bounds how long Await waits for a signal before failing the
// test. One shared deadline keeps the meaning of a timeout identical across
// packages instead of each test re-deriving its own.
const DefaultTimeout = 3 * time.Second

// Fataler is the slice of testing.TB that Await needs. Passing the concrete
// *testing.T satisfies it; keeping it an interface lets the timeout path be
// asserted with a fake instead of aborting the test goroutine.
type Fataler interface {
	Fatalf(format string, args ...any)
	Helper()
}

// Await blocks until sig fires, then returns. If the signal never arrives
// before DefaultTimeout it fails f with the channel name, so a stranded wait
// is identifiable in the log rather than hanging the suite.
func Await(f Fataler, name string, sig <-chan struct{}) {
	f.Helper()
	await(f, name, sig, DefaultTimeout)
}

// await is Await with an explicit timeout so the timeout path can be exercised
// with a short deadline.
func await(f Fataler, name string, sig <-chan struct{}, timeout time.Duration) {
	f.Helper()
	select {
	case <-sig:
	case <-time.After(timeout):
		f.Fatalf("timed out after %s waiting for %s", timeout, name)
	}
}

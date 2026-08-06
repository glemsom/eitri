// Package testutil provides reusable helpers for eitri's tests.
package testutil

import (
	"fmt"
	"runtime"
	"strings"
	"time"
)

// goroutineLeakT is the subset of *testing.T that the GoroutineLeakGuard needs.
// Both *testing.T and test-local recording fakes satisfy it, keeping the guard
// unit-testable without a real *testing.T.
type goroutineLeakT interface {
	Helper()
	Cleanup(func())
	// Errorf marks the test failed but lets it continue, so in-test service
	// tear-down (deferred Close/cancel calls) can still drain any lingering
	// goroutines after the guard reports.
	Errorf(string, ...any)
}

// leakGuardOptions holds GoroutineLeakGuard tuning knobs.
type leakGuardOptions struct {
	settleWindow time.Duration // how long cleanup waits for goroutines to return to baseline
	pollInterval time.Duration // how often cleanup re-checks the goroutine count
	settleBefore time.Duration // pre-baseline settle so earlier tests drain
	tolerance    int           // acceptable goroutine-count slack above baseline
}

// LeakGuardOption configures a GoroutineLeakGuard.
type LeakGuardOption func(*leakGuardOptions)

// WithSettleWindow sets how long the guard's cleanup polls for the goroutine
// count to settle back to baseline before reporting a leak. It should be at
// least as long as the longest background-retention window any service under
// test uses (the run service retains completed runs for 5s), so cleanly-
// scheduled teardown is not mistaken for a leak.
func WithSettleWindow(d time.Duration) LeakGuardOption {
	return func(o *leakGuardOptions) { o.settleWindow = d }
}

// WithPollInterval sets how often the guard re-checks the goroutine count
// during the settle window.
func WithPollInterval(d time.Duration) LeakGuardOption {
	return func(o *leakGuardOptions) { o.pollInterval = d }
}

// WithTolerance sets how much goroutine-count slack above the baseline is
// tolerated before the guard reports. Race-detector and test-harness internals
// can occasionally hold a couple of extra goroutines transiently; a small
// tolerance avoids false positives while still catching genuine accumulation.
func WithTolerance(n int) LeakGuardOption {
	return func(o *leakGuardOptions) { o.tolerance = n }
}

const (
	defaultSettleWindow = 6 * time.Second
	defaultPollInterval = 25 * time.Millisecond
	defaultSettleBefore = 100 * time.Millisecond
)

// GoroutineLeakGuard detects goroutines that a service under test started but
// failed to stop by the time the test tears down. It is the enforcement half of
// the shutdown audit from issue #1127: tests that start and stop services
// repeatedly register a guard so an accumulating background worker (a service
// loop, ticker, session/subscription goroutine, or LLM client) fails the test
// instead of silently leaking into the next case.
//
// Design notes:
//
//   - The guard snapshots baseline = runtime.NumGoroutine() at creation and, at
//     cleanup, polls until the live count settles back to baseline (within a
//     configurable tolerance). On a real leak the count never settles and the
//     guard reports.
//   - Register the guard FIRST among the test's t.Cleanup hooks so its check
//     runs last (cleanups run LIFO), after fixture servers/clients have been
//     closed and their goroutines drained. This keeps the baseline meaningful.
//   - It reports via Errorf (not Fatalf) so deferred shutdown still runs, and
//     on failure it dumps the live goroutine stacks for attribution.
type GoroutineLeakGuard struct {
	t         goroutineLeakT
	baseline  int
	tolerance int
	settle    time.Duration
	interval  time.Duration
}

// NewGoroutineLeakGuard registers a cleanup on t that verifies the goroutine
// count settles back to its baseline.
func NewGoroutineLeakGuard(t goroutineLeakT, opts ...LeakGuardOption) *GoroutineLeakGuard {
	t.Helper()
	o := leakGuardOptions{
		settleWindow: defaultSettleWindow,
		pollInterval: defaultPollInterval,
		settleBefore: defaultSettleBefore,
	}
	for _, opt := range opts {
		opt(&o)
	}
	if o.settleBefore > 0 {
		// Let goroutines left by earlier tests drain before snapshotting baseline.
		time.Sleep(o.settleBefore)
	}
	g := &GoroutineLeakGuard{
		t:         t,
		baseline:  runtime.NumGoroutine(),
		settle:    o.settleWindow,
		interval:  o.pollInterval,
		tolerance: o.tolerance,
	}
	if g.settle <= 0 {
		g.settle = defaultSettleWindow
	}
	if g.interval <= 0 {
		g.interval = defaultPollInterval
	}
	t.Cleanup(g.runCheck)
	return g
}

// runCheck is the cleanup the guard registers. It polls until goroutines settle
// back to baseline or the settle window elapses.
func (g *GoroutineLeakGuard) runCheck() {
	g.t.Helper()
	deadline := time.Now().Add(g.settle)
	for {
		runtime.GC()
		if runtime.NumGoroutine() <= g.baseline+g.tolerance {
			return
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(g.interval)
	}
	live := runtime.NumGoroutine()
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	var b strings.Builder
	fmt.Fprintf(&b, "goroutine leak: %d live goroutines after cleanup, baseline was %d (tolerance %d); a service under test likely failed to stop a background worker\ncurrent goroutine stacks:\n%s",
		live, g.baseline, g.tolerance, strings.TrimRight(string(buf[:n]), "\n"))
	g.t.Errorf("%s", b.String())
}

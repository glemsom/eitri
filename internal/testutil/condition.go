// Package testutil provides reusable helpers for eitri's tests.
package testutil

import (
	"errors"
	"fmt"
	"time"
)

// ErrTimeout is returned by WaitForConditionOr when the deadline passes
// before the condition becomes true.
var ErrTimeout = errors.New("condition not satisfied within timeout")

// WaitForCondition polls eval() every interval until it returns true or the
// deadline passes. On timeout it fails the test with a descriptive message.
//
// eval is always called at least once. The poll interval is configurable so
// callers can trade responsiveness against CPU churn.
func WaitForCondition(t interface {
	Helper()
	Fatalf(string, ...any)
}, interval time.Duration, timeout time.Duration, eval func() bool) {
	t.Helper()
	_, err := WaitForConditionOr(t, interval, timeout, func() (struct{}, bool) {
		if eval() {
			return struct{}{}, true
		}
		return struct{}{}, false
	})
	if err != nil {
		t.Fatalf("wait-for-condition failed: %v", err)
	}
}

// WaitForConditionOr polls eval() every interval until it reports a satisfied
// condition, returning the observed value. On timeout it returns the last
// observed value wrapped with ErrTimeout instead of failing the test, so
// callers can assert against that final state.
func WaitForConditionOr[T any](t interface {
	Helper()
}, interval time.Duration, timeout time.Duration, eval func() (T, bool)) (T, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)

	var last T
	first := true
	for first || time.Now().Before(deadline) {
		first = false
		value, ok := eval()
		last = value
		if ok {
			return value, nil
		}
		if interval <= 0 {
			break
		}
		time.Sleep(interval)
	}
	return last, fmt.Errorf("%w after %v (final value: %v)", ErrTimeout, timeout, last)
}

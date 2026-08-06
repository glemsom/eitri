package testutil_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/testutil"
)

func TestWaitForConditionPassesWithinDeadline(t *testing.T) {
	var n atomic.Int32
	eval := func() bool { return n.Add(1) >= 3 }
	testutil.WaitForCondition(t, time.Millisecond, time.Second, eval)
	if got := n.Load(); got < 3 {
		t.Fatalf("eval called %d times, want >= 3", got)
	}
}

func TestWaitForConditionAlreadyTrue(t *testing.T) {
	count := 0
	testutil.WaitForCondition(t, time.Millisecond, time.Second, func() bool {
		count++
		return true
	})
	if count != 1 {
		t.Fatalf("eval called %d times, want 1", count)
	}
}

func TestWaitForConditionTimesOutAndFails(t *testing.T) {
	fake := &fakeT{}
	testutil.WaitForCondition(fake, time.Millisecond, 20*time.Millisecond, func() bool { return false })
	if fake.failed == nil {
		t.Fatal("expected test failure on timeout, got none")
	}
	if got := fake.failed.Error(); got == "" {
		t.Fatal("expected non-empty failure message")
	}
}

func TestWaitForConditionOrReturnsObservedValue(t *testing.T) {
	var n atomic.Int32
	got, err := testutil.WaitForConditionOr(t, time.Millisecond, time.Second, func() (string, bool) {
		v := n.Add(1)
		if v >= 3 {
			return "done", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "done" {
		t.Fatalf("got %q, want \"done\"", got)
	}
}

func TestWaitForConditionOrTimesOutReturnsLastValue(t *testing.T) {
	last := 7
	got, err := testutil.WaitForConditionOr[int](t, time.Millisecond, 20*time.Millisecond, func() (int, bool) {
		return last, false
	})
	if err == nil {
		t.Fatal("expected error on timeout, got none")
	}
	if got != 7 {
		t.Fatalf("got last observed %d, want 7", got)
	}
	if !errors.Is(err, testutil.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

func TestWaitForConditionOrSucceedsFirstEval(t *testing.T) {
	got, err := testutil.WaitForConditionOr[int](t, time.Millisecond, time.Second, func() (int, bool) {
		return 42, true
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
}

// fakeT implements testing.TB enough to observe a timeout failure.
type fakeT struct {
	failed error
}

func (f *fakeT) Helper()                             {}
func (f *fakeT) Fail()                               {}
func (f *fakeT) FailNow()                            {}
func (f *fakeT) Log(args ...any)                     {}
func (f *fakeT) Logf(format string, args ...any)     {}
func (f *fakeT) Fatal(args ...any)                   { f.failed = errors.New("fatal") }
func (f *fakeT) Fatalf(format string, args ...any)   { f.failed = errors.New("fatalf") }
func (f *fakeT) Error(args ...any)                   {}
func (f *fakeT) Errorf(format string, args ...any)   {}
func (f *fakeT) Cleanup(func())                      {}
func (f *fakeT) Name() string                        { return "fake" }
func (f *fakeT) Setenv(key, value string)            {}
func (f *fakeT) TempDir() string                     { return "" }
func (f *fakeT) Parallel()                           {}
func (f *fakeT) Run(string, func(t *testing.T)) bool { return true }
func (f *fakeT) Skip(args ...any)                    {}
func (f *fakeT) SkipNow()                            {}
func (f *fakeT) Skipf(format string, args ...any)    {}
func (f *fakeT) Skipped() bool                       { return false }
func (f *fakeT) Context() context.Context            { return context.Background() }

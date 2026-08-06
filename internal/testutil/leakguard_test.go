package testutil_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/testutil"
)

// recordingT records cleanup funcs and failure calls so a leak guard can be
// driven through a full lifecycle without a real *testing.T.
type recordingT struct {
	cleanups []func()
	fatalfs  []string
	errorfs  []string
}

func (r *recordingT) Helper()          {}
func (r *recordingT) Cleanup(f func()) { r.cleanups = append(r.cleanups, f) }
func (r *recordingT) Fatalf(format string, args ...any) {
	r.fatalfs = append(r.fatalfs, sprintf(format, args...))
}
func (r *recordingT) Errorf(format string, args ...any) {
	r.errorfs = append(r.errorfs, sprintf(format, args...))
}

func sprintf(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

func (r *recordingT) runCleanups() {
	for i := len(r.cleanups) - 1; i >= 0; i-- {
		r.cleanups[i]()
	}
}

func TestNewGoroutineLeakGuard_SettlesBack_NoFailure(t *testing.T) {
	rec := &recordingT{}
	guard := testutil.NewGoroutineLeakGuard(rec, testutil.WithSettleWindow(200*time.Millisecond))

	// Nothing is leaked; nothing extra spawned.
	rec.runCleanups()
	if len(rec.fatalfs) != 0 || len(rec.errorfs) != 0 {
		t.Fatalf("clean guard reported a leak: fatalfs=%v errorfs=%v", rec.fatalfs, rec.errorfs)
	}
	_ = guard
}

func TestNewGoroutineLeakGuard_DetectsLeakedGoroutine(t *testing.T) {
	rec := &recordingT{}
	guard := testutil.NewGoroutineLeakGuard(rec, testutil.WithSettleWindow(100*time.Millisecond))

	// Leak a goroutine that never exits until we close it after asserting.
	block := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-block // held open; represents a leaked service goroutine
	}()

	rec.runCleanups()
	if len(rec.fatalfs) == 0 && len(rec.errorfs) == 0 {
		t.Fatalf("expected the guard to flag the leaked goroutine")
	}
	// Release the goroutine so the suite can exit cleanly.
	close(block)
	wg.Wait()
	_ = guard
}

func TestNewGoroutineLeakGuard_StartedAndStopped_Passes(t *testing.T) {
	rec := &recordingT{}
	guard := testutil.NewGoroutineLeakGuard(rec, testutil.WithSettleWindow(300*time.Millisecond))

	// Start a goroutine that exits on signal, then signal it before cleanup.
	turnOff := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-turnOff
	}()
	close(turnOff)
	wg.Wait()

	rec.runCleanups()
	if len(rec.fatalfs) != 0 || len(rec.errorfs) != 0 {
		t.Fatalf("cleanly-stopped goroutine flagged as leak: fatalfs=%v errorfs=%v", rec.fatalfs, rec.errorfs)
	}
	_ = guard
}

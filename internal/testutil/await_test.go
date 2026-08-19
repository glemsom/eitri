package testutil

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeFataler records the fatal call instead of aborting the goroutine, so the
// timeout path of Await is assertable directly.
type fakeFataler struct {
	fatalMsg string
}

func (f *fakeFataler) Fatalf(format string, args ...any) { f.fatalMsg = fmt.Sprintf(format, args...) }
func (f *fakeFataler) Helper()                           {}

// TestAwaitReturnsWhenSignalFires asserts a fired signal unblocks Await before
// the default timeout, so a ready stream never trips the backstop.
func TestAwaitReturnsWhenSignalFires(t *testing.T) {
	sig := make(chan struct{})
	go func() {
		close(sig)
	}()
	Await(t, "test signal", sig)
}

// TestAwaitReportsChannelNameOnTimeout asserts a signal that never fires makes
// Await fail with the channel name in the message, so the stranded wait is
// identifiable rather than a silent hang.
func TestAwaitReportsChannelNameOnTimeout(t *testing.T) {
	f := &fakeFataler{}
	await(f, "provider ready", make(chan struct{}), time.Millisecond)

	if f.fatalMsg == "" {
		t.Fatal("Await must fail the test when the signal never fires")
	}
	if !strings.Contains(f.fatalMsg, "provider ready") {
		t.Errorf("timeout message %q must name the channel %q", f.fatalMsg, "provider ready")
	}
}

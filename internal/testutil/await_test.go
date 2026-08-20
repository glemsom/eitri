package testutil

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeFataler struct {
	fatalMsg string
}

func (f *fakeFataler) Fatalf(format string, args ...any) { f.fatalMsg = fmt.Sprintf(format, args...) }
func (f *fakeFataler) Helper()                           {}

func TestAwaitReturnsWhenSignalFires(t *testing.T) {
	t.Parallel()
	sig := make(chan struct{})
	go func() {
		close(sig)
	}()
	Await(t, "test signal", sig)
}

func TestAwaitReportsChannelNameOnTimeout(t *testing.T) {
	t.Parallel()
	f := &fakeFataler{}
	await(f, "provider ready", make(chan struct{}), time.Millisecond)

	if f.fatalMsg == "" {
		t.Fatal("Await must fail the test when the signal never fires")
	}
	if !strings.Contains(f.fatalMsg, "provider ready") {
		t.Errorf("timeout message %q must name the channel %q", f.fatalMsg, "provider ready")
	}
}

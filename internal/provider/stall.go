package provider

import (
	"errors"
	"io"
	"time"
)

// streamIdleTimeout is the longest a provider stream may go without delivering
// a single byte before it is declared stalled. Slow-but-live providers still
// stream reasoning/delta lines well inside this window; a connection that is
// open but silent this long is wedged, and the run must fail loudly instead of
// hanging forever (as batch mode would, with no turn context to cancel it).
var streamIdleTimeout = 5 * time.Minute

// ErrStreamStalled is returned by a provider stream that delivered no bytes for
// streamIdleTimeout; the engine surfaces it as a normal failed turn.
var ErrStreamStalled = errors.New("provider stream stalled: no data received; the connection is open but silent")

// idleWatchdog wraps a streaming response body so a read that produces no
// bytes within the idle window is aborted: the body is closed, the blocked
// read unwedges, and the caller fails the turn with ErrStreamStalled.
type idleWatchdog struct {
	body    io.ReadCloser
	timeout time.Duration
}

// watchForIdle wraps body with the idle-stall watchdog.
func watchForIdle(body io.ReadCloser) io.ReadCloser {
	return &idleWatchdog{body: body, timeout: streamIdleTimeout}
}

type readResult struct {
	n   int
	err error
}

// Read returns the underlying body's bytes, or ErrStreamStalled once the
// connection has been silent for the full idle window. The watchdog closes the
// body so the pending network read unwedges before surfacing the error.
func (w *idleWatchdog) Read(p []byte) (int, error) {
	done := make(chan readResult, 1)
	go func() {
		n, err := w.body.Read(p)
		done <- readResult{n, err}
	}()
	timer := time.NewTimer(w.timeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.n, r.err
	case <-timer.C:
		_ = w.body.Close()
		<-done // reap the reader goroutine after the close unwedges it
		return 0, ErrStreamStalled
	}
}

func (w *idleWatchdog) Close() error { return w.body.Close() }

package provider

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// blockedBody delivers a little fixture data up front and then blocks on every
// subsequent Read until Close unwedges it, mirroring an HTTP response body whose
// connection is open but silent.
type blockedBody struct {
	closed chan struct{}
	once   sync.Once
	data   []byte
}

func newBlockedBody(data string) *blockedBody {
	return &blockedBody{closed: make(chan struct{}), data: []byte(data)}
}

func (b *blockedBody) Read(p []byte) (int, error) {
	if len(b.data) > 0 {
		n := copy(p, b.data)
		b.data = b.data[n:]
		return n, nil
	}
	<-b.closed
	return 0, errors.New("connection closed")
}

func (b *blockedBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func (b *blockedBody) closedFlag() bool {
	select {
	case <-b.closed:
		return true
	default:
		return false
	}
}

func TestIdleWatchdogPassesLiveDataThrough(t *testing.T) {
	defer func(prev time.Duration) { streamIdleTimeout = prev }(streamIdleTimeout)
	streamIdleTimeout = 200 * time.Millisecond
	body := newBlockedBody("hello")
	r := watchForIdle(body)
	buf := make([]byte, 8)
	n, err := r.Read(buf)
	if err != nil || n != 5 || string(buf[:5]) != "hello" {
		t.Fatalf("Read() = (%d, %v), want (5, nil) %q", n, err, buf[:5])
	}
	_ = r.Close()
}

func TestIdleWatchdogStallsOnSilentConnection(t *testing.T) {
	defer func(prev time.Duration) { streamIdleTimeout = prev }(streamIdleTimeout)
	streamIdleTimeout = 15 * time.Millisecond
	// No fixture data: every Read blocks on the open-but-silent connection.
	body := newBlockedBody("")
	r := watchForIdle(body)
	buf := make([]byte, 8)
	start := time.Now()
	_, err := r.Read(buf)
	if !errors.Is(err, ErrStreamStalled) {
		t.Fatalf("Read() error = %v, want ErrStreamStalled", err)
	}
	if elapsed := time.Since(start); elapsed < streamIdleTimeout {
		t.Fatalf("Read returned after %v, before the %v idle timeout", elapsed, streamIdleTimeout)
	}
	if !body.closedFlag() {
		t.Fatal("watchdog did not close the body after declaring a stall")
	}
}

// pacingBody delivers one byte per Read after a fixed pause, so each read
// completes comfortably inside the idle window but a cumulative timer would
// have expired long before the loop finishes.
type pacingBody struct {
	delay  time.Duration
	pauses int
}

func (b *pacingBody) Read(p []byte) (int, error) {
	if b.pauses <= 0 {
		return 0, io.EOF
	}
	time.Sleep(b.delay)
	b.pauses--
	p[0] = 'x'
	return 1, nil
}

func (pacingBody) Close() error { return nil }

func TestIdleWatchdogResetsClockOnEachChunk(t *testing.T) {
	defer func(prev time.Duration) { streamIdleTimeout = prev }(streamIdleTimeout)
	streamIdleTimeout = 100 * time.Millisecond
	body := &pacingBody{delay: 40 * time.Millisecond, pauses: 4}
	r := watchForIdle(body)
	var buf [1]byte
	for i := 0; i < 4; i++ {
		n, err := r.Read(buf[:])
		if err != nil || n != 1 {
			t.Fatalf("Read #%d = (%d, %v), want (1, nil): a slow-but-live stream must not stall", i, n, err)
		}
	}
	// The four 40ms reads span ~160ms total — more than one full idle window — so
	// this only passes if each chunk resets the stall clock per-read.
	if n, err := r.Read(buf[:]); n != 0 || err != io.EOF {
		t.Fatalf("final Read() = (%d, %v), want (0, io.EOF)", n, err)
	}
}

package provider

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// errStream is a Stream that yields the scripted chunks and then the scripted
// error, so a test can place a transport failure mid-stream.
type errStream struct {
	chunks []Chunk
	err    error
	idx    int
}

func (s *errStream) Next() (Chunk, error) {
	if s.idx < len(s.chunks) {
		c := s.chunks[s.idx]
		s.idx++
		return c, nil
	}
	return Chunk{}, s.err
}

func TestRetryingStreamRetriesPeerResetBeforeContent(t *testing.T) {
	withFastRetry(t)
	reset := peerStreamError{StreamID: 11, Code: 0x8, Cause: errors.New("received from peer")}
	var opens int
	open := func() (Stream, error) {
		opens++
		if opens == 1 {
			// Role-only chunk, then the peer resets the stream: nothing the
			// engine would append, so the retry is safe.
			return &errStream{chunks: []Chunk{{}}, err: reset}, nil
		}
		return StreamFunc(Chunk{Content: "Hello"}, Chunk{Done: true, FinishReason: "stop"}), nil
	}
	s, err := beginStream(context.Background(), open)
	if err != nil {
		t.Fatalf("beginStream error = %v", err)
	}
	answer, _, err := consume(s)
	if err != nil {
		t.Fatalf("consume error = %v, want nil", err)
	}
	if answer != "Hello" {
		t.Fatalf("answer = %q, want %q", answer, "Hello")
	}
	if opens != 2 {
		t.Fatalf("open calls = %d, want 2", opens)
	}
}

func TestRetryingStreamDoesNotRetryAfterContent(t *testing.T) {
	withFastRetry(t)
	reset := peerStreamError{StreamID: 11, Code: 0x8, Cause: errors.New("received from peer")}
	var opens int
	open := func() (Stream, error) {
		opens++
		return &errStream{chunks: []Chunk{{Content: "partial"}}, err: reset}, nil
	}
	s, err := beginStream(context.Background(), open)
	if err != nil {
		t.Fatalf("beginStream error = %v", err)
	}
	first, err := s.Next()
	if err != nil || first.Content != "partial" {
		t.Fatalf("first Next() = (%+v, %v), want partial content", first, err)
	}
	_, err = s.Next()
	var pe peerStreamError
	if !errors.As(err, &pe) {
		t.Fatalf("Next() error = %v, want the peer reset surfaced (a retry would duplicate output)", err)
	}
	if opens != 1 {
		t.Fatalf("open calls = %d, want 1 (no retry after content)", opens)
	}
}

func TestRetryingStreamExhaustsRetries(t *testing.T) {
	withFastRetry(t)
	reset := peerStreamError{StreamID: 1, Code: 0x8}
	var opens int
	open := func() (Stream, error) {
		opens++
		return &errStream{err: reset}, nil
	}
	s, err := beginStream(context.Background(), open)
	if err != nil {
		t.Fatalf("beginStream error = %v", err)
	}
	if _, err := s.Next(); err == nil {
		t.Fatal("Next() error = nil, want the reset surfaced after the retry budget")
	}
	if want := providerMaxRetries + 1; opens != want {
		t.Fatalf("open calls = %d, want %d attempt(s)", opens, want)
	}
}

func TestRetryableStreamError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"peer reset", peerStreamError{StreamID: 3, Code: 0x8}, true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"net error", &net.OpError{Op: "read", Err: errors.New("reset by peer")}, true},
		{"clean eof", io.EOF, false},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"stalled", ErrStreamStalled, false},
		{"generic", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryableStreamError(tc.err); got != tc.want {
				t.Fatalf("retryableStreamError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestOpenAIRetriesPeerResetMidStream drives a real HTTP/2 exchange: the first
// request aborts the stream with a RST_STREAM after flushing only a role chunk,
// the retry serves the full fixture, and the turn completes on the resumed
// stream.
func TestOpenAIRetriesPeerResetMidStream(t *testing.T) {
	withFastRetry(t)
	fixture, err := os.ReadFile("testdata/hello.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var hits int
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/event-stream")
		if hits == 1 {
			// Flush a contentless role chunk so the client is mid-stream, then
			// reset: net/http turns ErrAbortHandler into RST_STREAM(INTERNAL_ERROR).
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n")
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}
		_, _ = w.Write(fixture)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	cl := NewOpenAICompatible("test-key", srv.URL+"/v1/chat/completions")
	cl.http = &http.Client{Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		ForceAttemptHTTP2: true,
	}}
	s, err := cl.Stream(context.Background(), Request{
		Model:    "deepseek-v4-flash",
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v, want nil", err)
	}
	answer, _, err := consume(s)
	if err != nil {
		t.Fatalf("consume error = %v, want nil after the retry", err)
	}
	if answer != "Hello world" {
		t.Fatalf("answer = %q, want %q", answer, "Hello world")
	}
	if hits != 2 {
		t.Fatalf("server hits = %d, want 2 (reset then retry)", hits)
	}
}

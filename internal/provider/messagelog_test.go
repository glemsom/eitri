package provider

import (
	"context"
	"errors"
	"io"
	"testing"
)

// memSink collects message-log records in memory.
type memSink struct {
	reqs  []RequestLog
	resps []ResponseLog
}

func (m *memSink) LogRequest(r RequestLog)   { m.reqs = append(m.reqs, r) }
func (m *memSink) LogResponse(r ResponseLog) { m.resps = append(m.resps, r) }

// logFakeStream replays chunks then EOF.
type logFakeStream struct {
	chunks []Chunk
	i      int
	err    error // returned after chunks exhausted (when non-nil)
}

func (f *logFakeStream) Next() (Chunk, error) {
	if f.i < len(f.chunks) {
		c := f.chunks[f.i]
		f.i++
		return c, nil
	}
	if f.err != nil {
		return Chunk{}, f.err
	}
	return Chunk{}, io.EOF
}

// fakeProvider returns the given stream or error.
type fakeProvider struct {
	stream Stream
	err    error
}

func (f *fakeProvider) Stream(ctx context.Context, req Request) (Stream, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.stream, nil
}

func TestLoggingProviderRecordsRequestAndResponse(t *testing.T) {
	sink := &memSink{}
	p := NewLoggingProvider(&fakeProvider{stream: &logFakeStream{chunks: []Chunk{
		{Content: "hel"},
		{Content: "lo", Done: true, FinishReason: "stop", Usage: &Usage{PromptTokens: 10, CompletionTokens: 2}},
	}}}, sink)

	s, err := p.Stream(context.Background(), Request{
		Model:    "m1",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
		Tools:    []Tool{{Type: "function", Function: ToolFunction{Name: "read"}}},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	for {
		_, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
	}

	if len(sink.reqs) != 1 {
		t.Fatalf("got %d request records, want 1", len(sink.reqs))
	}
	r := sink.reqs[0]
	if r.Dir != "req" || r.Model != "m1" || len(r.Messages) != 1 || len(r.Tools) != 1 || r.Tools[0] != "read" {
		t.Errorf("unexpected request record: %+v", r)
	}
	if len(sink.resps) != 1 {
		t.Fatalf("got %d response records, want 1", len(sink.resps))
	}
	resp := sink.resps[0]
	if resp.Content != "hello" || resp.FinishReason != "stop" || resp.Usage == nil || resp.Usage.PromptTokens != 10 {
		t.Errorf("unexpected response record: %+v", resp)
	}
	if resp.Error != "" {
		t.Errorf("clean turn recorded an error: %q", resp.Error)
	}
}

func TestLoggingProviderRecordsErrorResponsesOnce(t *testing.T) {
	sink := &memSink{}
	wantErr := errors.New("boom")
	p := NewLoggingProvider(&fakeProvider{err: wantErr}, sink)
	if _, err := p.Stream(context.Background(), Request{}); !errors.Is(err, wantErr) {
		t.Fatalf("Stream() error = %v, want %v", err, wantErr)
	}
	if len(sink.resps) != 1 || sink.resps[0].Error == "" {
		t.Fatalf("want exactly one error response record, got %+v", sink.resps)
	}

	// Mid-stream failure records one response with the error.
	sink2 := &memSink{}
	p2 := NewLoggingProvider(&fakeProvider{stream: &logFakeStream{chunks: []Chunk{{Content: "a"}}, err: wantErr}}, sink2)
	s, _ := p2.Stream(context.Background(), Request{})
	for {
		_, err := s.Next()
		if err != nil && !errors.Is(err, wantErr) {
			t.Fatalf("Next() unexpected error %v", err)
		}
		if err != nil {
			break
		}
	}
	if len(sink2.resps) != 1 || sink2.resps[0].Error == "" || sink2.resps[0].Content != "a" {
		t.Fatalf("mid-stream failure not recorded once with content: %+v", sink2.resps)
	}

	// Extra Next() calls after completion must not duplicate records.
	_, _ = s.Next()
	if got := len(sink2.resps); got != 1 {
		t.Errorf("duplicate response records after stream end: %d", got)
	}
}

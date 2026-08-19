package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewCreatesGUIDTranscriptDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := New(dir, false)
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	defer s.Close()

	if s.GUID() == "" {
		t.Fatal("New() GUID is empty")
	}
	// Dir is under the data dir's sessions/<GUID> tree.
	rel, err := filepath.Rel(dir, s.Dir())
	if err != nil {
		t.Fatalf("session dir %s not under data dir %s: %v", s.Dir(), dir, err)
	}
	want := filepath.Join("sessions", s.GUID())
	if rel != want {
		t.Fatalf("session dir rel = %q, want %q", rel, want)
	}
}

func TestWriteTranscriptPersistsContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := New(dir, true)
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	defer s.Close()

	if err := s.WriteTranscript([]byte("hello eitri transcript\n")); err != nil {
		t.Fatalf("WriteTranscript() error = %v, want nil", err)
	}
	trPath := filepath.Join(s.Dir(), "transcript.md")
	data, err := os.ReadFile(trPath)
	if err != nil {
		t.Fatalf("read transcript %s: %v", trPath, err)
	}
	if string(data) != "hello eitri transcript\n" {
		t.Fatalf("transcript content = %q, want %q", data, "hello eitri transcript\n")
	}
}

func TestDebugTraceSinkWritesHTTPTraces(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	t.Run("enabled writes trace files", func(t *testing.T) {
		s, err := New(dir, true)
		if err != nil {
			t.Fatalf("New() error = %v, want nil", err)
		}
		defer s.Close()
		sink := s.TraceSink()
		if sink == nil {
			t.Fatal("TraceSink() = nil, want enabled sink in debug mode")
		}
		sink.TraceRequest([]byte("GET /v1/chat/completions"))
		sink.TraceResponse([]byte("200 OK body"))

		req := filepath.Join(s.Dir(), "trace-request.http")
		if b, err := os.ReadFile(req); err != nil {
			t.Fatalf("read request trace: %v", err)
		} else if !strings.Contains(string(b), "GET /v1/chat/completions") {
			t.Fatalf("request trace = %q, want request body", b)
		}
		resp := filepath.Join(s.Dir(), "trace-response.http")
		if b, err := os.ReadFile(resp); err != nil {
			t.Fatalf("read response trace: %v", err)
		} else if !strings.Contains(string(b), "200 OK body") {
			t.Fatalf("response trace = %q, want response body", b)
		}
	})

	t.Run("disabled yields nil sink", func(t *testing.T) {
		s, err := New(dir, false)
		if err != nil {
			t.Fatalf("New() error = %v, want nil", err)
		}
		defer s.Close()
		if sink := s.TraceSink(); sink != nil {
			t.Fatalf("TraceSink() = %v, want nil when debug disabled", sink)
		}
	})
}

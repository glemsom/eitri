package session_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/glemsom/eitri/internal/session"
)

func TestTraceSinkSerializesConcurrentRecords(t *testing.T) {
	s, err := session.New(t.TempDir(), true)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	sink := s.TraceSink()
	if sink == nil {
		t.Fatal("TraceSink() = nil, want debug trace sink")
	}

	const workers = 32
	const recordsPerWorker = 8
	const padding = 128 << 10

	expectedRequests := make(map[string]bool, workers*recordsPerWorker)
	expectedResponses := make(map[string]bool, workers*recordsPerWorker)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for record := range recordsPerWorker {
				request := fmt.Sprintf("request-%02d-%02d-%s", worker, record, strings.Repeat("r", padding))
				response := fmt.Sprintf("response-%02d-%02d-%s", worker, record, strings.Repeat("s", padding))
				sink.TraceRequest([]byte(request))
				sink.TraceResponse([]byte(response))
			}
		}()
		for record := range recordsPerWorker {
			expectedRequests[fmt.Sprintf("request-%02d-%02d-%s", worker, record, strings.Repeat("r", padding))] = true
			expectedResponses[fmt.Sprintf("response-%02d-%02d-%s", worker, record, strings.Repeat("s", padding))] = true
		}
	}
	close(start)
	wg.Wait()

	assertTraceRecords(t, filepath.Join(s.Dir(), "trace-request.http"), expectedRequests)
	assertTraceRecords(t, filepath.Join(s.Dir(), "trace-response.http"), expectedResponses)
}

func assertTraceRecords(t *testing.T, path string, expected map[string]bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	records := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(records) != len(expected) {
		t.Fatalf("trace has %d records, want %d", len(records), len(expected))
	}
	for _, record := range records {
		if !expected[record] {
			t.Fatalf("trace contains interleaved or unknown record")
		}
		delete(expected, record)
	}
	if len(expected) != 0 {
		t.Fatalf("trace is missing %d records", len(expected))
	}
}

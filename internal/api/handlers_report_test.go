package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/persist"
	"github.com/glemsom/eitri/internal/report"
	"github.com/glemsom/eitri/internal/timeline"
)

// testHelper creates a test server with a temp persister directory.
// Returns the server, the root dir, and a cleanup function.
func testHelper(t *testing.T) (*httptest.Server, string, *persist.Persister) {
	t.Helper()
	dir, err := os.MkdirTemp("", "api-report-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	p, err := persist.New(dir)
	if err != nil {
		t.Fatalf("failed to create persister: %v", err)
	}

	cfg := ServerConfig{
		Persister:     p,
		ReportService: report.New(p),
		Workspace:     dir,
		StartTime:     time.Now(),
	}
	srv := NewServer(cfg)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, dir, p
}

func TestHandleListReports_Empty(t *testing.T) {
	ts, _, _ := testHelper(t)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/api/sessions/nonexistent/reports")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	runs, ok := body["runs"].([]any)
	if !ok {
		t.Fatal("expected 'runs' array")
	}
	if len(runs) != 0 {
		t.Errorf("expected empty runs, got %d", len(runs))
	}
}

func TestHandleListReports_WithTimeline(t *testing.T) {
	ts, dir, _ := testHelper(t)
	defer ts.Close()

	sessionID := "sess-test-1"

	p, err := persist.New(dir)
	if err != nil {
		t.Fatalf("failed to create persister: %v", err)
	}

	now := time.Now().UTC()
	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-abc",
		SessionID: sessionID,
		Provider: timeline.TimelineProvider{
			Model:      "test-model",
			ProviderID: "test-provider",
		},
		StartedAt: now,
		EndedAt:   now.Add(5 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason:  timeline.TerminationCompleted,
			Message: "",
		},
		Events: []timeline.TimelineEvent{
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "grep"},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "grep", Output: "results", Error: false},
		},
	}
	if err := p.SaveTimeline(sessionID, tl); err != nil {
		t.Fatalf("failed to save timeline: %v", err)
	}

	resp, err := ts.Client().Get(ts.URL + "/api/sessions/" + sessionID + "/reports")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	runs, ok := body["runs"].([]any)
	if !ok {
		t.Fatal("expected 'runs' array")
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	run0 := runs[0].(map[string]any)
	if run0["termination"].(map[string]any)["reason"] != "completed" {
		t.Errorf("expected completed, got %v", run0["termination"])
	}
}

func TestHandleGetReport_Full(t *testing.T) {
	ts, dir, _ := testHelper(t)
	defer ts.Close()

	sessionID := "sess-report-1"
	p, err := persist.New(dir)
	if err != nil {
		t.Fatalf("failed to create persister: %v", err)
	}

	now := time.Now().UTC()
	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-xyz",
		SessionID: sessionID,
		Provider: timeline.TimelineProvider{
			Model:      "gold-model",
			ProviderID: "openai",
		},
		StartedAt: now,
		EndedAt:   now.Add(10 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason:  timeline.TerminationCompleted,
			Message: "",
		},
		Events: []timeline.TimelineEvent{
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "bash", Args: json.RawMessage(`{"cmd":"ls"}`)},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "bash", Output: "file.txt", Error: false},
		},
	}
	if err := p.SaveTimeline(sessionID, tl); err != nil {
		t.Fatalf("failed to save timeline: %v", err)
	}

	resp, err := ts.Client().Get(ts.URL + "/api/sessions/" + sessionID + "/report?run=0")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	if body["model"] != "gold-model" {
		t.Errorf("expected model 'gold-model', got %v", body["model"])
	}
	if body["report_version"] != "full" {
		t.Errorf("expected report_version 'full', got %v", body["report_version"])
	}
	if body["duration_ms"] != float64(10000) {
		t.Errorf("expected duration 10000, got %v", body["duration_ms"])
	}
}

func TestHandleGetReport_BadRunIndex(t *testing.T) {
	ts, _, _ := testHelper(t)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/api/sessions/test/report?run=-1")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleListReports_NoPersister(t *testing.T) {
	dir, err := os.MkdirTemp("", "api-report-no-persist-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	cfg := ServerConfig{
		Persister: nil, // No persister
		Workspace: dir,
		StartTime: time.Now(),
	}
	srv := NewServer(cfg)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := ts.Client().Get(ts.URL + "/api/sessions/test/reports")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	runs, ok := body["runs"].([]any)
	if !ok {
		t.Fatal("expected 'runs' array")
	}
	if len(runs) != 0 {
		t.Errorf("expected empty runs, got %d", len(runs))
	}
}

// TestHandleListReports_InjectedServiceOnly proves the handler consumes the
// injected report.Service (issue #1206): ServerConfig.Persister is absent, so
// a run list can only come from the service wired into ServerConfig.
func TestHandleListReports_InjectedServiceOnly(t *testing.T) {
	dir, err := os.MkdirTemp("", "api-report-injected-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	p, err := persist.New(dir)
	if err != nil {
		t.Fatalf("failed to create persister: %v", err)
	}

	cfg := ServerConfig{
		ReportService: report.New(p), // no Persister — service is the only source
		Workspace:     dir,
		StartTime:     time.Now(),
	}
	srv := NewServer(cfg)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	sessionID := "sess-injected-1"
	now := time.Now().UTC()
	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-injected",
		SessionID: sessionID,
		Provider: timeline.TimelineProvider{
			Model:      "test-model",
			ProviderID: "test-provider",
		},
		StartedAt: now,
		EndedAt:   now.Add(5 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason:  timeline.TerminationCompleted,
			Message: "",
		},
		Events: []timeline.TimelineEvent{
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "grep"},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "grep", Output: "results", Error: false},
		},
	}
	if err := p.SaveTimeline(sessionID, tl); err != nil {
		t.Fatalf("failed to save timeline: %v", err)
	}

	resp, err := ts.Client().Get(ts.URL + "/api/sessions/" + sessionID + "/reports")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}

	runs, ok := body["runs"].([]any)
	if !ok {
		t.Fatal("expected 'runs' array")
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run from injected service, got %d", len(runs))
	}
}

// TestHandleGetReport_InjectedServiceOnly proves the report body comes from the
// injected report.Service without ServerConfig.Persister (issue #1206).
func TestHandleGetReport_InjectedServiceOnly(t *testing.T) {
	dir, err := os.MkdirTemp("", "api-report-injected-get-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	p, err := persist.New(dir)
	if err != nil {
		t.Fatalf("failed to create persister: %v", err)
	}

	cfg := ServerConfig{
		ReportService: report.New(p), // no Persister — service is the only source
		Workspace:     dir,
		StartTime:     time.Now(),
	}
	srv := NewServer(cfg)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	sessionID := "sess-injected-get-1"
	now := time.Now().UTC()
	tl := &timeline.Timeline{
		Version:   1,
		RunID:     "run-injected-get",
		SessionID: sessionID,
		Provider: timeline.TimelineProvider{
			Model:      "injected-model",
			ProviderID: "openai",
		},
		StartedAt: now,
		EndedAt:   now.Add(10 * time.Second),
		Termination: &timeline.TimelineTermination{
			Reason:  timeline.TerminationCompleted,
			Message: "",
		},
		Events: []timeline.TimelineEvent{
			{Type: "tool_call", Timestamp: now, Turn: 1, Tool: "bash", Args: json.RawMessage(`{"cmd":"ls"}`)},
			{Type: "tool_result", Timestamp: now, Turn: 1, Tool: "bash", Output: "file.txt", Error: false},
		},
	}
	if err := p.SaveTimeline(sessionID, tl); err != nil {
		t.Fatalf("failed to save timeline: %v", err)
	}

	resp, err := ts.Client().Get(ts.URL + "/api/sessions/" + sessionID + "/report?run=0")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode body: %v", err)
	}
	if body["model"] != "injected-model" {
		t.Errorf("expected model 'injected-model', got %v", body["model"])
	}
}

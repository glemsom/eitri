package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeStatsFixture creates two sessions: one with a successful and a failed
// tool call across two turns, one tool-free single turn.
func writeStatsFixture(t *testing.T, dataDir string) {
	t.Helper()
	write := func(guid string, lines []string) {
		dir := filepath.Join(dataDir, "sessions", guid)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for _, l := range lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(dir, "messages.jsonl"), []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", []string{
		`{"ts":"2026-01-01T00:00:00Z","dir":"req","model":"m1","messages":[{"role":"user","content":"list the files"}],"tools":["read_file","bash"]}`,
		`{"ts":"2026-01-01T00:00:01Z","dir":"resp","tool_calls":[{"id":"t1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.go\"}"}},{"id":"t2","type":"function","function":{"name":"bash","arguments":"{\"command\":\"false\"}"}}],"finish_reason":"tool_calls","usage":{"prompt_tokens":100,"completion_tokens":10,"prompt_cache_hit_tokens":80,"prompt_cache_miss_tokens":20}}`,
		`{"ts":"2026-01-01T00:00:02Z","dir":"req","model":"m1","messages":[{"role":"user","content":"list the files"},{"role":"assistant","content":""},{"role":"tool","tool_call_id":"t1","content":"package main"},{"role":"tool","tool_call_id":"t2","content":"error executing tool: exit status 1"}]}`,
		`{"ts":"2026-01-01T00:00:03Z","dir":"resp","content":"there is one file","finish_reason":"stop","usage":{"prompt_tokens":150,"completion_tokens":5}}`,
	})
	write("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2", []string{
		`{"ts":"2026-01-02T00:00:00Z","dir":"req","model":"m2","messages":[{"role":"user","content":"hi"}]}`,
		`{"ts":"2026-01-02T00:00:01Z","dir":"resp","content":"hello","finish_reason":"stop","usage":{"prompt_tokens":10,"completion_tokens":2}}`,
	})
}

func TestStatsSessionDetail(t *testing.T) {
	dataDir := t.TempDir()
	writeStatsFixture(t, dataDir)
	var out bytes.Buffer
	if err := StatsSession(dataDir, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", "text", &out); err != nil {
		t.Fatalf("StatsSession() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{"turns 2", "in=250", "out=15", "cache_hit=80", "hit=80%", "tool calls 2 (1 errors)", "bash=1 (1 errors)", "stop=1", "tool_calls=1"} {
		if !strings.Contains(got, want) {
			t.Errorf("stats detail missing %q in:\n%s", want, got)
		}
	}
}

func TestStatsSessionAll(t *testing.T) {
	dataDir := t.TempDir()
	writeStatsFixture(t, dataDir)
	var out bytes.Buffer
	if err := StatsSession(dataDir, "all", "text", &out); err != nil {
		t.Fatalf("StatsSession() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 session rows, got %d:\n%s", len(lines), out.String())
	}
	// Newest (2026-01-02) session must sort first.
	if !strings.HasPrefix(lines[0], "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2") {
		t.Errorf("expected newest session first, got:\n%s", out.String())
	}
}

func TestStatsSessionJSON(t *testing.T) {
	dataDir := t.TempDir()
	writeStatsFixture(t, dataDir)
	var out bytes.Buffer
	if err := StatsSession(dataDir, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", "json", &out); err != nil {
		t.Fatalf("StatsSession() error = %v", err)
	}
	var row sessionStatsJSON
	if err := json.Unmarshal(out.Bytes(), &row); err != nil {
		t.Fatalf("stats json not decodable: %v\n%s", err, out.String())
	}
	if row.Turns != 2 || row.PromptTokens != 250 || row.ToolCalls != 2 || row.ToolErrors != 1 {
		t.Errorf("unexpected stats json: %+v", row)
	}
	if len(row.Tools) != 2 {
		t.Errorf("want 2 tool roll-ups, got %+v", row.Tools)
	}
}

func TestToolsSessionText(t *testing.T) {
	dataDir := t.TempDir()
	writeStatsFixture(t, dataDir)
	var out bytes.Buffer
	if err := ToolsSession(dataDir, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", "", 0, "text", &out); err != nil {
		t.Fatalf("ToolsSession() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "[1] read_file ok 12B") {
		t.Errorf("missing ok read_file call in:\n%s", got)
	}
	if !strings.Contains(got, "[1] bash error") {
		t.Errorf("missing error bash call in:\n%s", got)
	}
	if !strings.Contains(got, "2 calls (1 errors)") {
		t.Errorf("missing summary in:\n%s", got)
	}
}

func TestToolsSessionFilters(t *testing.T) {
	dataDir := t.TempDir()
	writeStatsFixture(t, dataDir)
	var out bytes.Buffer
	if err := ToolsSession(dataDir, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", "bash", 0, "text", &out); err != nil {
		t.Fatalf("ToolsSession() error = %v", err)
	}
	if strings.Contains(out.String(), "read_file") {
		t.Errorf("--name bash should exclude read_file:\n%s", out.String())
	}
	out.Reset()
	if err := ToolsSession(dataDir, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", "", 2, "text", &out); err != nil {
		t.Fatalf("ToolsSession() error = %v", err)
	}
	if !strings.Contains(out.String(), "no tool calls matched") {
		t.Errorf("--turn 2 has no calls, got:\n%s", out.String())
	}
}

func TestToolsSessionJSON(t *testing.T) {
	dataDir := t.TempDir()
	writeStatsFixture(t, dataDir)
	var out bytes.Buffer
	if err := ToolsSession(dataDir, "all", "", 0, "json", &out); err != nil {
		t.Fatalf("ToolsSession() error = %v", err)
	}
	var payload struct {
		Calls []toolCallJSON `json:"calls"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("tools json not decodable: %v\n%s", err, out.String())
	}
	if len(payload.Calls) != 2 {
		t.Fatalf("want 2 calls, got %+v", payload.Calls)
	}
	if payload.Calls[0].GUID == "" {
		t.Errorf("all-sessions json should carry guid: %+v", payload.Calls[0])
	}
	if payload.Calls[0].ResultBytes != len("package main") {
		t.Errorf("want result_bytes=%d, got %d", len("package main"), payload.Calls[0].ResultBytes)
	}
}

func TestRunSessionCmdStatsAndTools(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("EITRI_DIR", dataDir)
	writeStatsFixture(t, dataDir)
	for _, args := range [][]string{
		{"stats"},
		{"stats", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"},
		{"stats", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", "--format", "json"},
		{"tools", "all", "--name", "bash", "--format", "json"},
		{"tools", "--turn", "1"},
	} {
		var out bytes.Buffer
		if err := RunSessionCmd(args, &out); err != nil {
			t.Errorf("RunSessionCmd(%v) error = %v", args, err)
		}
	}
}

func TestRunSessionCmdStatsToolsRejectBadArgs(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("EITRI_DIR", dataDir)
	for _, args := range [][]string{
		{"stats", "not-a-guid"},
		{"stats", "--format", "xml"},
		{"tools", "--name"},
		{"tools", "--turn", "0"},
		{"tools", "--bogus"},
	} {
		var out bytes.Buffer
		if err := RunSessionCmd(args, &out); err == nil {
			t.Errorf("RunSessionCmd(%v) expected error, got nil (%q)", args, out.String())
		}
	}
}

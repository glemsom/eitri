package app

import (
	"bytes"

	"github.com/glemsom/eitri/internal/provider"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMessagesFixture creates a session dir with a two-cycle messages.jsonl.
func writeMessagesFixture(t *testing.T, dataDir, guid string) {
	t.Helper()
	dir := filepath.Join(dataDir, "sessions", guid)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"ts":"2026-01-01T00:00:00Z","dir":"req","model":"m1","messages":[{"role":"user","content":"list the files"}],"tools":["read_file"]}`,
		`{"ts":"2026-01-01T00:00:01Z","dir":"resp","content":"","tool_calls":[{"id":"t1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.go\"}"}}],"finish_reason":"tool_calls","usage":{"prompt_tokens":100,"completion_tokens":10}}`,
		`{"ts":"2026-01-01T00:00:02Z","dir":"req","model":"m1","messages":[{"role":"user","content":"list the files"},{"role":"assistant","content":""},{"role":"tool","content":"package main"}]}`,
		`{"ts":"2026-01-01T00:00:03Z","dir":"resp","content":"there is one file","finish_reason":"stop","usage":{"prompt_tokens":150,"completion_tokens":5}}`,
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

func TestListSessions(t *testing.T) {
	dataDir := t.TempDir()
	writeMessagesFixture(t, dataDir, "aaaa1111")
	var out bytes.Buffer
	if err := ListSessions(dataDir, &out); err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "aaaa1111") || !strings.Contains(got, "2 cycles") || !strings.Contains(got, "m1") {
		t.Errorf("unexpected list output: %q", got)
	}
}

func TestShowSessionSummaryAndTurn(t *testing.T) {
	dataDir := t.TempDir()
	writeMessagesFixture(t, dataDir, "bbbb2222")
	var out bytes.Buffer
	if err := ShowSession(dataDir, "bbbb2222", 0, &out); err != nil {
		t.Fatalf("ShowSession() error = %v", err)
	}
	summary := out.String()
	for _, want := range []string{"[1]", "[2]", "tools=read_file", "calls=read_file", "tokens(in=100,out=10)", "tokens(in=150,out=5)"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary missing %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, `"role"`) {
		t.Errorf("summary must not dump full message bodies:\n%s", summary)
	}

	out.Reset()
	if err := ShowSession(dataDir, "bbbb2222", 1, &out); err != nil {
		t.Fatalf("ShowSession(turn 1) error = %v", err)
	}
	turnJSON := out.String()
	if !strings.Contains(turnJSON, `"dir": "req"`) || strings.Contains(turnJSON, `there is one file`) {
		t.Errorf("--turn 1 should dump only cycle 1's full records:\n%s", turnJSON)
	}
}

func TestGrepSession(t *testing.T) {
	dataDir := t.TempDir()
	writeMessagesFixture(t, dataDir, "cccc3333")
	var out bytes.Buffer
	if err := GrepSession(dataDir, "one file", "", &out); err != nil {
		t.Fatalf("GrepSession() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "cccc3333:2") || !strings.Contains(got, "resp.content") {
		t.Errorf("grep output missing cycle hit: %q", got)
	}

	out.Reset()
	if err := GrepSession(dataDir, "zzz-no-match-zzz", "cccc3333", &out); err != nil {
		t.Fatalf("GrepSession() error = %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("no-match grep printed output: %q", out.String())
	}
}

func TestRunSessionCmdDispatch(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(DataDirEnv, dataDir)
	writeMessagesFixture(t, dataDir, "dddd4444")
	var out bytes.Buffer
	if err := RunSessionCmd([]string{"show", "dddd4444"}, &out); err != nil {
		t.Fatalf("RunSessionCmd(show) error = %v", err)
	}
	if !strings.Contains(out.String(), "dddd4444") == false && !strings.Contains(out.String(), "[1]") {
		t.Errorf("dispatch output unexpected: %q", out.String())
	}
	if err := RunSessionCmd([]string{"bogus"}, &out); err == nil {
		t.Error("unknown subcommand must error")
	}
}

// TestRunBatchWritesMessageTranscript is the end-to-end check: a real Run through the engine must leave a messages.jsonl with req+resp records in the new session dir.
func TestRunBatchWritesMessageTranscript(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, ".eitri")
	t.Setenv(DataDirEnv, dataDir)
	var out bytes.Buffer
	if err := Run(Options{
		DataDir:  dataDir,
		LookPath: okLookPath,
		Prompt:   "Say hello",
		Stdout:   &out,
		Provider: provider.NewFake("../provider/testdata/hello.sse"),
	}); err != nil {
		t.Fatalf("Run(batch) error = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, "sessions"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("no session dirs: %v", err)
	}
	path := filepath.Join(dataDir, "sessions", entries[0].Name(), "messages.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, want := range []string{`"dir":"req"`, `"dir":"resp"`, `"role":"system"`, `"role":"user"`} {
		if !bytes.Contains(b, []byte(want)) {
			t.Errorf("messages.jsonl missing %s:\n%s", want, b)
		}
	}

	// And the CLI reads it back.
	var list bytes.Buffer
	if err := RunSessionCmd([]string{"list"}, &list); err != nil {
		t.Fatalf("session list: %v", err)
	}
	if !strings.Contains(list.String(), entries[0].Name()) {
		t.Errorf("session list missing the new GUID: %q", list.String())
	}
}

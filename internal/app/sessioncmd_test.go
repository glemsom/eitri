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
	if err := ShowSession(dataDir, "bbbb2222", 0, false, &out); err != nil {
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
	if err := ShowSession(dataDir, "bbbb2222", 1, false, &out); err != nil {
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
	if err := GrepSession(dataDir, "one file", "", false, &out); err != nil {
		t.Fatalf("GrepSession() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "cccc3333:2") || !strings.Contains(got, "resp.content") {
		t.Errorf("grep output missing cycle hit: %q", got)
	}

	out.Reset()
	if err := GrepSession(dataDir, "zzz-no-match-zzz", "cccc3333", false, &out); err != nil {
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

func TestShowSessionNoReasoning(t *testing.T) {
	dataDir := t.TempDir()
	guid := "eeee5555"
	dir := filepath.Join(dataDir, "sessions", guid)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"ts":"2026-01-01T00:00:00Z","dir":"req","model":"m1","messages":[{"role":"assistant","content":"","reasoning_content":"secret thoughts"}]}`,
		`{"ts":"2026-01-01T00:00:01Z","dir":"resp","content":"answer","reasoning_content":"more secret thoughts","finish_reason":"stop"}`,
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "messages.jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := ShowSession(dataDir, guid, 1, true, &out); err != nil {
		t.Fatalf("ShowSession(--no-reasoning) error = %v", err)
	}
	if strings.Contains(out.String(), "secret thoughts") {
		t.Errorf("--no-reasoning leaked reasoning:\n%s", out.String())
	}

	out.Reset()
	if err := ShowSession(dataDir, guid, 1, false, &out); err != nil {
		t.Fatalf("ShowSession() error = %v", err)
	}
	if !strings.Contains(out.String(), "secret thoughts") {
		t.Errorf("default show must keep reasoning:\n%s", out.String())
	}
}

func TestTalkSession(t *testing.T) {
	dataDir := t.TempDir()
	writeMessagesFixture(t, dataDir, "ffff6666")
	var out bytes.Buffer
	if err := TalkSession(dataDir, "ffff6666", TalkOptions{}, &out); err != nil {
		t.Fatalf("TalkSession() error = %v", err)
	}
	full := out.String()
	// Dedupe: turn 1's user message must appear once even though turn 2 resends it.
	if got := strings.Count(full, "list the files"); got != 1 {
		t.Errorf("user message repeated %d times (want 1, deduped history):\n%s", got, full)
	}
	for _, want := range []string{"[1] user:", "[2] tool:", "package main", "there is one file"} {
		if !strings.Contains(full, want) {
			t.Errorf("talk missing %q:\n%s", want, full)
		}
	}

	// Turn range.
	out.Reset()
	if err := TalkSession(dataDir, "ffff6666", TalkOptions{FromTurn: 2, ToTurn: 2}, &out); err != nil {
		t.Fatalf("TalkSession(turn 2) error = %v", err)
	}
	if strings.Contains(out.String(), "[1]") || !strings.Contains(out.String(), "[2] assistant:") {
		t.Errorf("--turn 2 output wrong:\n%s", out.String())
	}

	// Role filter.
	out.Reset()
	if err := TalkSession(dataDir, "ffff6666", TalkOptions{Role: "user"}, &out); err != nil {
		t.Fatalf("TalkSession(role=user) error = %v", err)
	}
	got := out.String()
	if strings.Contains(got, "assistant") || strings.Contains(got, "tool") || !strings.Contains(got, "list the files") {
		t.Errorf("--role user output wrong:\n%s", got)
	}
}

func TestTalkSessionReasoning(t *testing.T) {
	dataDir := t.TempDir()
	guid := "aaaa7777"
	dir := filepath.Join(dataDir, "sessions", guid)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"ts":"2026-01-01T00:00:00Z","dir":"req","model":"m1","messages":[{"role":"user","content":"hi"}]}`,
		`{"ts":"2026-01-01T00:00:01Z","dir":"resp","content":"answer","reasoning_content":"secret thoughts","finish_reason":"stop"}`,
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "messages.jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := TalkSession(dataDir, guid, TalkOptions{}, &out); err != nil {
		t.Fatalf("TalkSession() error = %v", err)
	}
	if strings.Contains(out.String(), "secret thoughts") {
		t.Errorf("default talk must strip reasoning:\n%s", out.String())
	}
	out.Reset()
	if err := TalkSession(dataDir, guid, TalkOptions{Reasoning: true}, &out); err != nil {
		t.Fatalf("TalkSession(--reasoning) error = %v", err)
	}
	if !strings.Contains(out.String(), "secret thoughts") {
		t.Errorf("--reasoning talk dropped reasoning:\n%s", out.String())
	}
}

func TestGrepSessionFullMode(t *testing.T) {
	dataDir := t.TempDir()
	writeMessagesFixture(t, dataDir, "bbbb8888")
	var out bytes.Buffer
	if err := GrepSession(dataDir, "one file", "bbbb8888", true, &out); err != nil {
		t.Fatalf("GrepSession(full) error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "resp.content:\n    there is one file") {
		t.Errorf("-full grep missing full field text:\n%q", got)
	}
	if strings.Contains(got, "…") {
		t.Errorf("-full grep must not truncate with ellipsis:\n%q", got)
	}
}

func TestRunSessionCmdTalkAndGrepFull(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(DataDirEnv, dataDir)
	writeMessagesFixture(t, dataDir, "cccc9999")
	var out bytes.Buffer
	if err := RunSessionCmd([]string{"talk", "cccc9999", "--turn", "1-2", "--role", "user"}, &out); err != nil {
		t.Fatalf("RunSessionCmd(talk) error = %v", err)
	}
	if !strings.Contains(out.String(), "[1] user:") || strings.Contains(out.String(), "[2] assistant") {
		t.Errorf("dispatched talk wrong:\n%s", out.String())
	}
	out.Reset()
	if err := RunSessionCmd([]string{"grep", "one file", "cccc9999", "-full"}, &out); err != nil {
		t.Fatalf("RunSessionCmd(grep -full) error = %v", err)
	}
	if !strings.Contains(out.String(), "there is one file") {
		t.Errorf("dispatched grep -full wrong:\n%s", out.String())
	}
	if _, _, err := parseTurnRange("3"); err != nil {
		t.Errorf("parseTurnRange(3): %v", err)
	}
	if lo, hi, err := parseTurnRange("2-5"); err != nil || lo != 2 || hi != 5 {
		t.Errorf("parseTurnRange(2-5) = %d,%d,%v", lo, hi, err)
	}
	if _, _, err := parseTurnRange("bogus"); err == nil {
		t.Error("parseTurnRange(bogus) must error")
	}
}

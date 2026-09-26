package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// writeMessagesFixture creates a session dir with a two-turn messages.jsonl.
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
	writeMessagesFixture(t, dataDir, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1")
	var out bytes.Buffer
	if err := ListSessions(dataDir, &out); err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1") || !strings.Contains(got, "2 turns") || !strings.Contains(got, "m1") {
		t.Errorf("unexpected list output: %q", got)
	}
}

func TestSessionCommandsRejectMalformedAndTraversalGUIDs(t *testing.T) {
	dataDir := t.TempDir()
	for _, guid := range []string{"../escape", "not-a-guid", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaA"} {
		t.Run(guid, func(t *testing.T) {
			commands := []struct {
				name string
				run  func(*bytes.Buffer) error
			}{
				{"show", func(out *bytes.Buffer) error { return ShowSession(dataDir, guid, 0, false, out) }},
				{"talk", func(out *bytes.Buffer) error { return TalkSession(dataDir, guid, TalkOptions{}, out) }},
				{"grep", func(out *bytes.Buffer) error { return GrepSession(dataDir, "needle", guid, false, out) }},
			}
			for _, command := range commands {
				t.Run(command.name, func(t *testing.T) {
					if err := command.run(&bytes.Buffer{}); err == nil {
						t.Fatalf("%s accepted invalid GUID %q", command.name, guid)
					}
				})
			}
		})
	}
}

func TestSessionCommandsAcceptGeneratedGUID(t *testing.T) {
	dataDir := t.TempDir()
	guid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	writeMessagesFixture(t, dataDir, guid)
	for _, command := range []struct {
		name string
		run  func(*bytes.Buffer) error
	}{
		{"show", func(out *bytes.Buffer) error { return ShowSession(dataDir, guid, 0, false, out) }},
		{"talk", func(out *bytes.Buffer) error { return TalkSession(dataDir, guid, TalkOptions{}, out) }},
		{"grep", func(out *bytes.Buffer) error { return GrepSession(dataDir, "one file", guid, false, out) }},
	} {
		t.Run(command.name, func(t *testing.T) {
			if err := command.run(&bytes.Buffer{}); err != nil {
				t.Fatalf("%s rejected valid GUID: %v", command.name, err)
			}
		})
	}
}

func TestShowSessionSummaryAndTurn(t *testing.T) {
	dataDir := t.TempDir()
	writeMessagesFixture(t, dataDir, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	var out bytes.Buffer
	if err := ShowSession(dataDir, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 0, false, &out); err != nil {
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
	if err := ShowSession(dataDir, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 1, false, &out); err != nil {
		t.Fatalf("ShowSession(turn 1) error = %v", err)
	}
	turnJSON := out.String()
	if !strings.Contains(turnJSON, `"dir": "req"`) || strings.Contains(turnJSON, `there is one file`) {
		t.Errorf("--turn 1 should dump only turn 1's full records:\n%s", turnJSON)
	}
}

func TestGrepSession(t *testing.T) {
	dataDir := t.TempDir()
	writeMessagesFixture(t, dataDir, "cccccccccccccccccccccccccccccccc")
	var out bytes.Buffer
	if err := GrepSession(dataDir, "one file", "", false, &out); err != nil {
		t.Fatalf("GrepSession() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "cccccccccccccccccccccccccccccccc:2") || !strings.Contains(got, "resp.content") {
		t.Errorf("grep output missing turn hit: %q", got)
	}

	out.Reset()
	if err := GrepSession(dataDir, "zzz-no-match-zzz", "cccccccccccccccccccccccccccccccc", false, &out); err != nil {
		t.Fatalf("GrepSession() error = %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("no-match grep printed output: %q", out.String())
	}
}

func TestRunSessionCmdDispatch(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(DataDirEnv, dataDir)
	writeMessagesFixture(t, dataDir, "dddddddddddddddddddddddddddddddd")
	var out bytes.Buffer
	if err := RunSessionCmd([]string{"show", "dddddddddddddddddddddddddddddddd"}, &out); err != nil {
		t.Fatalf("RunSessionCmd(show) error = %v", err)
	}
	if !strings.Contains(out.String(), "dddddddddddddddddddddddddddddddd") == false && !strings.Contains(out.String(), "[1]") {
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
	guid := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
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
	writeMessagesFixture(t, dataDir, "ffffffffffffffffffffffffffffffff")
	var out bytes.Buffer
	if err := TalkSession(dataDir, "ffffffffffffffffffffffffffffffff", TalkOptions{}, &out); err != nil {
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

	out.Reset()
	if err := TalkSession(dataDir, "ffffffffffffffffffffffffffffffff", TalkOptions{FromTurn: 2, ToTurn: 2}, &out); err != nil {
		t.Fatalf("TalkSession(turn 2) error = %v", err)
	}
	if strings.Contains(out.String(), "[1]") || !strings.Contains(out.String(), "[2] assistant:") {
		t.Errorf("--turn 2 output wrong:\n%s", out.String())
	}

	out.Reset()
	if err := TalkSession(dataDir, "ffffffffffffffffffffffffffffffff", TalkOptions{Role: "user"}, &out); err != nil {
		t.Fatalf("TalkSession(role=user) error = %v", err)
	}
	got := out.String()
	if strings.Contains(got, "assistant") || strings.Contains(got, "tool") || !strings.Contains(got, "list the files") {
		t.Errorf("--role user output wrong:\n%s", got)
	}
}

func TestTalkSessionReasoning(t *testing.T) {
	dataDir := t.TempDir()
	guid := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
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
	writeMessagesFixture(t, dataDir, "dddddddddddddddddddddddddddddddd")
	var out bytes.Buffer
	if err := GrepSession(dataDir, "one file", "dddddddddddddddddddddddddddddddd", true, &out); err != nil {
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
	writeMessagesFixture(t, dataDir, "cccccccccccccccccccccccccccccccc")
	var out bytes.Buffer
	if err := RunSessionCmd([]string{"talk", "cccccccccccccccccccccccccccccccc", "--turn", "1-2", "--role", "user"}, &out); err != nil {
		t.Fatalf("RunSessionCmd(talk) error = %v", err)
	}
	if !strings.Contains(out.String(), "[1] user:") || strings.Contains(out.String(), "[2] assistant") {
		t.Errorf("dispatched talk wrong:\n%s", out.String())
	}
	out.Reset()
	if err := RunSessionCmd([]string{"grep", "one file", "cccccccccccccccccccccccccccccccc", "-full"}, &out); err != nil {
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

func TestRunSessionCmdRejectsUndocumentedGrammar(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(DataDirEnv, dataDir)
	writeMessagesFixture(t, dataDir, "11111111111111111111111111111111")

	tests := [][]string{
		{"list", "extra"},
		{"show", "11111111111111111111111111111111", "extra"},
		{"show", "11111111111111111111111111111111", "--turn", "1junk"},
		{"show", "11111111111111111111111111111111", "--turn", "0"},
		{"show", "11111111111111111111111111111111", "--turn"},
		{"show", "--no-reasoning"},
		{"show", "11111111111111111111111111111111", "--no-reasoning", "--no-reasoning"},
		{"talk", "11111111111111111111111111111111", "--turn", "1-2junk"},
		{"talk", "11111111111111111111111111111111", "--turn", "1-"},
		{"talk", "11111111111111111111111111111111", "--from", "2junk"},
		{"talk", "11111111111111111111111111111111", "--from", "0"},
		{"talk", "11111111111111111111111111111111", "--role", "human"},
		{"talk", "11111111111111111111111111111111", "--role"},
		{"talk", "--all"},
		{"talk", "11111111111111111111111111111111", "--turn", "1", "--from", "2"},
		{"talk", "11111111111111111111111111111111", "--all", "--all"},
		{"grep", "files", "guid=11111111111111111111111111111111"},
		{"grep", "files", "11111111111111111111111111111111", "--full"},
		{"grep", "files", "-full", "11111111111111111111111111111111"},
		{"grep", "files", "all", "11111111111111111111111111111111"},
		{"grep", "files", "11111111111111111111111111111111", "-full", "extra"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			if err := RunSessionCmd(args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "usage:") {
				t.Fatalf("RunSessionCmd(%q) error = %v, want usage error", args, err)
			}
		})
	}
}

func writeCorruptMessagesFixture(t *testing.T, dataDir, guid, contents string) {
	t.Helper()
	dir := filepath.Join(dataDir, "sessions", guid)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "messages.jsonl"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSessionCommandsReportMalformedTranscript(t *testing.T) {
	dataDir := t.TempDir()
	writeCorruptMessagesFixture(t, dataDir, "22222222222222222222222222222222", "{not json}\n")

	tests := []struct {
		name string
		run  func(*bytes.Buffer) error
	}{
		{"list", func(out *bytes.Buffer) error { return ListSessions(dataDir, out) }},
		{"show", func(out *bytes.Buffer) error {
			return ShowSession(dataDir, "22222222222222222222222222222222", 0, false, out)
		}},
		{"talk", func(out *bytes.Buffer) error {
			return TalkSession(dataDir, "22222222222222222222222222222222", TalkOptions{}, out)
		}},
		{"grep", func(out *bytes.Buffer) error { return GrepSession(dataDir, "anything", "", false, out) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := tt.run(&out)
			if err == nil || !strings.Contains(err.Error(), "22222222222222222222222222222222") || !strings.Contains(err.Error(), "line 1") {
				t.Fatalf("error = %v, want session and malformed line", err)
			}
			if out.Len() != 0 {
				t.Fatalf("failed command presented partial output as complete: %q", out.String())
			}
		})
	}
}

func TestShowSessionReportsTruncatedTranscript(t *testing.T) {
	dataDir := t.TempDir()
	writeCorruptMessagesFixture(t, dataDir, "33333333333333333333333333333333", `{"dir":"req","model":"m1","messages":[`)
	var out bytes.Buffer
	err := ShowSession(dataDir, "33333333333333333333333333333333", 0, false, &out)
	if err == nil || !strings.Contains(err.Error(), "33333333333333333333333333333333") || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("error = %v, want session and truncated line", err)
	}
	if out.Len() != 0 {
		t.Fatalf("show printed partial output: %q", out.String())
	}
}

func TestShowSessionReportsEmptyState(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "sessions", "44444444444444444444444444444444"), 0o700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := ShowSession(dataDir, "44444444444444444444444444444444", 0, false, &out); err != nil {
		t.Fatalf("ShowSession() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "no records") {
		t.Errorf("show missing empty-state message: %q", got)
	}
}

func TestListSessionsSkipsMissingTranscript(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "sessions", "55555555555555555555555555555555"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeMessagesFixture(t, dataDir, "66666666666666666666666666666666")
	var out bytes.Buffer
	if err := ListSessions(dataDir, &out); err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if !strings.Contains(out.String(), "66666666666666666666666666666666") {
		t.Errorf("list missing valid session: %q", out.String())
	}
	if strings.Contains(out.String(), "55555555555555555555555555555555") {
		t.Errorf("list included incomplete session: %q", out.String())
	}
}

func TestSessionCommandsReportTruncatedAndStructurallyInvalidTranscripts(t *testing.T) {
	fixtures := map[string]string{
		"33333333333333333333333333333333": `{"dir":"req","model":"m1","messages":[`,
		"77777777777777777777777777777777": "{\"dir\":\"resp\",\"finish_reason\":\"stop\"}\n",
		"88888888888888888888888888888888": "{\"dir\":\"req\",\"model\":\"m1\",\"messages\":[]}\n",
	}
	for guid, contents := range fixtures {
		t.Run(guid, func(t *testing.T) {
			dataDir := t.TempDir()
			writeCorruptMessagesFixture(t, dataDir, guid, contents)
			commands := []func(*bytes.Buffer) error{
				func(out *bytes.Buffer) error { return ListSessions(dataDir, out) },
				func(out *bytes.Buffer) error { return ShowSession(dataDir, guid, 0, false, out) },
				func(out *bytes.Buffer) error { return TalkSession(dataDir, guid, TalkOptions{}, out) },
				func(out *bytes.Buffer) error { return GrepSession(dataDir, "anything", "", false, out) },
			}
			for _, run := range commands {
				var out bytes.Buffer
				err := run(&out)
				if err == nil || !strings.Contains(err.Error(), guid) {
					t.Errorf("error = %v, want affected session %s", err, guid)
				}
				if out.Len() != 0 {
					t.Errorf("command printed partial output: %q", out.String())
				}
			}
		})
	}
}

func TestRunSessionCmdTalkRejectsRemovedAllFlag(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(DataDirEnv, dataDir)
	writeMessagesFixture(t, dataDir, "99999999999999999999999999999999")

	err := RunSessionCmd([]string{"talk", "99999999999999999999999999999999", "--all"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("session talk --all succeeded; want usage error")
	}
	if strings.Contains(err.Error(), "[--all]") {
		t.Fatalf("talk usage still advertises --all: %v", err)
	}
}

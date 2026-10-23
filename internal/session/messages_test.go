package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/provider"
)

func TestMessageLogWritesJSONL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := New(dir, false)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	sink := s.MessageLogSink()
	sink.LogRequest(provider.RequestLog{
		Time: time.Now(), Dir: "req", Model: "m1",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	sink.LogResponse(provider.ResponseLog{
		Time: time.Now(), Dir: "resp", Content: "hello",
		FinishReason: "stop",
	})

	path := filepath.Join(s.Dir(), messagesName)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var dirs []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var probe struct {
			Dir string `json:"dir"`
		}
		if err := json.Unmarshal(sc.Bytes(), &probe); err != nil {
			t.Fatalf("line %q is not valid JSON: %v", sc.Text(), err)
		}
		dirs = append(dirs, probe.Dir)
	}
	if got, want := strings.Join(dirs, ","), "req,resp"; got != want {
		t.Fatalf("record order = %q, want %q", got, want)
	}
}

func TestLogRunEndAppendsEndRecord(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := New(dir, false)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer s.Close()

	s.MessageLogSink().LogResponse(provider.ResponseLog{Time: time.Now(), Dir: "resp", Content: "done", FinishReason: "stop"})
	s.LogRunEnd(provider.RunEndMaxTurns)

	f, err := os.Open(filepath.Join(s.Dir(), messagesName))
	if err != nil {
		t.Fatalf("open messages.jsonl: %v", err)
	}
	defer f.Close()
	var recs []provider.RunEndLog
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var probe struct {
			Dir string `json:"dir"`
		}
		if err := json.Unmarshal(sc.Bytes(), &probe); err != nil {
			t.Fatalf("line %q is not valid JSON: %v", sc.Text(), err)
		}
		if probe.Dir == "end" {
			var r provider.RunEndLog
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				t.Fatalf("decode end record: %v", err)
			}
			recs = append(recs, r)
		}
	}
	if len(recs) != 1 || recs[0].Reason != provider.RunEndMaxTurns {
		t.Fatalf("end records = %+v, want one with reason %q", recs, provider.RunEndMaxTurns)
	}
}

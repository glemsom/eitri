package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/provider"
)

// envelopeKeys returns the top-level JSON keys of one envelope in document
// order, so a test can pin placement rather than only presence.
func envelopeKeys(t *testing.T, line string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(line))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("decode envelope %q: %v", line, err)
	}
	if tok != json.Delim('{') {
		t.Fatalf("envelope %q does not start with an object", line)
	}
	var keys []string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			t.Fatalf("decode envelope key: %v", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			t.Fatalf("envelope key %v is not a string", keyTok)
		}
		keys = append(keys, key)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			t.Fatalf("decode envelope value for %q: %v", key, err)
		}
	}
	return keys
}

func writeBatchConfig(t *testing.T, model string) (dataDir, cfgPath string) {
	t.Helper()
	dataDir = filepath.Join(t.TempDir(), ".eitri")
	cfgPath = filepath.Join(dataDir, "config.json")
	cfg := config.Default()
	cfg.Model = model
	if err := config.Save(cfg, cfgPath); err != nil {
		t.Fatalf("config.Save() error = %v", err)
	}
	return dataDir, cfgPath
}

func runBatchJSON(t *testing.T, opts Options) string {
	t.Helper()
	var out bytes.Buffer
	opts.Format = "json"
	opts.Stdout = &out
	opts.Stderr = &bytes.Buffer{}
	opts.LookPath = okLookPath
	if opts.Prompt == "" {
		opts.Prompt = "Say hello"
	}
	if err := Run(opts); err != nil {
		t.Fatalf("Run(batch --format json) error = %v, want nil", err)
	}
	return out.String()
}

func TestRunBatchJSONEnvelopeReportsRunTelemetry(t *testing.T) {
	dataDir, cfgPath := writeBatchConfig(t, "grok-2")
	out := runBatchJSON(t, Options{
		DataDir:    dataDir,
		ConfigPath: cfgPath,
		Provider:   provider.NewFake("../provider/testdata/hello.sse"),
	})

	line := strings.TrimSpace(out)
	var env struct {
		Answer     string `json:"answer"`
		Session    string `json:"session"`
		Turns      int    `json:"turns"`
		Stopped    bool   `json:"stopped"`
		Model      string `json:"model"`
		DurationMs *int64 `json:"duration_ms"`
		ToolCalls  *int   `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		t.Fatalf("stdout %q is not one JSON object: %v", out, err)
	}
	if env.Model != "grok-2" {
		t.Fatalf("envelope model = %q, want %q", env.Model, "grok-2")
	}
	if env.DurationMs == nil {
		t.Fatal("envelope duration_ms is absent, want a millisecond wall clock")
	}
	if *env.DurationMs < 0 {
		t.Fatalf("envelope duration_ms = %d, want non-negative", *env.DurationMs)
	}
	if env.ToolCalls == nil {
		t.Fatal("envelope tool_calls is absent, want 0 for a run with no tools")
	}
	if *env.ToolCalls != 0 {
		t.Fatalf("envelope tool_calls = %d, want 0 for a run with no tools", *env.ToolCalls)
	}

	want := []string{"answer", "session", "turns", "stopped", "model", "duration_ms", "tool_calls", "usage"}
	got := envelopeKeys(t, line)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("envelope keys = %v, want %v", got, want)
	}
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("envelope is not one line: %q", out)
	}
}

func TestRunBatchJSONEnvelopeCountsExecutedToolCalls(t *testing.T) {
	dataDir, cfgPath := writeBatchConfig(t, "grok-2")
	out := runBatchJSON(t, Options{
		DataDir:    dataDir,
		ConfigPath: cfgPath,
		Provider:   scriptedBashOnly(),
		Prompt:     "probe",
	})

	var env struct {
		ToolCalls int `json:"tool_calls"`
		Turns     int `json:"turns"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil {
		t.Fatalf("stdout %q is not one JSON object: %v", out, err)
	}
	if env.Turns != 2 {
		t.Fatalf("envelope turns = %d, want 2", env.Turns)
	}
	if env.ToolCalls != 1 {
		t.Fatalf("envelope tool_calls = %d, want 1 for a single executed bash call", env.ToolCalls)
	}
}

func TestRunBatchJSONEnvelopeOmitsModelWhenUnset(t *testing.T) {
	dataDir, cfgPath := writeBatchConfig(t, "")
	out := runBatchJSON(t, Options{
		DataDir:    dataDir,
		ConfigPath: cfgPath,
		Provider: provider.NewScripted(func(context.Context, provider.Request) (provider.Stream, error) {
			return provider.StreamFunc(provider.Chunk{Content: "ok", FinishReason: "stop", Done: true}), nil
		}),
	})

	line := strings.TrimSpace(out)
	if strings.Contains(line, `"model"`) {
		t.Fatalf("envelope %q carries model for a run with no configured model", line)
	}
	want := []string{"answer", "session", "turns", "stopped", "duration_ms", "tool_calls"}
	if got := envelopeKeys(t, line); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("envelope keys = %v, want %v", got, want)
	}
}

// sleepingStream pauses before a mid-stream chunk so a run's wall clock must
// include the provider's latency rather than only its dispatch overhead.
type sleepingStream struct {
	chunks []provider.Chunk
	pause  time.Duration
	idx    int
}

func (s *sleepingStream) Next() (provider.Chunk, error) {
	if s.idx == 1 {
		time.Sleep(s.pause)
	}
	if s.idx >= len(s.chunks) {
		return provider.Chunk{}, io.EOF
	}
	c := s.chunks[s.idx]
	s.idx++
	return c, nil
}

func TestRunBatchJSONEnvelopeDurationMeasuresRunWallClock(t *testing.T) {
	dataDir, cfgPath := writeBatchConfig(t, "grok-2")
	out := runBatchJSON(t, Options{
		DataDir:    dataDir,
		ConfigPath: cfgPath,
		Provider: provider.NewScripted(func(context.Context, provider.Request) (provider.Stream, error) {
			return &sleepingStream{pause: 20 * time.Millisecond, chunks: []provider.Chunk{
				{Content: "slow"},
				{Content: " answer"},
				{FinishReason: "stop", Done: true},
			}}, nil
		}),
	})

	var env struct {
		DurationMs *int64 `json:"duration_ms"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil {
		t.Fatalf("stdout %q is not one JSON object: %v", out, err)
	}
	if env.DurationMs == nil {
		t.Fatal("envelope duration_ms is absent, want a millisecond wall clock")
	}
	if *env.DurationMs < 20 {
		t.Fatalf("envelope duration_ms = %d, want at least the 20ms the provider paused mid-stream", *env.DurationMs)
	}
}

func TestRunBatchJSONEnvelopeCountsToolCallsAcrossTurns(t *testing.T) {
	dataDir, cfgPath := writeBatchConfig(t, "grok-2")
	out := runBatchJSON(t, Options{
		DataDir:    dataDir,
		ConfigPath: cfgPath,
		Provider: provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
			var results int
			for _, m := range req.Messages {
				if m.Role == provider.RoleTool {
					results++
				}
			}
			if results < 2 {
				n := results + 1
				return provider.StreamFunc(
					provider.Chunk{Content: "probe"},
					provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
						{ID: "call_" + strconv.Itoa(n), Name: "bash", Arguments: `{"command":"echo probe-` + strconv.Itoa(n) + `"}`},
					}, Done: true},
				), nil
			}
			return provider.StreamFunc(
				provider.Chunk{Content: "done"},
				provider.Chunk{FinishReason: "stop", Done: true},
			), nil
		}),
		Prompt: "probe",
	})

	var env struct {
		ToolCalls int `json:"tool_calls"`
		Turns     int `json:"turns"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &env); err != nil {
		t.Fatalf("stdout %q is not one JSON object: %v", out, err)
	}
	if env.Turns != 3 {
		t.Fatalf("envelope turns = %d, want 3 (two tool-calling turns and the final answer)", env.Turns)
	}
	if env.ToolCalls != 2 {
		t.Fatalf("envelope tool_calls = %d, want 2 (one executed bash call per tool-calling turn)", env.ToolCalls)
	}
}

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tools"
	"github.com/glemsom/eitri/internal/tui"
)

func scriptedBashOnly() *provider.Scripted {
	return provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		hasTool := false
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				hasTool = true
			}
		}
		if !hasTool {
			return provider.StreamFunc(
				provider.Chunk{Content: "", ReasoningContent: "run a probe"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_1", Name: "bash", Arguments: `{"command":"echo sandbox-ok"}`},
				}, Done: true},
			), nil
		}
		last := ""
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == provider.RoleTool {
				last = req.Messages[i].Content
				break
			}
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "got: " + last},
			provider.Chunk{FinishReason: "stop", Done: true},
		), nil
	})
}

func TestBatchDispatchesToolThroughRegistry(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	ws := filepath.Join(home, ".eitri-app-ws")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(ws); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	defer os.RemoveAll(ws)

	var out bytes.Buffer
	dir := t.TempDir()
	err = Run(Options{
		DataDir:  filepath.Join(dir, ".eitri"),
		LookPath: okLookPath,
		Provider: scriptedBashOnly(),
		Prompt:   "run a probe",
		Stdout:   &out,
	})
	if err != nil {
		t.Fatalf("Run(batch) error = %v, want nil", err)
	}
	if !strings.Contains(out.String(), "sandbox-ok") {
		t.Fatalf("batch stdout = %q, want it to carry sandbox output 'sandbox-ok'", out.String())
	}
}

type stubFetcher struct {
	body string
}

func (s *stubFetcher) Fetch(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(s.body)), nil
}

type recordingBrowser struct {
	targets []string
}

func (r *recordingBrowser) Open(_ context.Context, target string) error {
	r.targets = append(r.targets, target)
	return nil
}

func scriptedWebTurn() *provider.Scripted {
	return provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		hasTool := false
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				hasTool = true
			}
		}
		if !hasTool {
			return provider.StreamFunc(
				provider.Chunk{Content: "", ReasoningContent: "fetch the page"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_f", Name: "web_fetch", Arguments: `{"url":"https://example.com/doc"}`},
				}, Done: true},
			), nil
		}
		last := ""
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == provider.RoleTool {
				last = req.Messages[i].Content
				break
			}
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "answer: " + last},
			provider.Chunk{FinishReason: "stop", Done: true},
		), nil
	})
}

func TestBatchWebFetchThroughEngineSeam(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	ws := filepath.Join(home, ".eitri-app-wf")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(ws); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	defer os.RemoveAll(ws)

	var out bytes.Buffer
	dir := t.TempDir()
	err = Run(Options{
		DataDir:  filepath.Join(dir, ".eitri"),
		LookPath: okLookPath,
		Provider: scriptedWebTurn(),
		Fetcher:  &stubFetcher{body: `<html><body><h1>Docs</h1><p>Hello <strong>world</strong>.</p></body></html>`},
		Browser:  &recordingBrowser{},
		Prompt:   "fetch the page",
		Stdout:   &out,
	})
	if err != nil {
		t.Fatalf("Run(batch web_fetch) error = %v, want nil", err)
	}
	if !strings.Contains(out.String(), "# Docs") {
		t.Fatalf("batch stdout = %q, want markdown from web_fetch to reach the answer", out.String())
	}
}

func scriptedBrowserTurn() *provider.Scripted {
	return provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		hasTool := false
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				hasTool = true
			}
		}
		if !hasTool {
			return provider.StreamFunc(
				provider.Chunk{Content: "", ReasoningContent: "open the report"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_b", Name: "open_in_browser", Arguments: `{"path":"file:///tmp/report.html"}`},
				}, Done: true},
			), nil
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "opened ok"},
			provider.Chunk{FinishReason: "stop", Done: true},
		), nil
	})
}

func scriptedEditTurn(ws string) *provider.Scripted {
	return provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		hasTool := false
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				hasTool = true
			}
		}
		if !hasTool {
			return provider.StreamFunc(
				provider.Chunk{Content: "", ReasoningContent: "edit the file"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_e", Name: "edit", Arguments: `{"path":"` + ws + `/main.go","old_string":"b","new_string":"b\nc\nd"}`},
				}, Done: true},
			), nil
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "edited"},
			provider.Chunk{FinishReason: "stop", Done: true},
		), nil
	})
}

func TestBatchEditToolReportsLineDelta(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	ws := filepath.Join(home, ".eitri-app-del", filepath.Base(t.TempDir()))
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	defer os.RemoveAll(ws)
	if err := os.WriteFile(filepath.Join(ws, "main.go"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	reg := tools.NewRegistry(tools.Deps{
		Workspace: ws,
		TempHost:  filepath.Join(t.TempDir(), "eitri-tmp"),
		Runner:    tools.RealRunner,
	})
	obs := tui.NewDeltaObserver(fileDeltaResolver(reg))
	e := engine.New(scriptedEditTurn(ws), mockTranscript{})
	var gotAdded, gotRemoved int
	var gotBefore, gotAfter, gotPath string
	e.SetListener(func(ev engine.Event) {
		switch ev := ev.(type) {
		case engine.ToolCallEvent:
			obs.Start(ev.ID, ev.Name, ev.Arguments)
		case engine.ToolResultEvent:
			gotAdded, gotRemoved, gotBefore, gotAfter, gotPath = obs.Result(ev.ID, ev.Name)
		}
	})

	_, err = e.RunAgent(context.Background(), engine.RunRequest{Model: "deepseek-v4-flash", Prompt: "edit"},
		engine.AgentOptions{
			Tools: providerTools(reg.Definitions()),
			Executor: engine.ExecutorFunc(func(ctx context.Context, name, argsJSON string) (engine.ToolExecResult, error) {
				var args map[string]any
				if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
					return engine.ToolExecResult{}, err
				}
				res, err := reg.Run(ctx, name, args)
				if err != nil {
					return engine.ToolExecResult{}, err
				}
				return engine.ToolExecResult{Text: res.Text, Compressed: res.Compressed}, nil
			}),
			MaxTurns: 5,
		})
	if err != nil {
		t.Fatalf("RunAgent(edit) error = %v", err)
	}

	editResult := filepath.Join(ws, "main.go")
	if data, _ := os.ReadFile(editResult); string(data) != "a\nb\nc\nd\n" {
		t.Errorf("fixture after edit = %q, want \"a\nb\nc\nd\n\"", string(data))
	}
	if gotAdded != 2 || gotRemoved != 0 {
		t.Errorf("edit delta = +%d-%d, want +2-0", gotAdded, gotRemoved)
	}
	if gotBefore != "a\nb\n" || gotAfter != "a\nb\nc\nd\n" {
		t.Errorf("content = before %q after %q, want a\nb\n -> a\nb\nc\nd\n", gotBefore, gotAfter)
	}
	if gotPath != editResult {
		t.Errorf("Path = %q, want %q", gotPath, editResult)
	}
}

func TestBatchOpenInBrowserThroughEngineSeam(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	ws := filepath.Join(home, ".eitri-app-br")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(ws); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	defer os.RemoveAll(ws)

	br := &recordingBrowser{}
	var out bytes.Buffer
	dir := t.TempDir()
	err = Run(Options{
		DataDir:  filepath.Join(dir, ".eitri"),
		LookPath: okLookPath,
		Provider: scriptedBrowserTurn(),
		Fetcher:  &stubFetcher{body: "<html><body></body></html>"},
		Browser:  br,
		Prompt:   "open the report",
		Stdout:   &out,
	})
	if err != nil {
		t.Fatalf("Run(batch open_in_browser) error = %v, want nil", err)
	}
	if len(br.targets) != 1 || !strings.HasPrefix(br.targets[0], "file:///tmp/eitri-") || !strings.HasSuffix(br.targets[0], "/report.html") {
		t.Fatalf("browser targets = %v, want one host-translated file:///tmp/eitri-*/report.html", br.targets)
	}
}

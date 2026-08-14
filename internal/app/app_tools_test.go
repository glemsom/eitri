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

// scriptedBashOnly answers with a single bash tool call, then echoes the tool
// result in a final answer. It drives the app's batch dispatch path against the
// real sandbox registry wiring.
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

// TestBatchDispatchesToolThroughRegistry runs batch mode end-to-end with a
// scripted provider that makes a bash tool call, verifying app.Run wires the
// shared tool registry into the engine and the final answer carries real
// sandbox output.
func TestBatchDispatchesToolThroughRegistry(t *testing.T) {
	// The batch engine uses the current working directory as the workspace.
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

// stubFetcher serves a canned HTML body for engine-seam web_fetch turns.
type stubFetcher struct {
	body string
}

func (s *stubFetcher) Fetch(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(s.body)), nil
}

// recordingBrowser records the target handed to open_in_browser, standing in
// for the host browser launch.
type recordingBrowser struct {
	targets []string
}

func (r *recordingBrowser) Open(_ context.Context, target string) error {
	r.targets = append(r.targets, target)
	return nil
}

// scriptedWebTurn drives batch mode through a provider that makes one web_fetch
// call, then echoes the fetched markdown as the final answer. It exercises the
// engine seam's dispatch of web_fetch end-to-end.
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

// TestBatchWebFetchThroughEngineSeam runs batch mode with a fake provider issuing
// a web_fetch turn and asserts the fetched content is converted to markdown and
// routed back through the tool-result channel into the final answer.
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

// scriptedBrowserTurn drives batch mode through a provider that makes one
// open_in_browser call on a file in the session temp, then confirms.
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

// scriptedEditTurn drives a scripted provider through one edit tool call on a
// workspace file, then confirms. It exercises the app's real edit tool path
// end-to-end against the shared registry path resolution (issue #174).
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

// TestBatchEditToolReportsLineDelta drives a real edit tool call through the
// app's shared registry and asserts the TUI-side delta observer (issue #174)
// computes the same before/after line delta the engine's ToolDelta seam used
// to report (issue #84 AC3): the file gains two lines as one is swapped for
// three, so the observer reports +2, -0. The observer is fed from the engine's
// event stream (ToolCallEvent → pre-edit snapshot, ToolResultEvent → diff)
// exactly as the app's TUI listener wires it, and the engine itself carries no
// ToolDelta seam — the batch run stays byte-identical.
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
	// The observer's path-resolution seam is the same wiring runTUI uses: the
	// registry's shared path translator + workspace root (issue #174).
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
			Executor: engine.ExecutorFunc(func(ctx context.Context, name, argsJSON string) (string, error) {
				var args map[string]any
				if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
					return "", err
				}
				return reg.Run(ctx, name, args)
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
	// The observer must also report the real before/after file content and host
	// path so the TUI review panel can render an inline diff and hand the file
	// to the browser (issue #90 / #174).
	if gotBefore != "a\nb\n" || gotAfter != "a\nb\nc\nd\n" {
		t.Errorf("content = before %q after %q, want a\nb\n -> a\nb\nc\nd\n", gotBefore, gotAfter)
	}
	if gotPath != editResult {
		t.Errorf("Path = %q, want %q", gotPath, editResult)
	}
}

// issuing an open_in_browser turn on a session-temp file:// target, asserting the
// host-side launch translates the sandbox /tmp path to the host /tmp/eitri-GUID
// form.
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
	// The GUID is random per run, so assert the host temp form structurally
	// (root /tmp/eitri- + the file path) rather than an exact GUID.
	if len(br.targets) != 1 || !strings.HasPrefix(br.targets[0], "file:///tmp/eitri-") || !strings.HasSuffix(br.targets[0], "/report.html") {
		t.Fatalf("browser targets = %v, want one host-translated file:///tmp/eitri-*/report.html", br.targets)
	}
}

package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
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

// TestBatchOpenInBrowserThroughEngineSeam runs batch mode with a fake provider
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

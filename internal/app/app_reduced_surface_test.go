package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// reducedToolTurn asserts, on the first request, that the provider receives
// exactly the reduced tool surface — bash, open_in_browser — and never the
// removed web/read/write/edit/skill tools. It then answers directly so
// the batch run completes.
func reducedToolTurn() *provider.Scripted {
	return provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		names := map[string]bool{}
		for _, t := range req.Tools {
			names[t.Function.Name] = true
		}
		if len(names) != 2 {
			return provider.StreamFunc(
				provider.Chunk{Content: fmt.Sprintf("tool count = %d, want exactly 2", len(names)), FinishReason: "stop", Done: true},
			), nil
		}
		for _, want := range []string{"bash", "open_in_browser"} {
			if !names[want] {
				return provider.StreamFunc(
					provider.Chunk{Content: "missing tool: " + want, FinishReason: "stop", Done: true},
				), nil
			}
		}
		for _, banned := range []string{"read", "write", "edit", "skill", "web_fetch"} {
			if names[banned] {
				return provider.StreamFunc(
					provider.Chunk{Content: "forbidden tool present: " + banned, FinishReason: "stop", Done: true},
				), nil
			}
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "reduced surface verified", FinishReason: "stop", Done: true},
		), nil
	})
}

func TestBatchProviderToolsAreExactlyReducedSet(t *testing.T) {
	var out bytes.Buffer
	dir := t.TempDir()
	err := Run(Options{
		DataDir:  filepath.Join(dir, ".eitri"),
		LookPath: okLookPath,
		Prompt:   "verify the reduced surface",
		Stdout:   &out,
		Provider: reducedToolTurn(),
	})
	if err != nil {
		t.Fatalf("Run(batch) error = %v, want nil", err)
	}
	if !strings.Contains(out.String(), "reduced surface verified") {
		t.Fatalf("batch stdout = %q, want the reduced-surface confirmation", out.String())
	}
}

// bashHeredocSedTurn drives a bash heredoc write then a bash `sed -i` edit
// across two tool turns, then stops. The round-trip is asserted on the file
// that lands in the workspace after Run returns.
func bashHeredocSedTurn(t *testing.T) *provider.Scripted {
	t.Helper()
	return provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		hasTool := false
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				hasTool = true
			}
		}
		if !hasTool {
			return provider.StreamFunc(
				provider.Chunk{Content: "", ReasoningContent: "write then edit a file"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_w", Name: "bash", Arguments: `{"command":"cat > note.txt <<'EOF'\napple\nbanana\ncherry\nEOF"}`},
				}, Done: true},
			), nil
		}
		// Count prior bash tool calls to sequence write → sed -i.
		bashCalls := 0
		for i := len(req.Messages) - 1; i >= 0; i-- {
			m := req.Messages[i]
			if m.Role == provider.RoleAssistant {
				for _, tc := range m.ToolCalls {
					if tc.Name == "bash" {
						bashCalls++
					}
				}
			}
		}
		if bashCalls < 2 {
			return provider.StreamFunc(
				provider.Chunk{Content: "", ReasoningContent: "edit the file"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_e", Name: "bash", Arguments: `{"command":"sed -i 's/banana/blueberry/' note.txt"}`},
				}, Done: true},
			), nil
		}
		// After the sed -i edit, the round-trip is asserted on the workspace file.
		return provider.StreamFunc(
			provider.Chunk{Content: "edited ok", FinishReason: "stop", Done: true},
		), nil
	})
}

func TestBatchHeredocWriteAndSedEditRoundTrip(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	ws := filepath.Join(home, ".eitri-app-heredoc-sed")
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
		Provider: bashHeredocSedTurn(t),
		Prompt:   "write and edit a file",
		Stdout:   &out,
	})
	if err != nil {
		t.Fatalf("Run(batch) error = %v, want nil", err)
	}

	data, rerr := os.ReadFile(filepath.Join(ws, "note.txt"))
	if rerr != nil {
		t.Fatalf("read note.txt: %v", rerr)
	}
	want := "apple\nblueberry\ncherry\n"
	if string(data) != want {
		t.Fatalf("note.txt = %q, want %q (heredoc write + sed -i edit not round-tripped)", string(data), want)
	}
}

package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// proxyBody is the wire request shape the OpenCode-shaped proxy server sees. It
// mirrors only the field the cache-prefix assertion needs — the message list —
// so a recorded request body can be decoded independent of provider internals.
type proxyBody struct {
	Messages []provider.Message `json:"messages"`
}

// proxyHandler is a recorded OpenCode-proxy stand-in: an httptest server that
// captures the exact request body of every turn and serves the recorded SSE
// turn-by-turn. Sending the request body through the real OpenAICompatible HTTP
// client means the bytes it marshals are exactly what the proxy gateway would
// see — the proxy-shaped (not unit-marshaled) view of the head.
type proxyHandler struct {
	fixtures []string // recorded .sse fixtures, one per turn
	bodies   [][]byte // raw request bodies, one per received turn
	turns    int
}

func (h *proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.bodies = append(h.bodies, body)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	if h.turns >= len(h.fixtures) {
		// An unexpected extra turn is a test failure, not a silent empty stream:
		// surfacing a non-2xx here fails the RunAgent call loudly instead of
		// masking the missing recorded fixture.
		http.Error(w, "unexpected turn: fixture exhausted", http.StatusTeapot)
		return
	}
	_, _ = w.Write([]byte(h.fixtures[h.turns]))
	h.turns++
}

// proxyFixture returns the recorded SSE fixture bytes for the named turn, or
// fails the test if the fixture cannot be read. The D3 recorded session lives
// under the provider package's testdata/, so tests here reach it
// via the package-relative path.
func proxyFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../provider/testdata/" + name)
	if err != nil {
		t.Fatalf("read recorded fixture %s: %v", name, err)
	}
	return string(b)
}

// TestRunAgentKeepsByteIdenticalHeadThroughProxy drives the engine seam over a
// recorded 2-turn, OpenCode-proxy-shaped session: requests go out through the
// real OpenAI-compatible HTTP client (so the head bytes are the actual marshaled
// wire body) and responses are the recorded SSE stream. The shared-prefix head
// must marshal byte-identically across turns and only grow at the tail — the
// proxy-shaped form of the prompt-cache invariant that unit marshaling tests
// cannot observe.
func TestRunAgentKeepsByteIdenticalHeadThroughProxy(t *testing.T) {
	t.Parallel()
	h := &proxyHandler{
		fixtures: []string{
			proxyFixture(t, "proxy-turn1.sse"), // turn 1: tool_calls (read)
			proxyFixture(t, "proxy-turn2.sse"), // turn 2: final answer
		},
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	cl := provider.NewOpenAICompatible("test-key", srv.URL+"/v1/chat/completions")
	e := New(cl, &mockTranscript{})

	res, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "read the file",
		SessionKey: "sess-proxy",
	}, AgentOptions{
		Tools:      strictToolDefs(),
		ToolChoice: "auto",
		// Executor stubs the recorded read tool call with a canned result so
		// the tool-result turn appends deterministically.
		Executor: &mockToolRecorder{},
		MaxTurns: 10,
	})
	if err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}
	if res.Answer != "The file contains the data." {
		t.Errorf("final answer = %q, want %q", res.Answer, "The file contains the data.")
	}
	if res.Usage == nil {
		t.Fatal("recorded proxy usage not propagated to Result")
	}
	if res.Usage.PromptCacheHitTokens != 112 {
		t.Errorf("proxy turn-2 cache hit = %d, want 112 (recorded fixture)", res.Usage.PromptCacheHitTokens)
	}

	if len(h.bodies) != 2 {
		t.Fatalf("proxy received %d request bodies, want 2 turns", len(h.bodies))
	}

	// Decode each recorded wire body to its message list. Marshaling through the
	// real HTTP client is the point: the bytes compared below are the exact
	// request body the OpenCode proxy would cache against.
	heads := make([][]provider.Message, len(h.bodies))
	for i, body := range h.bodies {
		var pb proxyBody
		if err := json.Unmarshal(body, &pb); err != nil {
			t.Fatalf("turn %d: decode recorded body: %v", i, err)
		}
		heads[i] = pb.Messages
	}

	head1 := headMessages(heads[0])
	head2 := headMessages(heads[1])

	// The shared prefix of the two recorded request bodies must be byte-identical
	// and the head may only grow (the appended assistant tool-call turn + tool
	// result), never be rewritten in place.
	shared := min(len(head1), len(head2))
	if shared == 0 {
		t.Fatal("no shared request head to compare")
	}
	for i := range shared {
		b1, _ := json.Marshal(head1[i])
		b2, _ := json.Marshal(head2[i])
		if !bytes.Equal(b1, b2) {
			t.Errorf("request-head message %d not byte-identical through proxy:\n turn1=%s\n turn2=%s", i, b1, b2)
		}
	}
	if len(head2) <= len(head1) {
		t.Errorf("proxy head grew by %d messages, want strictly more (appended at tail)", len(head2)-len(head1))
	}
}

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

type proxyBody struct {
	Messages []provider.Message `json:"messages"`
}

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
		http.Error(w, "unexpected turn: fixture exhausted", http.StatusTeapot)
		return
	}
	_, _ = w.Write([]byte(h.fixtures[h.turns]))
	h.turns++
}

func proxyFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../provider/testdata/" + name)
	if err != nil {
		t.Fatalf("read recorded fixture %s: %v", name, err)
	}
	return string(b)
}

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
		Executor:   &mockToolRecorder{},
		MaxTurns:   10,
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

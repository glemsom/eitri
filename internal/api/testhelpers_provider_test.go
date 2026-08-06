package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// This file holds HTTP fake-server helpers shared by both the fast
// (non-browser) integration tests and the browser E2E tests. It is NOT build
// tagged, so it compiles in every build. Browser-E2E-only helpers live in the
// `e2e`-tagged browser_*.go files.

// fakeChatServer returns an httptest.Server that acts as an OpenAI-compatible
// LLM provider for chat tests. It handles:
//   - GET /v1/models — returns a model list for config validation
//   - POST /v1/chat/completions — returns a streaming SSE chat completion
//
// Mode "ok" returns a short completion, "error" returns a 500 error.
func fakeChatServer(t *testing.T, mode string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)

		case "/v1/chat/completions":
			if mode == "error" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				io.WriteString(w, `{"error":{"message":"Internal error","type":"server_error"}}`)
				return
			}

			// Streaming SSE response
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming not supported", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			// Initial chunk with role
			now := time.Now().Unix()
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`+"\n\n", now)
			flusher.Flush()

			// Content chunks
			for _, word := range []string{"Hello", " from", " the", " fake", " LLM"} {
				fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"content":"%s"},"finish_reason":null}]}`+"\n\n", now, word)
				flusher.Flush()
				time.Sleep(5 * time.Millisecond)
			}

			// Final stop chunk
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n", now)
			flusher.Flush()

			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// configureProvider saves runnable LLM provider config to test server via HTTP.
func configureProvider(t *testing.T, server *httptest.Server, llmURL string) {
	t.Helper()
	putBrowserConfig(t, server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"test-model"}`, llmURL))
}

// putBrowserConfig saves an arbitrary config JSON body to the test server via PUT /api/config.
func putBrowserConfig(t *testing.T, server *httptest.Server, body string) {
	t.Helper()
	req, err := http.NewRequest("PUT", server.URL+"/api/config", strings.NewReader(body))
	if err != nil {
		t.Fatalf("failed to create config request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to PUT config: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&errResp) == nil {
			t.Fatalf("config save failed (status %d): %s", resp.StatusCode, errResp.Error)
		}
		t.Fatalf("config save failed with status %d", resp.StatusCode)
	}
}

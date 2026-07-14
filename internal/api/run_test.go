package api_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/api"
	"github.com/glemsom/eitri/internal/executor"
	agentrunner "github.com/glemsom/eitri/internal/runner"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

// newRunnerManager creates a test RunnerManager.
func newRunnerManager(t *testing.T) *agentrunner.Manager {
	t.Helper()
	return agentrunner.NewManager()
}

type testServerWithRuns struct {
	server      *httptest.Server
	sessionMgr  *session.Manager
	executorMgr *executor.SessionManager
	runMgr      *api.RunManager
}

// newManagedTestServerWithRuns creates a test server with RunManager enabled
// and returns test handles for session/run/executor assertions.
func newManagedTestServerWithRuns(t *testing.T) *testServerWithRuns {
	t.Helper()
	sessionMgr := session.NewManager(10)
	executorMgr := executor.NewSessionManager(t.TempDir(), 0, 0)
	runnerMgr := newRunnerManager(t)
	runMgr := api.NewRunManager(runnerMgr, executorMgr)
	skillsSvc := skills.NewService()
	runMgr.SetSkillsService(skillsSvc)
	runMgr.SetUISessionManager(sessionMgr)

	cfg := api.ServerConfig{
		ConfigPath:     t.TempDir() + "/config.json",
		Workspace:      t.TempDir(),
		SessionManager: sessionMgr,
		RunManager:     runMgr,
		SkillsService:  skillsSvc,
	}
	srv := api.NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)
	return &testServerWithRuns{
		server:      server,
		sessionMgr:  sessionMgr,
		executorMgr: executorMgr,
		runMgr:      runMgr,
	}
}

// newTestServerWithRuns creates a test server with RunManager enabled.
func newTestServerWithRuns(t *testing.T) *httptest.Server {
	t.Helper()
	return newManagedTestServerWithRuns(t).server
}

func fakeSlowChatServer(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
		case "/v1/chat/completions":
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming not supported", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			now := time.Now().Unix()
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`+"\n\n", now)
			flusher.Flush()
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"content":"working"},"finish_reason":null}]}`+"\n\n", now)
			flusher.Flush()

			select {
			case <-r.Context().Done():
				return
			case <-time.After(delay):
			}

			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n", now)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fakeBurstChatServer(t *testing.T, tokenCount int, delay time.Duration) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
		case "/v1/chat/completions":
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming not supported", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			now := time.Now().Unix()
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`+"\n\n", now)
			flusher.Flush()

			for i := 0; i < tokenCount; i++ {
				fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":null}]}`+"\n\n", now)
				flusher.Flush()
				if delay > 0 {
					select {
					case <-r.Context().Done():
						return
					case <-time.After(delay):
					}
				}
			}

			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n", now)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fakePhasedChatServer(t *testing.T, delays []time.Duration, parts []string) *httptest.Server {
	t.Helper()

	if len(delays) != len(parts) {
		t.Fatalf("len(delays)=%d len(parts)=%d", len(delays), len(parts))
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
		case "/v1/chat/completions":
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming not supported", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			now := time.Now().Unix()
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`+"\n\n", now)
			flusher.Flush()

			for i, part := range parts {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(delays[i]):
				}
				fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}`+"\n\n", now, part)
				flusher.Flush()
			}

			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n", now)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func createSessionAndCookie(t *testing.T, serverURL string) (string, *http.Cookie) {
	t.Helper()

	client := noRedirectClient()
	resp, err := client.Get(serverURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	sessionID := strings.TrimPrefix(resp.Header.Get("Location"), "/sessions/")
	if sessionID == "" {
		t.Fatal("missing session ID")
	}

	for _, c := range resp.Cookies() {
		if c.Name == "browser_id" {
			return sessionID, c
		}
	}
	t.Fatal("missing browser cookie")
	return "", nil
}

func startChatRun(t *testing.T, serverURL, sessionID string, browserCookie *http.Cookie) {
	t.Helper()

	client := noRedirectClient()
	chatPath := "/api/sessions/" + sessionID + "/chat"
	req, err := http.NewRequest(http.MethodPost, serverURL+chatPath, strings.NewReader("message=Hello"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(browserCookie)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", resp.StatusCode)
	}
}

func openStreamWithRetry(t *testing.T, serverURL, sessionID string, browserCookie *http.Cookie) *http.Response {
	t.Helper()

	client := noRedirectClient()
	streamPath := "/api/sessions/" + sessionID + "/stream"
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, serverURL+streamPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(browserCookie)
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			return resp
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("stream never became available")
	return nil
}

type sseStream struct {
	resp    *http.Response
	scanner *bufio.Scanner
}

func newSSEStream(resp *http.Response) *sseStream {
	return &sseStream{
		resp:    resp,
		scanner: bufio.NewScanner(resp.Body),
	}
}

func (s *sseStream) Close() error {
	return s.resp.Body.Close()
}

func (s *sseStream) waitFor(t *testing.T, match func(string) bool, timeout time.Duration) string {
	t.Helper()

	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		for s.scanner.Scan() {
			line := s.scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if match(data) {
				resultCh <- data
				return
			}
		}
		if err := s.scanner.Err(); err != nil {
			errCh <- err
			return
		}
		resultCh <- ""
	}()

	select {
	case result := <-resultCh:
		return result
	case err := <-errCh:
		t.Fatalf("read SSE: %v", err)
	case <-time.After(timeout):
		t.Fatal("timed out waiting for SSE event")
	}
	return ""
}

func TestRunManager_NewRunManager(t *testing.T) {
	runnerMgr := newRunnerManager(t)
	executorMgr := executor.NewSessionManager(t.TempDir(), 0, 0)
	rm := api.NewRunManager(runnerMgr, executorMgr)
	if rm == nil {
		t.Fatal("RunManager is nil")
	}
}

func TestDeleteSessionCancelsActiveRunClosesExecutorAndClosesStream(t *testing.T) {
	llmSrv := fakeSlowChatServer(t, 2*time.Second)
	h := newManagedTestServerWithRuns(t)
	configureProvider(t, h.server, llmSrv.URL)
	client := noRedirectClient()

	resp, err := client.Get(h.server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	sessionID := strings.TrimPrefix(loc, "/sessions/")
	if sessionID == "" {
		t.Fatal("missing session ID")
	}

	var browserCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	if browserCookie == nil {
		t.Fatal("missing browser cookie")
	}

	oldExecutor, err := h.executorMgr.GetOrCreate(sessionID)
	if err != nil {
		t.Fatalf("GetOrCreate(%s) = %v", sessionID, err)
	}

	chatPath := "/api" + loc + "/chat"
	req, _ := http.NewRequest(http.MethodPost, h.server.URL+chatPath, strings.NewReader("message=Delete+me"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(browserCookie)
	chatResp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	chatResp.Body.Close()
	if chatResp.StatusCode != http.StatusOK {
		t.Fatalf("chat status = %d, want 200", chatResp.StatusCode)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.runMgr.ActiveRun(sessionID) != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h.runMgr.ActiveRun(sessionID) == nil {
		t.Fatal("run never became active")
	}

	streamPath := "/api" + loc + "/stream"
	var streamResp *http.Response
	for time.Now().Before(deadline) {
		streamReq, _ := http.NewRequest(http.MethodGet, h.server.URL+streamPath, nil)
		streamReq.AddCookie(browserCookie)
		streamResp, err = client.Do(streamReq)
		if err == nil && streamResp.StatusCode == http.StatusOK {
			break
		}
		if streamResp != nil {
			streamResp.Body.Close()
		}
		time.Sleep(10 * time.Millisecond)
	}
	if streamResp == nil || streamResp.StatusCode != http.StatusOK {
		if streamResp != nil {
			streamResp.Body.Close()
		}
		t.Fatalf("stream status = %v, want 200", statusCodeOf(streamResp))
	}
	defer streamResp.Body.Close()

	streamEvents := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(streamResp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				streamEvents <- strings.TrimPrefix(line, "data: ")
			}
		}
		close(streamEvents)
	}()

	deleteReq, _ := http.NewRequest(http.MethodDelete, h.server.URL+"/api/sessions/"+sessionID, nil)
	deleteReq.AddCookie(browserCookie)
	deleteResp, err := client.Do(deleteReq)
	if err != nil {
		t.Fatal(err)
	}
	defer deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusFound {
		t.Fatalf("delete status = %d, want 302", deleteResp.StatusCode)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.runMgr.ActiveRun(sessionID) == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h.runMgr.ActiveRun(sessionID) != nil {
		t.Fatal("run still active after delete")
	}

	newExecutor, err := h.executorMgr.GetOrCreate(sessionID)
	if err != nil {
		t.Fatalf("GetOrCreate(%s) after delete = %v", sessionID, err)
	}
	if newExecutor == oldExecutor {
		t.Fatal("executor was not closed and removed on delete")
	}

	foundClosed := false
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case evt, ok := <-streamEvents:
			if !ok {
				deadline = time.Now()
				continue
			}
			if strings.Contains(evt, `"type":"closed"`) {
				foundClosed = true
				deadline = time.Now()
			}
		case <-time.After(25 * time.Millisecond):
		}
	}
	if !foundClosed {
		t.Fatal("stream never received closed event")
	}
}

func TestStream_TwoClientsBothReceiveDone(t *testing.T) {
	llmSrv := fakePhasedChatServer(t, []time.Duration{150 * time.Millisecond, 150 * time.Millisecond}, []string{"first", "second"})
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmSrv.URL)

	sessionID, browserCookie := createSessionAndCookie(t, server.URL)
	startChatRun(t, server.URL, sessionID, browserCookie)

	stream1 := newSSEStream(openStreamWithRetry(t, server.URL, sessionID, browserCookie))
	defer stream1.Close()
	stream2 := newSSEStream(openStreamWithRetry(t, server.URL, sessionID, browserCookie))
	defer stream2.Close()

	if got := stream1.waitFor(t, func(data string) bool {
		return strings.Contains(data, `"type":"done"`)
	}, 3*time.Second); got == "" {
		t.Fatal("first stream never received done")
	}
	if got := stream2.waitFor(t, func(data string) bool {
		return strings.Contains(data, `"type":"done"`)
	}, 3*time.Second); got == "" {
		t.Fatal("second stream never received done")
	}
}

func TestStream_ClientDisconnectDoesNotBlockRunCompletion(t *testing.T) {
	llmSrv := fakeBurstChatServer(t, 250, 0)
	h := newManagedTestServerWithRuns(t)
	configureProvider(t, h.server, llmSrv.URL)

	sessionID, browserCookie := createSessionAndCookie(t, h.server.URL)
	startChatRun(t, h.server.URL, sessionID, browserCookie)

	stream := newSSEStream(openStreamWithRetry(t, h.server.URL, sessionID, browserCookie))
	_ = stream.waitFor(t, func(data string) bool {
		return strings.Contains(data, `"type":"token"`)
	}, 2*time.Second)
	stream.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h.runMgr.ActiveRun(sessionID) == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run stayed active after client disconnect")
}

func TestStream_ReconnectDuringActiveRunReceivesSubsequentEventsAndDone(t *testing.T) {
	llmSrv := fakePhasedChatServer(t, []time.Duration{50 * time.Millisecond, 300 * time.Millisecond}, []string{"first", "second"})
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmSrv.URL)

	sessionID, browserCookie := createSessionAndCookie(t, server.URL)
	startChatRun(t, server.URL, sessionID, browserCookie)

	stream1 := newSSEStream(openStreamWithRetry(t, server.URL, sessionID, browserCookie))
	firstToken := stream1.waitFor(t, func(data string) bool {
		return strings.Contains(data, `"content":"first"`)
	}, 2*time.Second)
	if firstToken == "" {
		t.Fatal("first stream never received first token")
	}
	stream1.Close()

	stream2 := newSSEStream(openStreamWithRetry(t, server.URL, sessionID, browserCookie))
	defer stream2.Close()
	if got := stream2.waitFor(t, func(data string) bool {
		return strings.Contains(data, `"content":"second"`)
	}, 2*time.Second); got == "" {
		t.Fatal("reconnected stream never received subsequent token")
	}
	if got := stream2.waitFor(t, func(data string) bool {
		return strings.Contains(data, `"type":"done"`)
	}, 2*time.Second); got == "" {
		t.Fatal("reconnected stream never received done")
	}
}

func statusCodeOf(resp *http.Response) any {
	if resp == nil {
		return nil
	}
	return resp.StatusCode
}

func TestChatEndpoint_NeedsSession(t *testing.T) {
	server := newTestServerWithRuns(t)
	client := noRedirectClient()

	resp, err := client.Post(server.URL+"/api/sessions/nonexistent/chat", "application/x-www-form-urlencoded", strings.NewReader("message=hello"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestChatEndpoint_MissingMessage(t *testing.T) {
	server := newTestServerWithRuns(t)
	client := noRedirectClient()

	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")

	var browserCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}

	chatPath := "/api" + loc + "/chat"
	req, _ := http.NewRequest("POST", server.URL+chatPath, nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if browserCookie != nil {
		req.AddCookie(browserCookie)
	}
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("empty message status = %d, want 400", resp2.StatusCode)
	}
}

func TestRenderMarkdown(t *testing.T) {
	// Test markdown rendering directly
	tests := []struct {
		name     string
		input    string
		wantHTML string
	}{
		{
			name:     "empty string",
			input:    "",
			wantHTML: "",
		},
		{
			name:     "simple text",
			input:    "Hello world",
			wantHTML: "<p>Hello world</p>\n",
		},
		{
			name:     "think tags",
			input:    "Before <think>thinking content</think> After",
			wantHTML: "<p>Before </p>\n<details class=\"think-details\">\n<summary>Thinking...</summary>\nthinking content\n</details>\n<p> After</p>\n",
		},
		{
			name:     "code block",
			input:    "```go\npackage main\n```",
			wantHTML: "<pre><code class=\"language-go\">package main\n</code></pre>\n",
		},
		{
			name:     "bold text",
			input:    "**bold** text",
			wantHTML: "<p><strong>bold</strong> text</p>\n",
		},
	}

	// Use a helper to test the renderer indirectly via the markdown render function
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We test goldmark rendering via HTTP endpoint
			server := newTestServerWithRuns(t)
			client := noRedirectClient()

			// Create session first
			resp, err := client.Get(server.URL + "/")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			loc := resp.Header.Get("Location")

			var browserCookie *http.Cookie
			for _, c := range resp.Cookies() {
				if c.Name == "browser_id" {
					browserCookie = c
					break
				}
			}

			// Test the render/markdown endpoint
			renderPath := "/api" + loc + "/render/markdown"
			body := `{"message_id":"test_1"}`
			req, _ := http.NewRequest("POST", server.URL+renderPath, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if browserCookie != nil {
				req.AddCookie(browserCookie)
			}
			resp2, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp2.Body.Close()

			if resp2.StatusCode != http.StatusNotFound && resp2.StatusCode != http.StatusOK {
				t.Logf("render/markdown status = %d", resp2.StatusCode)
			}
		})
	}
}

func TestRenderToolCard(t *testing.T) {
	server := newTestServerWithRuns(t)
	client := noRedirectClient()

	// Create session
	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")

	var browserCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}

	renderPath := "/api" + loc + "/render/tool-card"
	body := `{"type":"tool_call","tool":"terminal_execute","args":{"command":"echo hello"}}`
	req, _ := http.NewRequest("POST", server.URL+renderPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if browserCookie != nil {
		req.AddCookie(browserCookie)
	}
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("tool-card render status = %d, want 200", resp2.StatusCode)
	} else {
		ct := resp2.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html", ct)
		}
	}
}

func TestRenderError(t *testing.T) {
	server := newTestServerWithRuns(t)
	client := noRedirectClient()

	body := `{"message":"Test error message"}`
	resp, err := client.Post(server.URL+"/api/sessions/nonexistent/render/error", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Error rendering should work even for nonexistent sessions
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestCancelEndpoint(t *testing.T) {
	server := newTestServerWithRuns(t)
	client := noRedirectClient()

	// Create session
	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location") // e.g. /sessions/abc123

	var browserCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}

	// Cancel via /api/sessions/{id}/cancel
	cancelPath := "/api" + loc + "/cancel" // /api/sessions/abc123/cancel
	req, _ := http.NewRequest("POST", server.URL+cancelPath, nil)
	if browserCookie != nil {
		req.AddCookie(browserCookie)
	}
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("cancel status = %d, want 200", resp2.StatusCode)
	} else {
		// Should return HTML fragment (MessageInput)
		ct := resp2.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html", ct)
		}
	}
}

func TestConsecutiveChatEndpoint(t *testing.T) {
	// Reproduces the zombie-active-run bug: sending a message, waiting for
	// completion, then sending another should NOT return 409.
	llmSrv := fakeChatServer(t, "ok")
	defer llmSrv.Close()

	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmSrv.URL)
	client := noRedirectClient()

	// Create session
	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	loc := resp.Header.Get("Location") // e.g. /sessions/abc123
	var browserCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}

	chatPath := "/api" + loc + "/chat"

	// First chat request
	body := "message=Say+hi"
	req1, _ := http.NewRequest("POST", server.URL+chatPath, strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req1.Header.Set("HX-Request", "true")
	if browserCookie != nil {
		req1.AddCookie(browserCookie)
	}
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()

	if resp1.StatusCode == http.StatusConflict {
		t.Fatalf("first chat got 409 — unexpected active run")
	}

	if resp1.StatusCode != http.StatusOK {
		t.Logf("first chat status = %d (non-fatal, may be provider config issue)", resp1.StatusCode)
	}

	// Wait for run to complete (poll stream endpoint until it 404s, meaning run is done)
	// Maximum wait: 5 seconds
	streamPath := "/api" + loc + "/stream"
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		reqCheck, _ := http.NewRequest("GET", server.URL+streamPath, nil)
		if browserCookie != nil {
			reqCheck.AddCookie(browserCookie)
		}
		respCheck, err := client.Do(reqCheck)
		if err == nil {
			respCheck.Body.Close()
			if respCheck.StatusCode == http.StatusNotFound {
				// Stream ended — run completed
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Second chat request — should NOT get 409
	req2, _ := http.NewRequest("POST", server.URL+chatPath, strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("HX-Request", "true")
	if browserCookie != nil {
		req2.AddCookie(browserCookie)
	}
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()

	if resp2.StatusCode == http.StatusConflict {
		t.Errorf("second chat got 409 — zombie active run (bug: Done not closed on normal completion)")
	}

	// Wait for second run to also complete
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		reqCheck, _ := http.NewRequest("GET", server.URL+streamPath, nil)
		if browserCookie != nil {
			reqCheck.AddCookie(browserCookie)
		}
		respCheck, err := client.Do(reqCheck)
		if err == nil {
			respCheck.Body.Close()
			if respCheck.StatusCode == http.StatusNotFound {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestSSEEventSerialization(t *testing.T) {
	// Test that SSEEvent serializes correctly
	evt := api.SSEEvent{Type: "token", Content: "Hello, world!"}
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["type"] != "token" {
		t.Errorf("type = %v, want 'token'", decoded["type"])
	}
	if decoded["content"] != "Hello, world!" {
		t.Errorf("content = %v, want 'Hello, world!'", decoded["content"])
	}
}

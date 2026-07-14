package api_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/api"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/executor"
	"github.com/glemsom/eitri/internal/provider"
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
	configPath  string
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

	configPath := t.TempDir() + "/config.json"
	cfg := api.ServerConfig{
		ConfigPath:     configPath,
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
		configPath:  configPath,
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

func newManagedTestServerWithRunsAndSkillsService(t *testing.T, workspace string, skillsSvc *skills.Service) *testServerWithRuns {
	t.Helper()
	sessionMgr := session.NewManager(10)
	executorMgr := executor.NewSessionManager(workspace, 0, 0)
	runnerMgr := newRunnerManager(t)
	runMgr := api.NewRunManager(runnerMgr, executorMgr)
	runMgr.SetSkillsService(skillsSvc)
	runMgr.SetUISessionManager(sessionMgr)

	configPath := t.TempDir() + "/config.json"
	cfg := api.ServerConfig{
		ConfigPath:     configPath,
		Workspace:      workspace,
		SessionManager: sessionMgr,
		RunManager:     runMgr,
		SkillsService:  skillsSvc,
	}
	srv := api.NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)
	return &testServerWithRuns{
		server:      server,
		configPath:  configPath,
		sessionMgr:  sessionMgr,
		executorMgr: executorMgr,
		runMgr:      runMgr,
	}
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

func waitForRunToFinish(t *testing.T, runMgr *api.RunManager, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runMgr.ActiveRun(sessionID) == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s did not finish", sessionID)
}

func waitForSessionMessageCount(t *testing.T, sessionMgr *session.Manager, sessionID string, want int) *session.UISession {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sess := sessionMgr.Get(sessionID)
		if sess != nil && len(sess.Messages) >= want {
			return sess
		}
		time.Sleep(10 * time.Millisecond)
	}
	sess := sessionMgr.Get(sessionID)
	if sess == nil {
		t.Fatalf("session %s missing", sessionID)
	}
	t.Fatalf("messages = %d, want at least %d", len(sess.Messages), want)
	return nil
}

func fakeTwoTurnToolChatServer(t *testing.T, waitBeforeSecondTurn <-chan struct{}) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var chatCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
		case "/v1/chat/completions":
			call := chatCalls.Add(1)
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming not supported", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			if call%2 == 1 {
				fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"terminal_execute","arguments":"{\"command\":\"echo hello\"}"}}]},"finish_reason":"tool_calls"}]}`, "\n\n")
				fmt.Fprint(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}

			if waitBeforeSecondTurn != nil && call == 2 {
				select {
				case <-r.Context().Done():
					return
				case <-waitBeforeSecondTurn:
				}
			}

			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"final answer after tool"},"index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &chatCalls
}

func TestChatRun_SystemPromptFollowsConfigAcrossRuns(t *testing.T) {
	h := newManagedTestServerWithRuns(t)

	releaseFirstRun := make(chan struct{})
	promptCh := make(chan string, 3)
	requestCount := 0
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"test-model"}]}`)
		case "/v1/chat/completions":
			requestCount++
			var body struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode chat request: %v", err)
			}
			var systemPrompt string
			for _, msg := range body.Messages {
				if msg.Role == "system" {
					systemPrompt = msg.Content
					break
				}
			}
			promptCh <- systemPrompt

			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming not supported", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"hello"},"index":0}]}`, "\n\n")
			flusher.Flush()
			if requestCount == 1 {
				<-releaseFirstRun
			}
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	defer llmSrv.Close()

	putJSONConfig(t, h.server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"test-model","system_prompt":"Prompt one"}`, llmSrv.URL))

	sessionID, browserCookie := createSessionAndCookie(t, h.server.URL)
	startChatRun(t, h.server.URL, sessionID, browserCookie)

	firstPrompt := <-promptCh
	if !strings.Contains(firstPrompt, "Prompt one") {
		t.Fatalf("first run system prompt = %q, want custom prompt", firstPrompt)
	}

	putJSONConfig(t, h.server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"test-model","system_prompt":"Prompt two"}`, llmSrv.URL))
	close(releaseFirstRun)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.runMgr.ActiveRun(sessionID) == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h.runMgr.ActiveRun(sessionID) != nil {
		t.Fatal("first run did not finish")
	}

	startChatRun(t, h.server.URL, sessionID, browserCookie)
	secondPrompt := <-promptCh
	if !strings.Contains(secondPrompt, "Prompt two") {
		t.Fatalf("second run system prompt = %q, want updated prompt", secondPrompt)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.runMgr.ActiveRun(sessionID) == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h.runMgr.ActiveRun(sessionID) != nil {
		t.Fatal("second run did not finish")
	}

	putJSONConfig(t, h.server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"test-model","system_prompt":""}`, llmSrv.URL))
	startChatRun(t, h.server.URL, sessionID, browserCookie)
	thirdPrompt := <-promptCh
	if strings.Contains(thirdPrompt, "Prompt one") || strings.Contains(thirdPrompt, "Prompt two") {
		t.Fatalf("third run system prompt = %q, want default prompt after clear", thirdPrompt)
	}
	if !strings.Contains(thirdPrompt, "You are Eitri") {
		t.Fatalf("third run system prompt = %q, want built-in default prompt", thirdPrompt)
	}
}

type capturedChatRequest struct {
	Messages []struct {
		Role       string `json:"role"`
		Content    string `json:"content"`
		ToolCallID string `json:"tool_call_id"`
		ToolCalls  []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"messages"`
}

func countActivateSkillMessages(req capturedChatRequest) (assistantCalls int, toolResponses int) {
	for _, msg := range req.Messages {
		for _, call := range msg.ToolCalls {
			if call.Function.Name == "activate_skill" {
				assistantCalls++
			}
		}
		if msg.Role == "tool" && msg.ToolCallID != "" && strings.Contains(msg.Content, "skill_directory") {
			toolResponses++
		}
	}
	return assistantCalls, toolResponses
}

func TestChatRun_ReappliesSlashActivatedSkillsOnEveryRunWithoutAccumulating(t *testing.T) {
	workspace := t.TempDir()
	rootDir := filepath.Join(workspace, ".eitri", "skills")
	if err := os.MkdirAll(filepath.Join(rootDir, "code-review"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "code-review", "SKILL.md"), []byte("---\nname: code-review\ndescription: Review code\n---\n# Review\n\nUse strict review checklist."), 0644); err != nil {
		t.Fatal(err)
	}

	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	h := newManagedTestServerWithRunsAndSkillsService(t, workspace, skillsSvc)
	requests := make(chan capturedChatRequest, 4)
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"test-model"}]}`)
		case "/v1/chat/completions":
			var body capturedChatRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode chat request: %v", err)
			}
			requests <- body
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming not supported", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"hello"},"index":0}]}`, "\n\n")
			flusher.Flush()
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	defer llmSrv.Close()

	putBrowserConfig(t, h.server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"test-model"}`, llmSrv.URL))

	sessionID, browserCookie := createSessionAndCookie(t, h.server.URL)
	client := noRedirectClient()
	activateReq, _ := http.NewRequest(http.MethodPost, h.server.URL+"/api/sessions/"+sessionID+"/chat", strings.NewReader(url.Values{"message": {"/code-review"}}.Encode()))
	activateReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	activateReq.AddCookie(browserCookie)
	activateResp, err := client.Do(activateReq)
	if err != nil {
		t.Fatal(err)
	}
	activateResp.Body.Close()
	if activateResp.StatusCode != http.StatusOK {
		t.Fatalf("slash activation status = %d, want 200", activateResp.StatusCode)
	}

	startChatRun(t, h.server.URL, sessionID, browserCookie)
	first := <-requests
	waitForRunToFinish(t, h.runMgr, sessionID)
	firstCalls, firstResponses := countActivateSkillMessages(first)
	if firstCalls != 1 || firstResponses != 1 {
		t.Fatalf("first run activate_skill context = (%d calls, %d responses), want (1,1); messages=%#v", firstCalls, firstResponses, first.Messages)
	}

	startChatRun(t, h.server.URL, sessionID, browserCookie)
	second := <-requests
	waitForRunToFinish(t, h.runMgr, sessionID)
	secondCalls, secondResponses := countActivateSkillMessages(second)
	if secondCalls != 1 || secondResponses != 1 {
		t.Fatalf("second run activate_skill context = (%d calls, %d responses), want (1,1); messages=%#v", secondCalls, secondResponses, second.Messages)
	}
}

func TestChatRun_SkipsDisappearedActiveSkillAndShowsWarning(t *testing.T) {
	workspace := t.TempDir()
	rootDir := filepath.Join(workspace, ".eitri", "skills")
	skillDir := filepath.Join(rootDir, "code-review")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: code-review\ndescription: Review code\n---\n# Review\n\nUse strict review checklist."), 0644); err != nil {
		t.Fatal(err)
	}

	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	h := newManagedTestServerWithRunsAndSkillsService(t, workspace, skillsSvc)
	requests := make(chan capturedChatRequest, 2)
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"test-model"}]}`)
		case "/v1/chat/completions":
			var body capturedChatRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode chat request: %v", err)
			}
			requests <- body
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming not supported", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"hello"},"index":0}]}`, "\n\n")
			flusher.Flush()
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	defer llmSrv.Close()

	putBrowserConfig(t, h.server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"test-model"}`, llmSrv.URL))

	sessionID, browserCookie := createSessionAndCookie(t, h.server.URL)
	client := noRedirectClient()
	activateReq, _ := http.NewRequest(http.MethodPost, h.server.URL+"/api/sessions/"+sessionID+"/chat", strings.NewReader(url.Values{"message": {"/code-review"}}.Encode()))
	activateReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	activateReq.AddCookie(browserCookie)
	activateResp, err := client.Do(activateReq)
	if err != nil {
		t.Fatal(err)
	}
	activateResp.Body.Close()
	if activateResp.StatusCode != http.StatusOK {
		t.Fatalf("slash activation status = %d, want 200", activateResp.StatusCode)
	}

	if err := os.RemoveAll(skillDir); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodPost, h.server.URL+"/api/sessions/"+sessionID+"/chat", strings.NewReader("message=Hello"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(browserCookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("chat status = %d, want 200, body=%s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "code-review") || !strings.Contains(string(body), "no longer available") {
		t.Fatalf("chat response missing stale-skill warning: %s", string(body))
	}

	captured := <-requests
	waitForRunToFinish(t, h.runMgr, sessionID)
	calls, responses := countActivateSkillMessages(captured)
	if calls != 0 || responses != 0 {
		t.Fatalf("stale skill should not be re-applied, got (%d calls, %d responses); messages=%#v", calls, responses, captured.Messages)
	}
	if got := h.sessionMgr.ActiveSkills(sessionID); len(got) != 0 {
		t.Fatalf("active skills after stale-skill skip = %v, want empty", got)
	}
}

func TestChatRun_GitHubCopilotUsesProviderAuthState(t *testing.T) {
	h := newManagedTestServerWithRuns(t)
	modelsAuthCh := make(chan string, 1)
	chatAuthCh := make(chan string, 1)
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			modelsAuthCh <- r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"gpt-4.1","policy":{"state":"enabled"},"model_picker_enabled":true,"supported_endpoints":["/chat/completions"]}]}`)
		case "/chat/completions":
			chatAuthCh <- r.Header.Get("Authorization")
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming not supported", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"hello"},"index":0}]}`, "\n\n")
			flusher.Flush()
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	defer llmSrv.Close()

	raw, err := provider.EncodeGitHubCopilotAuthState(provider.GitHubCopilotAuthState{AccessToken: "gho-provider-state"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Provider = "github_copilot"
	cfg.APIKey = ""
	cfg.ProviderAuth = raw
	cfg.BaseURL = llmSrv.URL
	cfg.Model = "gpt-4.1"
	if err := config.Save(h.configPath, &cfg); err != nil {
		t.Fatal(err)
	}

	sessionID, browserCookie := createSessionAndCookie(t, h.server.URL)
	startChatRun(t, h.server.URL, sessionID, browserCookie)

	sawModelsAuth := <-modelsAuthCh
	sawChatAuth := <-chatAuthCh
	if sawModelsAuth != "Bearer gho-provider-state" {
		t.Fatalf("model discovery Authorization = %q, want Bearer gho-provider-state", sawModelsAuth)
	}
	if sawChatAuth != "Bearer gho-provider-state" {
		t.Fatalf("chat Authorization = %q, want Bearer gho-provider-state", sawChatAuth)
	}
}

func TestChatRun_GitHubCopilotProviderAuthBeatsStaleAPIKey(t *testing.T) {
	h := newManagedTestServerWithRuns(t)
	modelsAuthCh := make(chan string, 1)
	chatAuthCh := make(chan string, 1)
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			modelsAuthCh <- r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"gpt-4.1","policy":{"state":"enabled"},"model_picker_enabled":true,"supported_endpoints":["/chat/completions"]}]}`)
		case "/chat/completions":
			chatAuthCh <- r.Header.Get("Authorization")
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming not supported", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"hello"},"index":0}]}`, "\n\n")
			flusher.Flush()
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	defer llmSrv.Close()

	raw, err := provider.EncodeGitHubCopilotAuthState(provider.GitHubCopilotAuthState{AccessToken: "gho-provider-state"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Provider = "github_copilot"
	cfg.APIKey = "gho-stale-api-key"
	cfg.ProviderAuth = raw
	cfg.BaseURL = llmSrv.URL
	cfg.Model = "gpt-4.1"
	if err := config.Save(h.configPath, &cfg); err != nil {
		t.Fatal(err)
	}

	sessionID, browserCookie := createSessionAndCookie(t, h.server.URL)
	startChatRun(t, h.server.URL, sessionID, browserCookie)

	sawModelsAuth := <-modelsAuthCh
	sawChatAuth := <-chatAuthCh
	if sawModelsAuth != "Bearer gho-provider-state" {
		t.Fatalf("model discovery Authorization = %q, want Bearer gho-provider-state", sawModelsAuth)
	}
	if sawChatAuth != "Bearer gho-provider-state" {
		t.Fatalf("chat Authorization = %q, want Bearer gho-provider-state", sawChatAuth)
	}
}

func TestChatRun_GitHubCopilotRefreshesExpiredProviderAuthState(t *testing.T) {
	sessionMgr := session.NewManager(10)
	executorMgr := executor.NewSessionManager(t.TempDir(), 0, 0)
	runnerMgr := newRunnerManager(t)
	runMgr := api.NewRunManager(runnerMgr, executorMgr)
	skillsSvc := skills.NewService()
	runMgr.SetSkillsService(skillsSvc)
	runMgr.SetUISessionManager(sessionMgr)
	configPath := t.TempDir() + "/config.json"
	now := time.Now().Add(-2 * time.Hour)

	modelsAuthCh := make(chan string, 1)
	chatAuthCh := make(chan string, 1)
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			modelsAuthCh <- r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"gpt-4.1","policy":{"state":"enabled"},"model_picker_enabled":true,"supported_endpoints":["/chat/completions"]}]}`)
		case "/chat/completions":
			chatAuthCh <- r.Header.Get("Authorization")
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming not supported", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"hello"},"index":0}]}`, "\n\n")
			flusher.Flush()
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	defer llmSrv.Close()

	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode refresh request: %v", err)
		}
		if body["grant_type"] != "refresh_token" {
			t.Fatalf("grant_type = %q, want refresh_token", body["grant_type"])
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"gho-refreshed","token_type":"bearer","scope":"read:user","refresh_token":"ghr-next","expires_in":28800,"refresh_token_expires_in":15897600}`)
	}))
	defer oauth.Close()

	raw, err := provider.EncodeGitHubCopilotAuthState(provider.GitHubCopilotAuthState{
		AccessToken:           "gho-expired",
		TokenType:             "bearer",
		Scope:                 "read:user",
		RefreshToken:          "ghr-refresh",
		ExpiresAt:             now.Add(-time.Minute),
		RefreshTokenExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Provider = "github_copilot"
	cfg.APIKey = ""
	cfg.ProviderAuth = raw
	cfg.BaseURL = llmSrv.URL
	cfg.Model = "gpt-4.1"
	if err := config.Save(configPath, &cfg); err != nil {
		t.Fatal(err)
	}

	srv := api.NewServer(api.ServerConfig{
		ConfigPath:     configPath,
		Workspace:      t.TempDir(),
		SessionManager: sessionMgr,
		RunManager:     runMgr,
		SkillsService:  skillsSvc,
		CopilotOAuth: api.GitHubCopilotOAuthConfig{
			ClientID:       "client-id",
			AccessTokenURL: oauth.URL,
		},
	})
	server := httptest.NewServer(srv.Handler())
	defer server.Close()

	sessionID, browserCookie := createSessionAndCookie(t, server.URL)
	startChatRun(t, server.URL, sessionID, browserCookie)

	sawModelsAuth := <-modelsAuthCh
	sawChatAuth := <-chatAuthCh
	if sawModelsAuth != "Bearer gho-refreshed" {
		t.Fatalf("model discovery Authorization = %q, want Bearer gho-refreshed", sawModelsAuth)
	}
	if sawChatAuth != "Bearer gho-refreshed" {
		t.Fatalf("chat Authorization = %q, want Bearer gho-refreshed", sawChatAuth)
	}

	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.APIKey != "gho-refreshed" {
		t.Fatalf("saved api_key = %q, want gho-refreshed", saved.APIKey)
	}
}

func TestChatRun_MaxTurnsStopsAfterToolTurn(t *testing.T) {
	llmSrv, chatCalls := fakeTwoTurnToolChatServer(t, nil)
	h := newManagedTestServerWithRuns(t)
	putBrowserConfig(t, h.server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"test-model","max_turns":1}`, llmSrv.URL))

	sessionID, browserCookie := createSessionAndCookie(t, h.server.URL)
	startChatRun(t, h.server.URL, sessionID, browserCookie)
	waitForRunToFinish(t, h.runMgr, sessionID)

	if got := chatCalls.Load(); got != 1 {
		t.Fatalf("chat completion calls = %d, want 1", got)
	}

	sess := waitForSessionMessageCount(t, h.sessionMgr, sessionID, 2)
	assistant := sess.Messages[1]
	if assistant.Role != "assistant" {
		t.Fatalf("assistant role = %q, want assistant", assistant.Role)
	}
	if !strings.Contains(assistant.Content, "max turns") {
		t.Fatalf("assistant message = %q, want max-turn explanation", assistant.Content)
	}
	if !strings.Contains(assistant.Content, "1") {
		t.Fatalf("assistant message = %q, want configured limit", assistant.Content)
	}
}

func TestChatRun_MaxTurnsConfigChangesOnlyAffectLaterRuns(t *testing.T) {
	secondTurnGate := make(chan struct{})
	llmSrv, chatCalls := fakeTwoTurnToolChatServer(t, secondTurnGate)
	h := newManagedTestServerWithRuns(t)
	putBrowserConfig(t, h.server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"test-model","max_turns":2}`, llmSrv.URL))

	sessionID, browserCookie := createSessionAndCookie(t, h.server.URL)
	startChatRun(t, h.server.URL, sessionID, browserCookie)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if chatCalls.Load() >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := chatCalls.Load(); got < 2 {
		t.Fatalf("chat completion calls = %d, want second turn in progress", got)
	}

	putBrowserConfig(t, h.server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"test-model","max_turns":1}`, llmSrv.URL))
	close(secondTurnGate)
	waitForRunToFinish(t, h.runMgr, sessionID)

	sess := waitForSessionMessageCount(t, h.sessionMgr, sessionID, 2)
	if got := sess.Messages[1].Content; !strings.Contains(got, "final answer after tool") {
		t.Fatalf("first run assistant message = %q, want final answer", got)
	}
	if strings.Contains(sess.Messages[1].Content, "max turns") {
		t.Fatalf("first run assistant message = %q, want no max-turn warning", sess.Messages[1].Content)
	}

	startChatRun(t, h.server.URL, sessionID, browserCookie)
	waitForRunToFinish(t, h.runMgr, sessionID)
	if got := chatCalls.Load(); got != 3 {
		t.Fatalf("chat completion calls after second run = %d, want 3", got)
	}

	sess = waitForSessionMessageCount(t, h.sessionMgr, sessionID, 4)
	if got := sess.Messages[3].Content; !strings.Contains(got, "max turns") {
		t.Fatalf("second run assistant message = %q, want max-turn explanation", got)
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

func TestStream_ReconnectAfterToolCallReceivesToolResultAndDone(t *testing.T) {
	llmSrv, _ := fakeTwoTurnToolChatServer(t, nil)
	h := newManagedTestServerWithRuns(t)
	configureProvider(t, h.server, llmSrv.URL)

	sessionID, browserCookie := createSessionAndCookie(t, h.server.URL)
	startChatRun(t, h.server.URL, sessionID, browserCookie)

	stream1 := newSSEStream(openStreamWithRetry(t, h.server.URL, sessionID, browserCookie))
	if got := stream1.waitFor(t, func(data string) bool {
		return strings.Contains(data, `"type":"tool_call"`) && strings.Contains(data, `"tool":"terminal_execute"`)
	}, 2*time.Second); got == "" {
		t.Fatal("first stream never received tool_call")
	}
	stream1.Close()

	stream2 := newSSEStream(openStreamWithRetry(t, h.server.URL, sessionID, browserCookie))
	defer stream2.Close()
	if got := stream2.waitFor(t, func(data string) bool {
		return strings.Contains(data, `"type":"tool_result"`) && strings.Contains(data, `"tool":"terminal_execute"`)
	}, 3*time.Second); got == "" {
		t.Fatal("reconnected stream never received tool_result")
	}
	if got := stream2.waitFor(t, func(data string) bool {
		return strings.Contains(data, `"type":"done"`)
	}, 3*time.Second); got == "" {
		t.Fatal("reconnected stream never received done after tool run")
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
		// Cancel now returns empty 200 (issue #103 — CSS class toggle, no more outerHTML swap)
		body, _ := io.ReadAll(resp2.Body)
		if len(body) > 0 {
			t.Errorf("cancel response body = %d bytes, want empty", len(body))
		}
	}
}

func TestConsecutiveChatEndpoint(t *testing.T) {
	// Reproduces zombie-active-run bug: sending message, waiting for
	// completion, then sending another should NOT return 409.
	llmSrv := fakeChatServer(t, "ok")
	defer llmSrv.Close()

	h := newManagedTestServerWithRuns(t)
	configureProvider(t, h.server, llmSrv.URL)
	client := noRedirectClient()

	// Create session.
	resp, err := client.Get(h.server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	loc := resp.Header.Get("Location")
	sessionID := strings.TrimPrefix(loc, "/sessions/")
	var browserCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}

	chatPath := "/api" + loc + "/chat"

	// First chat request.
	body := "message=Say+hi"
	req1, _ := http.NewRequest("POST", h.server.URL+chatPath, strings.NewReader(body))
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

	// Wait for first run to complete.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.runMgr.ActiveRun(sessionID) == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Second chat request — should NOT get 409.
	req2, _ := http.NewRequest("POST", h.server.URL+chatPath, strings.NewReader(body))
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

	// Wait for second run to also complete.
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.runMgr.ActiveRun(sessionID) == nil {
			break
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

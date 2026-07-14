package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/api"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/executor"
	agentrunner "github.com/glemsom/eitri/internal/runner"
)

func TestBuild(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "eitri")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	// Verify binary exists and is executable
	if _, err := exec.LookPath(binary); err != nil {
		t.Fatalf("binary not found after build: %v", err)
	}
}

func fakeSlowProvider(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	t.Cleanup(server.Close)
	return server
}

func TestCleanupRuntimeCancelsRunsAndClosesExecutors(t *testing.T) {
	provider := fakeSlowProvider(t, 2*time.Second)
	executorMgr := executor.NewSessionManager(t.TempDir(), time.Second, time.Minute)
	runMgr := api.NewRunManager(agentrunner.NewManager(), executorMgr)
	runMgr.UpdateProviderConfig(&config.Config{
		Provider: "custom_openai",
		BaseURL:  provider.URL,
		APIKey:   "sk-test",
		Model:    "test-model",
	})

	oldExecutor, err := executorMgr.GetOrCreate("session-1")
	if err != nil {
		t.Fatalf("GetOrCreate = %v", err)
	}
	if err := runMgr.StartRun(context.Background(), "session-1", "hello"); err != nil {
		t.Fatalf("StartRun = %v", err)
	}
	if runMgr.ActiveRun("session-1") == nil {
		t.Fatal("run did not become active")
	}

	cleanupRuntime(nil, runMgr, executorMgr)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runMgr.ActiveRun("session-1") == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if runMgr.ActiveRun("session-1") != nil {
		t.Fatal("run still active after cleanup")
	}

	newExecutor, err := executorMgr.GetOrCreate("session-1")
	if err != nil {
		t.Fatalf("GetOrCreate after cleanup = %v", err)
	}
	if newExecutor == oldExecutor {
		t.Fatal("executor was not closed during cleanup")
	}
}

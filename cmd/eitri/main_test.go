package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if _, err := runMgr.StartRun(context.Background(), "session-1", "hello"); err != nil {
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

func TestServeBindFailureReturnsActionableHint(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = serve(ctx, serveOptions{
		Addr:      listener.Addr().String(),
		Workspace: t.TempDir(),
		Handler:   http.NewServeMux(),
		Stdout:    &stdout,
		Stderr:    &stderr,
		Getenv:    os.Getenv,
		OpenURL: func(string) error {
			t.Fatal("OpenURL should not run on bind failure")
			return nil
		},
	})
	if err == nil {
		t.Fatal("serve error = nil, want bind failure")
	}
	if !strings.Contains(err.Error(), "EITRI_ADDR=127.0.0.1:8081 eitri") {
		t.Fatalf("error = %q, want bind hint", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty before successful bind", stdout.String())
	}
}

func TestServeWarnsOnNonLoopbackBind(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, serveOptions{
			Addr:      "0.0.0.0:0",
			Workspace: t.TempDir(),
			Handler:   http.NewServeMux(),
			Stdout:    &stdout,
			Stderr:    &stderr,
			Getenv:    func(string) string { return "0" },
			OpenURL:   func(string) error { return nil },
		})
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(stderr.String(), "no authentication") {
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("serve returned error: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatalf("stderr = %q, want non-loopback warning", stderr.String())
}

func TestServeOpensBrowserWhenForced(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	opened := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, serveOptions{
			Addr:      "127.0.0.1:0",
			Workspace: t.TempDir(),
			Handler:   http.NewServeMux(),
			Stdout:    &stdout,
			Stderr:    &stderr,
			Getenv: func(key string) string {
				if key == "EITRI_OPEN_BROWSER" {
					return "1"
				}
				return ""
			},
			OpenURL: func(url string) error {
				opened <- url
				cancel()
				return nil
			},
		})
	}()

	select {
	case url := <-opened:
		if !strings.HasPrefix(url, "http://127.0.0.1:") {
			t.Fatalf("opened url = %q, want loopback http url", url)
		}
	case <-time.After(2 * time.Second):
		cancel()
		<-done
		t.Fatal("OpenURL not called")
	}

	if err := <-done; err != nil {
		t.Fatalf("serve returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Workspace: ") {
		t.Fatalf("stdout = %q, want workspace line", stdout.String())
	}
}

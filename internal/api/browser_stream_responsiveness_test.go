package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// fakeLargeReasoningChatServer streams a realistic large reasoning_content
// payload (hundreds of KB across many deltas) followed by a short final
// answer. Mirrors deepseek-v4-flash's high-volume reasoning output that
// triggers the user-reported "page unresponsive / can't click gear" freeze.
func fakeLargeReasoningChatServer(t *testing.T, nDeltas, reachBeforeFlush int, paceBatchSleep time.Duration) *httptest.Server {
	t.Helper()
	// Build deterministic prose-only reasoning chunks (~120 chars each) so the
	// push is fast and unbounded by blank lines (a single growing block).
	base := "placerat vestibulum $`code`$ vitae condimentum nonummy auctor posuere nullam sem mattis cursus ligula gravida aliquam feugiat interdum velit habitasse **molestie** mi at "
	chunk := func(i int) string { return fmt.Sprintf("tok%d %s ", i, base) }

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
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":""},"finish_reason":null}]}`+"\n\n", now)
			for i := 0; i < nDeltas; i++ {
				fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"reasoning_content":%q},"finish_reason":null}]}`+"\n\n", now, chunk(i))
				if reachBeforeFlush > 0 && i%reachBeforeFlush == reachBeforeFlush-1 {
					flusher.Flush()
					// Pace the stream like a real provider so the DOM grows
					// progressively over seconds rather than in one burst.
					if paceBatchSleep > 0 {
						time.Sleep(paceBatchSleep)
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

// TestBrowser_LargeReasoningStream_MainThreadResponsive streams a large
// reasoning payload and asserts the main thread stays responsive while the
// live thinking panel is updating. This is the red-capable signal for the
// user-reported "UI unresponsive while streaming / can't click the gear icon /
// Chrome kill page" bug, and a regression guard against O(n²) layout caused
// by holding the whole reasoning transcript in the DOM as a live text node.
func TestBrowser_LargeReasoningStream_MainThreadResponsive(t *testing.T) {
	// Stream ~600 KB of reasoning over ~16s (paced), stressing the growing
	// DOM progressively -- before the bounded-transcript fix this froze the
	// main thread into 200ms+ stalls each frame.
	const nDeltas = 4000
	const flushEvery = 5
	const paceBatchSleep = 20 * time.Millisecond
	const stallThresholdMs = 100.0 // inter-frame gap beyond this = main-thread stall

	server := newTestServerWithRuns(t)
	configureProvider(t, server, fakeLargeReasoningChatServer(t, nDeltas, flushEvery, paceBatchSleep).URL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	// Start a high-frequency timestamp probe. When the main thread stalls
	// (an O(n²) re-layout during streaming) this timer is delayed and the gap
	// between successive ticks balloons -- the "can't click the gear icon /
	// Chrome wants to kill the page" symptom.
	var tickProbeRegistered bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`
			(() => {
				window.__ticks = [];
				if (typeof window.__tickId === 'number') { clearInterval(window.__tickId); }
				window.__tickId = setInterval(() => { window.__ticks.push(performance.now()); }, 5);
				return true;
			})()
		`, &tickProbeRegistered),
	)
	if err != nil {
		t.Fatalf("navigate/probe failed: %v", err)
	}

	// Reset the baseline right before sending so the window covers the run.
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(window.__ticks = [], true)`, new(bool)),
		chromedp.SendKeys("#chat-input", "reason hard", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	// Wait for the run to finish by polling the debug API Go-side. This is
	// deliberately NOT driven through the page's DOM: if the streaming freezes
	// the page main thread, in-page evals (and the browser UI) would block
	// too, masking the freeze. The debug API reports the agent-loop status
	// independently of the renderer.
	sessionResp := pollSessionStatuses(t, server.URL, func(st map[string]string) bool {
		for _, s := range st {
			if s == "running" {
				return true
			}
		}
		return false
	}, 60*time.Second)
	if sessionResp == 0 {
		t.Fatal("no session entered running state")
	}
	_ = pollSessionStatuses(t, server.URL, func(st map[string]string) bool {
		for _, s := range st {
			if s == "running" {
				return false
			}
		}
		return true
	}, 120*time.Second)

	// Measure max inter-tick gap during the run.
	var ticks []float64
	if err := chromedp.Run(ctx, chromedp.EvaluateAsDevTools(`window.__ticks`, &ticks)); err != nil {
		t.Fatalf("read ticks: %v", err)
	}

	maxGap := 0.0
	var stalls []float64
	for i := 1; i < len(ticks); i++ {
		if g := ticks[i] - ticks[i-1]; g > maxGap {
			maxGap = g
		}
		if g := ticks[i] - ticks[i-1]; g > stallThresholdMs {
			stalls = append(stalls, g)
		}
	}
	t.Logf("tick probe captured %d samples over run (max inter-tick gap=%.1fms)", len(ticks), maxGap)
	if len(stalls) > 0 {
		t.Errorf("main thread stalled during reasoning streaming: %d gaps >%gms (worst %.1fms) → page unresponsive, gear/nav unclickable.",
			len(stalls), stallThresholdMs, maxGap)
	} else {
		t.Logf("main thread stayed responsive during streaming (max gap %.1fms)", maxGap)
	}
}

// pollSessionStatuses polls GET /api/debug/sessions until predicate(pred)["id"]->status
// returns true or the deadline passes. Returns the number of sessions seen, or 0
// if the predicate never held.
func pollSessionStatuses(t *testing.T, baseURL string, pred func(map[string]string) bool, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var body []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		resp, err := http.Get(baseURL + "/api/debug/sessions")
		if err == nil {
			_ = json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
		}
		st := make(map[string]string)
		for _, s := range body {
			st[s.ID] = s.Status
		}
		if pred(st) {
			return len(st)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return 0
}
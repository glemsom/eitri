//go:build e2e

package api_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestBrowser_NoExternalFontRequests verifies the UI makes zero external font
// network requests: every font file is served from the embedded /static/fonts/*
// assets, and Google Fonts / any CDN is never contacted. It also checks that
// the critical latin subsets (Inter + JetBrains Mono) are actually requested
// and that the Inter face becomes available to the page. (issue #970)
func TestBrowser_NoExternalFontRequests(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var (
		mu       sync.Mutex
		fontURLs []string
	)
	chromedp.ListenTarget(ctx, func(ev any) {
		req, ok := ev.(*network.EventRequestWillBeSent)
		if !ok {
			return
		}
		url := req.Request.URL
		// Track font requests only (the font stylesheet or any font file).
		if !strings.Contains(url, "fonts.googleapis.com") &&
			!strings.Contains(url, "fonts.gstatic.com") &&
			!strings.Contains(url, "/static/fonts/") {
			return
		}
		mu.Lock()
		fontURLs = append(fontURLs, url)
		mu.Unlock()
	})

	err := chromedp.Run(ctx,
		network.Enable(),
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
	}
	waitForComposerReady(t, ctx)

	// Wait until both critical latin subsets have been requested from
	// /static/fonts/* and no external font CDN request has been seen.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		interSeen, monoSeen, external := fontRequestSummary(&mu, &fontURLs)
		if interSeen && monoSeen && !external {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// At least one Inter font face must actually be loaded by the page.
	var interLoaded bool
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_ = chromedp.Run(ctx, chromedp.EvaluateAsDevTools(`(function() {
			return [...document.fonts].some(function(f) {
				return f.family.indexOf('Inter') >= 0 && f.status === 'loaded';
			});
		})()`, &interLoaded))
		if interLoaded {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	interSeen, monoSeen, external := fontRequestSummary(&mu, &fontURLs)
	if len(fontURLs) == 0 {
		t.Fatal("no font requests observed — fonts were not loaded from /static/fonts/*")
	}
	if external {
		for _, url := range fontURLs {
			if strings.Contains(url, "fonts.googleapis.com") || strings.Contains(url, "fonts.gstatic.com") {
				t.Errorf("external font/CDN request observed: %s", url)
			}
		}
	}
	for _, url := range fontURLs {
		if !strings.HasPrefix(url, server.URL+"/static/fonts/") {
			t.Errorf("font request not served from embedded /static/fonts/: %s", url)
		}
	}
	if !interSeen {
		t.Error("Inter latin subset was never requested from /static/fonts/*")
	}
	if !monoSeen {
		t.Error("JetBrains Mono latin subset was never requested from /static/fonts/*")
	}
	if !interLoaded {
		t.Error("Inter font face never reached 'loaded' status in the page")
	}
}

// fontRequestSummary inspects the collected font request URLs and reports
// whether the Inter/JetBrains Mono latin subsets were requested and whether
// any external font CDN request was observed.
func fontRequestSummary(mu *sync.Mutex, fontURLs *[]string) (interSeen, monoSeen, external bool) {
	mu.Lock()
	defer mu.Unlock()
	for _, url := range *fontURLs {
		if strings.Contains(url, "fonts.googleapis.com") || strings.Contains(url, "fonts.gstatic.com") {
			external = true
		}
		if strings.Contains(url, "/static/fonts/Inter-latin.woff2") {
			interSeen = true
		}
		if strings.Contains(url, "/static/fonts/JetBrainsMono-latin.woff2") {
			monoSeen = true
		}
	}
	return interSeen, monoSeen, external
}

//go:build e2e

package api_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestBrowser_NoExternalFontRequests verifies the UI makes zero external font
// network requests: every font file is served from the embedded /static/fonts/*
// assets, and Google Fonts / any CDN is never contacted. It also checks that
// the critical latin subsets (Geist + JetBrains Mono) are actually requested
// and that the Geist face becomes available to the page. (issue #970)
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
		geistSeen, monoSeen, external := fontRequestSummary(&mu, &fontURLs)
		if geistSeen && monoSeen && !external {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// At least one Geist font face must actually be loaded by the page.
	var geistLoaded bool
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_ = chromedp.Run(ctx, chromedp.EvaluateAsDevTools(`(function() {
			return [...document.fonts].some(function(f) {
				return f.family.indexOf('Geist') >= 0 && f.status === 'loaded';
			});
		})()`, &geistLoaded))
		if geistLoaded {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	geistSeen, monoSeen, external := fontRequestSummary(&mu, &fontURLs)
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
	if !geistSeen {
		t.Error("Geist latin subset was never requested from /static/fonts/*")
	}
	if !monoSeen {
		t.Error("JetBrains Mono latin subset was never requested from /static/fonts/*")
	}
	if !geistLoaded {
		t.Error("Geist font face never reached 'loaded' status in the page")
	}
}

// TestBrowser_GeistApplied verifies the redesigned type hierarchy (issue
// #1177): the UI body font-family resolves to Geist in both light and dark
// themes (not Inter), the report/header display text uses negative
// letter-spacing, and the thinking panel is styled as a tinted, readable code
// panel.
func TestBrowser_GeistApplied(t *testing.T) {
	for _, scheme := range []string{"light", "dark"} {
		scheme := scheme
		t.Run(scheme, func(t *testing.T) {
			server := newTestServer(t)
			ctx, cancel := newBrowserCtx(t, server.URL)
			defer cancel()

			if err := chromedp.Run(ctx, emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{
				{Name: "prefers-color-scheme", Value: scheme},
			})); err != nil {
				t.Fatalf("set emulated media: %v", err)
			}
			if err := chromedp.Run(ctx, chromedp.Navigate(server.URL+"/")); err != nil {
				t.Fatalf("navigate: %v", err)
			}

			var uiFont, thinkingBg, headerLetterSpacing string
			pollForCondition(t, 5*time.Second, 50*time.Millisecond, func() bool {
				if err := chromedp.Run(ctx,
					chromedp.EvaluateAsDevTools(`getComputedStyle(document.body).fontFamily`, &uiFont),
					chromedp.EvaluateAsDevTools(`getComputedStyle(document.querySelector('.thinking-content')).backgroundColor`, &thinkingBg),
					chromedp.EvaluateAsDevTools(`getComputedStyle(document.querySelector('header h1')).letterSpacing`, &headerLetterSpacing),
				); err != nil {
					return false
				}
				return strings.Contains(strings.ToLower(uiFont), "geist")
			})

			if !strings.Contains(strings.ToLower(uiFont), "geist") {
				t.Errorf("body font-family = %q, want Geist (scheme %s)", uiFont, scheme)
			}
			if strings.Contains(strings.ToLower(uiFont), "inter") {
				t.Errorf("body font-family = %q, must not be Inter (scheme %s)", uiFont, scheme)
			}
			if thinkingBg == "" || thinkingBg == "rgba(0, 0, 0, 0)" {
				t.Errorf(".thinking-content background = %q, want a tinted panel background", thinkingBg)
			}
			if headerLetterSpacing == "normal" || headerLetterSpacing == "" {
				t.Errorf("header h1 letter-spacing = %q, want -0.02em", headerLetterSpacing)
			}
		})
	}
}

// fontRequestSummary inspects the collected font request URLs and reports
// whether the Geist/JetBrains Mono latin subsets were requested and whether
// any external font CDN request was observed.
func fontRequestSummary(mu *sync.Mutex, fontURLs *[]string) (geistSeen, monoSeen, external bool) {
	mu.Lock()
	defer mu.Unlock()
	for _, url := range *fontURLs {
		if strings.Contains(url, "fonts.googleapis.com") || strings.Contains(url, "fonts.gstatic.com") {
			external = true
		}
		if strings.Contains(url, "/static/fonts/Geist-latin.woff2") {
			geistSeen = true
		}
		if strings.Contains(url, "/static/fonts/JetBrainsMono-latin.woff2") {
			monoSeen = true
		}
	}
	return geistSeen, monoSeen, external
}

package api_test

import (
	"encoding/json"
	"testing"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

// TestBrowser_ReducedMotionAndTransparencyPrefs verifies eitri.css honours
// prefers-reduced-motion: reduce and prefers-reduced-transparency: reduce
// (issue #1075): decorative/infinite animations collapse to a static render,
// backdrop-filter glass surfaces become solid fills, and the chat page still
// loads and works with both preferences enabled.
func TestBrowser_ReducedMotionAndTransparencyPrefs(t *testing.T) {
	server := newTestServerWithRuns(t)
	configureProvider(t, server, testLLMURL(t))

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
	}
	waitForComposerReady(t, ctx)

	// readPrefStyles returns the reduced-preference-relevant computed styles of
	// the gear dropdown (.dropdown-content — a backdrop-filter glass panel) and
	// the header typing dots (.typing-dots span — an infinite dot-bounce
	// animation), plus the matchMedia results for both preference queries.
	readPrefStyles := func() string {
		var raw string
		if err := chromedp.Run(ctx, chromedp.EvaluateAsDevTools(`(function() {
			var dd = document.querySelector('.dropdown-content');
			var dot = document.querySelector('.typing-dots span');
			var ddStyle = getComputedStyle(dd);
			var dotStyle = getComputedStyle(dot);
			var bg = ddStyle.backgroundColor;
			// rgba(r,g,b,a) → alpha; rgb(r,g,b) has no alpha channel (opaque).
			var m = bg.match(/rgba\(([\d.]+),\s*([\d.]+),\s*([\d.]+),\s*([\d.]+)\)/);
			return JSON.stringify({
				motion: matchMedia('(prefers-reduced-motion: reduce)').matches,
				transparency: matchMedia('(prefers-reduced-transparency: reduce)').matches,
				blur: ddStyle.backdropFilter || ddStyle.webkitBackdropFilter || '',
				anim: dotStyle.animationName,
				backgroundAlpha: m ? parseFloat(m[4]) : 1
			});
		})()`, &raw)); err != nil {
			t.Fatalf("read pref styles: %v", err)
		}
		return raw
	}

	var base struct {
		Motion          bool
		Transparency    bool
		Blur            string
		Anim            string
		BackgroundAlpha float64
	}
	if err := json.Unmarshal([]byte(readPrefStyles()), &base); err != nil {
		t.Fatalf("parse baseline styles: %v", err)
	}
	// Default headless Chrome: no reduced preferences, glass blur active,
	// dot-bounce animation running.
	if base.Motion || base.Transparency {
		t.Fatalf("unexpected baseline prefs: motion=%v transparency=%v", base.Motion, base.Transparency)
	}
	if base.Blur == "" || base.Blur == "none" {
		t.Fatalf("expected glass blur on .dropdown-content by default, got %q", base.Blur)
	}
	if base.Anim != "dot-bounce" {
		t.Fatalf("expected dot-bounce animation by default, got %q", base.Anim)
	}

	// Enable both reduced preferences via CDP media emulation.
	err = chromedp.Run(ctx,
		emulation.SetEmulatedMedia().
			WithFeatures([]*emulation.MediaFeature{
				{Name: "prefers-reduced-motion", Value: "reduce"},
				{Name: "prefers-reduced-transparency", Value: "reduce"},
			}),
	)
	if err != nil {
		t.Fatalf("emulate reduced prefs: %v", err)
	}

	var reduced struct {
		Motion          bool
		Transparency    bool
		Blur            string
		Anim            string
		BackgroundAlpha float64
	}
	if err := json.Unmarshal([]byte(readPrefStyles()), &reduced); err != nil {
		t.Fatalf("parse reduced-pref styles: %v", err)
	}
	if !reduced.Motion || !reduced.Transparency {
		t.Fatalf("media emulation not applied: motion=%v transparency=%v", reduced.Motion, reduced.Transparency)
	}
	// Reduced motion: infinite dot-bounce animation collapses.
	if reduced.Anim != "none" {
		t.Errorf("dot-bounce must collapse to animation: none under reduced motion, got %q", reduced.Anim)
	}
	// Reduced transparency: glass panel drops the blur and becomes opaque.
	if reduced.Blur != "none" {
		t.Errorf(".dropdown-content must drop backdrop blur under reduced transparency, got %q", reduced.Blur)
	}
	if reduced.BackgroundAlpha != 1 {
		t.Errorf(".dropdown-content must get a solid fill under reduced transparency, alpha=%v", reduced.BackgroundAlpha)
	}

	// No functional breakage: the composer still works with both preferences on.
	waitForComposerReady(t, ctx)
	if err := chromedp.Run(ctx,
		chromedp.SendKeys("#chat-input", "hello", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
		chromedp.WaitVisible(".message-user", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("composer broken under reduced preferences: %v", err)
	}
}

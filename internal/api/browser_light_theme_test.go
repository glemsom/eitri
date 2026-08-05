package api_test

import (
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

// TestLightTheme verifies that prefers-color-scheme: light activates the light
// theme — CSS custom properties are overridden to light palette values (issue #977).
func TestLightTheme(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := newBrowserCtx(t, srv.URL)
	defer cancel()

	// Emulate light color scheme BEFORE navigating so the stylesheet applies
	// the correct theme on first paint (no flash-of-wrong-theme).
	if err := chromedp.Run(ctx, emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{
		{Name: "prefers-color-scheme", Value: "light"},
	})); err != nil {
		t.Fatalf("set emulated media: %v", err)
	}

	// Navigate to the page
	if err := chromedp.Run(ctx, chromedp.Navigate(srv.URL+"/")); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// Wait for styles to apply
	time.Sleep(200 * time.Millisecond)

	// Verify light theme CSS variables are applied
	var bgColor, textColor, surfaceColor, borderColor string
	err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.getComputedStyle(document.documentElement).getPropertyValue('--bg').trim()`, &bgColor),
		chromedp.Evaluate(`window.getComputedStyle(document.documentElement).getPropertyValue('--text').trim()`, &textColor),
		chromedp.Evaluate(`window.getComputedStyle(document.documentElement).getPropertyValue('--surface').trim()`, &surfaceColor),
		chromedp.Evaluate(`window.getComputedStyle(document.documentElement).getPropertyValue('--border').trim()`, &borderColor),
	)
	if err != nil {
		t.Fatalf("get computed styles: %v", err)
	}

	// Light theme values from eitri.css
	if bgColor != "#f5f5f7" {
		t.Errorf("--bg = %q, want %q", bgColor, "#f5f5f7")
	}
	if textColor != "#1d1d1f" {
		t.Errorf("--text = %q, want %q", textColor, "#1d1d1f")
	}
	if surfaceColor != "#ffffff" {
		t.Errorf("--surface = %q, want %q", surfaceColor, "#ffffff")
	}
	if borderColor != "#d2d2d7" {
		t.Errorf("--border = %q, want %q", borderColor, "#d2d2d7")
	}
}

// TestDarkThemeDefault verifies that the dark theme is applied when
// prefers-color-scheme: dark is active (issue #977).
// The CSS defines dark values in :root (base) and light values in
// @media (prefers-color-scheme: light), so dark is the default.
func TestDarkThemeDefault(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := newBrowserCtx(t, srv.URL)
	defer cancel()

	// Emulate dark color scheme explicitly since CI Chrome headless may
	// default to light. This verifies dark theme values are correct.
	if err := chromedp.Run(ctx, emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{
		{Name: "prefers-color-scheme", Value: "dark"},
	})); err != nil {
		t.Fatalf("set emulated media: %v", err)
	}

	// Navigate to the page
	if err := chromedp.Run(ctx, chromedp.Navigate(srv.URL+"/")); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// Wait for styles to apply
	time.Sleep(200 * time.Millisecond)

	// Verify dark theme CSS variables are applied
	var bgColor, textColor string
	err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.getComputedStyle(document.documentElement).getPropertyValue('--bg').trim()`, &bgColor),
		chromedp.Evaluate(`window.getComputedStyle(document.documentElement).getPropertyValue('--text').trim()`, &textColor),
	)
	if err != nil {
		t.Fatalf("get computed styles: %v", err)
	}

	// Dark theme values from eitri.css
	if bgColor != "#1a1a2e" {
		t.Errorf("--bg = %q, want %q", bgColor, "#1a1a2e")
	}
	if textColor != "#e0e0e0" {
		t.Errorf("--text = %q, want %q", textColor, "#e0e0e0")
	}
}

// TestLightThemeColorSchemeMeta verifies that the page sets the color-scheme
// meta tag so native controls (scrollbars, form inputs) follow the OS theme
// (issue #977).
func TestLightThemeColorSchemeMeta(t *testing.T) {
	srv := newTestServer(t)
	ctx, cancel := newBrowserCtx(t, srv.URL)
	defer cancel()

	if err := chromedp.Run(ctx, chromedp.Navigate(srv.URL+"/")); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// Verify the <meta name="color-scheme"> tag is present with correct value
	var colorScheme string
	err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelector('meta[name="color-scheme"]')?.getAttribute('content') || ''`, &colorScheme),
	)
	if err != nil {
		t.Fatalf("get color-scheme meta: %v", err)
	}

	if colorScheme != "light dark" {
		t.Errorf("color-scheme meta = %q, want %q", colorScheme, "light dark")
	}

	// Verify light theme-color meta tag is present
	var lightThemeColor string
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelector('meta[name="theme-color"][media="(prefers-color-scheme: light)"]')?.getAttribute('content') || ''`, &lightThemeColor),
	)
	if err != nil {
		t.Fatalf("get light theme-color meta: %v", err)
	}

	if lightThemeColor != "#f5f5f7" {
		t.Errorf("light theme-color = %q, want %q", lightThemeColor, "#f5f5f7")
	}
}

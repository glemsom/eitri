package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// ————— Settings page tests ————— —

func TestBrowser_SettingsPage(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var providerVal string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("body", chromedp.ByQuery),
		chromedp.Value("#provider", &providerVal, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("settings page test failed: %v", err)
	}

	if providerVal == "" {
		t.Log("settings page loaded, provider value (may be empty on first load):", providerVal)
	}
}

func TestBrowser_SettingsFormElements(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var providerExists bool
	var apiKeyExists, baseURLExists, modelExists, systemPromptExists bool
	var sendBtnAbsent bool
	var providerOptionsCount int

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#provider", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools("document.querySelector('#provider') !== null", &providerExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#api_key') !== null", &apiKeyExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#base_url') !== null", &baseURLExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#model') !== null", &modelExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#system_prompt') !== null", &systemPromptExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#send-btn') === null", &sendBtnAbsent),
		chromedp.EvaluateAsDevTools("document.querySelector('#provider').options.length", &providerOptionsCount),
	)
	if err != nil {
		t.Fatalf("settings form test failed: %v", err)
	}

	if !providerExists {
		t.Error("#provider select not found")
	}
	if providerOptionsCount < 2 {
		t.Errorf("provider select has %d options, want at least 2", providerOptionsCount)
	}
	if !apiKeyExists {
		t.Error("#api_key input not found")
	}
	if !baseURLExists {
		t.Error("#base_url input not found")
	}
	if !modelExists {
		t.Error("#model select not found")
	}
	if !systemPromptExists {
		t.Error("#system_prompt textarea not found")
	}
	if !sendBtnAbsent {
		t.Error("#send-btn should be absent on settings page")
	}
}

func TestBrowser_SettingsDirectNavigationPopulatesModels(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`)
	server := newTestServer(t)
	putBrowserConfig(t, server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"gpt-4"}`, fakeProvider.URL))

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var hasGPT4 bool
	var hasGPT35 bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitReady("#model option[value='gpt-4']", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(
			`Array.from(document.querySelector('#model').options).map(o => o.value).includes("gpt-4")`,
			&hasGPT4,
		),
		chromedp.EvaluateAsDevTools(
			`Array.from(document.querySelector('#model').options).map(o => o.value).includes("gpt-3.5-turbo")`,
			&hasGPT35,
		),
	)
	if err != nil {
		t.Fatalf("settings direct navigation failed: %v", err)
	}
	if !hasGPT4 {
		t.Error("settings page missing gpt-4 on direct navigation")
	}
	if !hasGPT35 {
		t.Error("settings page missing gpt-3.5-turbo on direct navigation")
	}
}

// TestBrowser_InitialConfigSavePopulatesModels verifies first save without a
// selected model discovers models and keeps the form editable for second save.
func TestBrowser_InitialConfigSavePopulatesModels(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`)
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		chromedp.SetValue("#provider", "custom_openai", chromedp.ByQuery),
		chromedp.Clear("#base_url", chromedp.ByQuery),
		chromedp.SendKeys("#base_url", fakeProvider.URL, chromedp.ByQuery),
		chromedp.Clear("#api_key", chromedp.ByQuery),
		chromedp.SendKeys("#api_key", "sk-test", chromedp.ByQuery),
		chromedp.Click("button[type=submit]", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("form submit failed: %v", err)
	}

	var modelOptionCount int
	var hasGPT4 bool
	var hasGPT35 bool
	var selectedModel string
	err = chromedp.Run(ctx,
		chromedp.WaitReady("#model option[value='gpt-4']", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools("document.querySelector('#model').options.length", &modelOptionCount),
		chromedp.EvaluateAsDevTools(
			`Array.from(document.querySelector('#model').options).map(o => o.value).includes("gpt-4")`,
			&hasGPT4,
		),
		chromedp.EvaluateAsDevTools(
			`Array.from(document.querySelector('#model').options).map(o => o.value).includes("gpt-3.5-turbo")`,
			&hasGPT35,
		),
		chromedp.Value("#model", &selectedModel, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("model dropdown check failed: %v", err)
	}

	if modelOptionCount < 3 {
		t.Errorf("model dropdown has %d options, expected at least 3 (placeholder + 2 models)", modelOptionCount)
	}
	if !hasGPT4 {
		t.Error("model dropdown missing gpt-4")
	}
	if !hasGPT35 {
		t.Error("model dropdown missing gpt-3.5-turbo")
	}
	if selectedModel != "" {
		t.Errorf("selected model = %q, want empty after initial discovery save", selectedModel)
	}
}

// TestBrowser_ConfigSavePopulatesModels verifies HTMX save succeeds when
// user selects discovered model from settings page.
func TestBrowser_ConfigSavePopulatesModels(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`)
	server := newTestServer(t)
	putBrowserConfig(t, server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"gpt-4"}`, fakeProvider.URL))

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		chromedp.SetValue("#model", "gpt-3.5-turbo", chromedp.ByQuery),
		chromedp.Click("button[type=submit]", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("form submit failed: %v", err)
	}

	var modelOptionCount int
	var hasGPT4 bool
	var hasGPT35 bool
	var selectedModel string
	err = chromedp.Run(ctx,
		chromedp.WaitReady("#model option[value='gpt-4']", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools("document.querySelector('#model').options.length", &modelOptionCount),
		chromedp.EvaluateAsDevTools(
			`Array.from(document.querySelector('#model').options).map(o => o.value).includes("gpt-4")`,
			&hasGPT4,
		),
		chromedp.EvaluateAsDevTools(
			`Array.from(document.querySelector('#model').options).map(o => o.value).includes("gpt-3.5-turbo")`,
			&hasGPT35,
		),
		chromedp.Value("#model", &selectedModel, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("model dropdown check failed: %v", err)
	}

	if modelOptionCount < 3 {
		t.Errorf("model dropdown has %d options, expected at least 3 (placeholder + 2 models)", modelOptionCount)
	}
	if !hasGPT4 {
		t.Error("model dropdown missing gpt-4")
	}
	if !hasGPT35 {
		t.Error("model dropdown missing gpt-3.5-turbo")
	}
	if selectedModel != "gpt-3.5-turbo" {
		t.Errorf("selected model = %q, want gpt-3.5-turbo", selectedModel)
	}

	var hasErrorToast bool
	_ = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools("document.querySelector('.error-toast') !== null", &hasErrorToast),
	)
	if hasErrorToast {
		t.Error("error toast present after successful config save")
	}
}

// TestBrowser_ConfigSaveProviderFailure verifies that provider validation failure
// returns swapped settings HTML with visible error feedback.
func TestBrowser_ConfigSaveProviderFailure(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusUnauthorized, `{"error":"unauthorized"}`)
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		chromedp.SetValue("#provider", "custom_openai", chromedp.ByQuery),
		chromedp.Clear("#base_url", chromedp.ByQuery),
		chromedp.SendKeys("#base_url", fakeProvider.URL, chromedp.ByQuery),
		chromedp.Clear("#api_key", chromedp.ByQuery),
		chromedp.SendKeys("#api_key", "sk-bad", chromedp.ByQuery),
		chromedp.Click("button[type=submit]", chromedp.ByQuery),
		chromedp.WaitVisible(".error-toast", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("form fill/submit failed: %v", err)
	}

	var modelOptionsEmpty bool
	var providerValue string
	var errorText string
	err = chromedp.Run(ctx,
		chromedp.Value("#provider", &providerValue, chromedp.ByQuery),
		chromedp.Text(".error-toast .error-text", &errorText, chromedp.ByQuery),
		chromedp.EvaluateAsDevTools("document.querySelector('#model').options.length <= 1", &modelOptionsEmpty),
	)
	if err != nil {
		t.Fatalf("post-submit check failed: %v", err)
	}

	if !modelOptionsEmpty {
		t.Error("model dropdown should be empty (placeholder only) after validation failure")
	}
	if providerValue != "custom_openai" {
		t.Errorf("provider should still be 'custom_openai' after error, got %q", providerValue)
	}
	if !strings.Contains(errorText, "Provider authentication failed") {
		t.Errorf("error text = %q, want auth guidance", errorText)
	}
}

// TestBrowser_SettingsSaveButtonLoadingState verifies that when the save button is clicked,
// it shows "Saving…" text and is disabled during the HTMX request, then re-enabled after.
func TestBrowser_SettingsSaveButtonLoadingState(t *testing.T) {
	// Use a slow provider server so the request takes long enough to observe loading state
	fakeProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(fakeProvider.Close)

	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var initialText, loadingText, postSubmitText string
	var submitDisabled bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		// Set provider to custom_openai and fill credentials so save will succeed
		chromedp.SetValue("#provider", "custom_openai", chromedp.ByQuery),
		chromedp.Clear("#base_url", chromedp.ByQuery),
		chromedp.SendKeys("#base_url", fakeProvider.URL, chromedp.ByQuery),
		chromedp.Clear("#api_key", chromedp.ByQuery),
		chromedp.SendKeys("#api_key", "sk-test", chromedp.ByQuery),
		// Read button text before click
		chromedp.Text("button[type=submit]", &initialText, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("initial setup failed: %v", err)
	}
	if !strings.Contains(initialText, "Save") {
		t.Errorf("initial button text = %q, want containing 'Save'", initialText)
	}

	// Click submit. The provider is slow (200ms delay), so we can observe loading state.
	err = chromedp.Run(ctx,
		chromedp.Click("button[type=submit]", chromedp.ByQuery),
		// Wait for beforeSend to fire (HTMX fires synchronously before XMLHttpRequest.send)
		chromedp.Sleep(150*time.Millisecond),
		chromedp.Text("button[type=submit]", &loadingText, chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(
			`document.querySelector('button[type=submit]').disabled`,
			&submitDisabled,
		),
	)
	if err != nil {
		t.Fatalf("loading state check failed: %v", err)
	}
	if !strings.Contains(loadingText, "Saving") {
		t.Errorf("button text during save = %q, want containing 'Saving'", loadingText)
	}
	if !submitDisabled {
		t.Error("submit button should be disabled during save request")
	}

	// Wait for the swap to complete (after provider delay), then verify button is re-enabled
	err = chromedp.Run(ctx,
		chromedp.WaitVisible(".save-success", chromedp.ByQuery),
		chromedp.Text("button[type=submit]", &postSubmitText, chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(
			`document.querySelector('button[type=submit]').disabled`,
			&submitDisabled,
		),
	)
	if err != nil {
		t.Fatalf("post-save state check failed: %v", err)
	}
	if !strings.Contains(postSubmitText, "Save") {
		t.Errorf("post-save button text = %q, want containing 'Save'", postSubmitText)
	}
	if submitDisabled {
		t.Error("submit button should be re-enabled after save completes")
	}
}

// TestBrowser_SettingsSaveShowsSuccessIndicator verifies that after a successful config
// save via PUT /api/config, the settings form shows a "✓ Saved" success indicator.
func TestBrowser_SettingsSaveShowsSuccessIndicator(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`)
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var successText string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		// Set provider to custom_openai and fill credentials
		chromedp.SetValue("#provider", "custom_openai", chromedp.ByQuery),
		chromedp.Clear("#base_url", chromedp.ByQuery),
		chromedp.SendKeys("#base_url", fakeProvider.URL, chromedp.ByQuery),
		chromedp.Clear("#api_key", chromedp.ByQuery),
		chromedp.SendKeys("#api_key", "sk-test", chromedp.ByQuery),
		chromedp.Click("button[type=submit]", chromedp.ByQuery),
		chromedp.WaitVisible(".save-success", chromedp.ByQuery),
		chromedp.Text(".save-success", &successText, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("save success indicator check failed: %v", err)
	}
	if !strings.Contains(successText, "Saved") {
		t.Errorf("save success text = %q, want containing 'Saved'", successText)
	}
}

func TestBrowser_SettingsSaveErrorAutoScroll(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusUnauthorized, `{"error":"unauthorized"}`)
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	// Fill form with invalid credentials and save — expect error toast
	var errorInView bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		chromedp.SetValue("#provider", "custom_openai", chromedp.ByQuery),
		chromedp.Clear("#base_url", chromedp.ByQuery),
		chromedp.SendKeys("#base_url", fakeProvider.URL, chromedp.ByQuery),
		chromedp.Clear("#api_key", chromedp.ByQuery),
		chromedp.SendKeys("#api_key", "sk-bad", chromedp.ByQuery),
		chromedp.Click("button[type=submit]", chromedp.ByQuery),
		chromedp.WaitVisible(".error-toast", chromedp.ByQuery),
		// Check if error toast is in the visible viewport (allow some tolerance for smooth scroll)
		chromedp.EvaluateAsDevTools(`
			(function() {
				var el = document.querySelector('.error-toast');
				if (!el) return false;
				var rect = el.getBoundingClientRect();
				// Allow 200px tolerance for smooth scroll animation gap
				return rect.top >= -200 && rect.left >= 0 &&
					rect.bottom <= (window.innerHeight || document.documentElement.clientHeight) + 200 &&
					rect.right <= (window.innerWidth || document.documentElement.clientWidth);
			})()
		`, &errorInView),
	)
	if err != nil {
		t.Fatalf("error scroll test failed: %v", err)
	}
	if !errorInView {
		t.Error("error-toast should be scrolled into view after failed save")
	}
}

// TestBrowser_SettingsCtrlEnterSaves verifies that Ctrl+Enter (or Cmd+Enter on macOS)
// submits the settings form from any input/textarea.
func TestBrowser_SettingsCtrlEnterSaves(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`)
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var successText string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		// Set up credentials
		chromedp.SetValue("#provider", "custom_openai", chromedp.ByQuery),
		chromedp.Clear("#base_url", chromedp.ByQuery),
		chromedp.SendKeys("#base_url", fakeProvider.URL, chromedp.ByQuery),
		chromedp.Clear("#api_key", chromedp.ByQuery),
		chromedp.SendKeys("#api_key", "sk-test", chromedp.ByQuery),
		chromedp.SetValue("#system_prompt", "test prompt", chromedp.ByQuery),
		// Dispatch Ctrl+Enter on the system_prompt textarea
		chromedp.EvaluateAsDevTools(`
			(function() {
				var textarea = document.getElementById('system_prompt');
				if (!textarea) return 'missing';
				var evt = new KeyboardEvent('keydown', {
					key: 'Enter',
					code: 'Enter',
					ctrlKey: true,
					bubbles: true,
					cancelable: true
				});
				return textarea.dispatchEvent(evt) ? 'ok' : 'prevented';
			})()
		`, &successText),
		chromedp.WaitVisible(".save-success", chromedp.ByQuery),
		chromedp.Text(".save-success", &successText, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("ctrl+enter save test failed: %v", err)
	}
	if !strings.Contains(successText, "Saved") {
		t.Errorf("save success text = %q, want containing 'Saved'", successText)
	}
}

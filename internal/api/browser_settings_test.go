package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// ————— Settings page tests ————— —

func getBrowserConfig(t *testing.T, server *httptest.Server) map[string]any {
	t.Helper()
	resp, err := http.Get(server.URL + "/api/config")
	if err != nil {
		t.Fatalf("failed to GET config: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET config status = %d, want 200", resp.StatusCode)
	}
	var cfg map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatalf("failed to decode config: %v", err)
	}
	return cfg
}

func TestBrowser_SettingsModelChangeDoesNotAutosave(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`)
	server := newTestServer(t)
	putBrowserConfig(t, server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"gpt-4"}`, fakeProvider.URL))

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var saveDisabled bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitReady("#model option[value='gpt-3.5-turbo']", chromedp.ByQuery),
		chromedp.Evaluate(`
			(function() {
				var model = document.querySelector('#model');
				model.value = 'gpt-3.5-turbo';
				model.dispatchEvent(new Event('change', { bubbles: true }));
			})()
		`, nil),
		chromedp.EvaluateAsDevTools(`document.querySelector('button[type=submit]').disabled`, &saveDisabled),
	)
	if err != nil {
		t.Fatalf("model change failed: %v", err)
	}
	if saveDisabled {
		t.Fatal("Save should be enabled when model draft is dirty")
	}

	cfg := getBrowserConfig(t, server)
	if cfg["model"] != "gpt-4" {
		t.Fatalf("saved model = %q, want unchanged gpt-4 before Save", cfg["model"])
	}
}

func TestBrowser_SettingsSavePersistsDraftAndClearsDirtyState(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`)
	server := newTestServer(t)
	putBrowserConfig(t, server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"gpt-4"}`, fakeProvider.URL))

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var saveDisabled bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitReady("#model option[value='gpt-3.5-turbo']", chromedp.ByQuery),
		chromedp.Evaluate(`
			(function() {
				var model = document.querySelector('#model');
				model.value = 'gpt-3.5-turbo';
				model.dispatchEvent(new Event('change', { bubbles: true }));
			})()
		`, nil),
		chromedp.Click("button[type=submit]", chromedp.ByQuery),
		chromedp.WaitVisible(".save-success", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`document.querySelector('button[type=submit]').disabled`, &saveDisabled),
	)
	if err != nil {
		t.Fatalf("save draft failed: %v", err)
	}
	if !saveDisabled {
		t.Fatal("Save should be disabled after successful save clears dirty state")
	}

	cfg := getBrowserConfig(t, server)
	if cfg["model"] != "gpt-3.5-turbo" {
		t.Fatalf("saved model = %q, want gpt-3.5-turbo after Save", cfg["model"])
	}
}

func TestBrowser_SettingsRevertRestoresSavedConfigWithoutWriting(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)
	server := newTestServer(t)
	putBrowserConfig(t, server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"gpt-4","system_prompt":"saved prompt"}`, fakeProvider.URL))

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var prompt string
	var saveDisabled bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#system_prompt", chromedp.ByQuery),
		chromedp.SetValue("#system_prompt", "draft prompt", chromedp.ByQuery),
		chromedp.Click("#settings-revert-btn", chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.WaitReady("#system_prompt", chromedp.ByQuery),
		chromedp.Value("#system_prompt", &prompt, chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`document.querySelector('button[type=submit]').disabled`, &saveDisabled),
	)
	if err != nil {
		t.Fatalf("revert failed: %v", err)
	}
	if prompt != "saved prompt" {
		t.Fatalf("prompt after Revert = %q, want saved prompt", prompt)
	}
	if !saveDisabled {
		t.Fatal("Save should be disabled after Revert clears dirty state")
	}

	cfg := getBrowserConfig(t, server)
	if cfg["system_prompt"] != "saved prompt" {
		t.Fatalf("saved system_prompt = %q, want unchanged saved prompt", cfg["system_prompt"])
	}
}

func TestBrowser_SettingsDirtyDraftGuardsNavigation(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)
	server := newTestServer(t)
	putBrowserConfig(t, server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"gpt-4"}`, fakeProvider.URL))

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var path string
	var unloadGuarded bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#system_prompt", chromedp.ByQuery),
		chromedp.Evaluate(`
			(function() {
				var prompt = document.querySelector('#system_prompt');
				prompt.value = 'dirty prompt';
				prompt.dispatchEvent(new Event('input', { bubbles: true }));
			})()
		`, nil),
		chromedp.EvaluateAsDevTools(`
			(function() {
				var event = new Event('beforeunload', { cancelable: true });
				window.dispatchEvent(event);
				return event.defaultPrevented;
			})()
		`, &unloadGuarded),
		chromedp.Evaluate(`window.confirm = function() { return false; }`, nil),
		chromedp.Evaluate(`document.querySelector('.nav-link[href="/skills"]').click()`, nil),
		chromedp.Sleep(100*time.Millisecond),
		chromedp.EvaluateAsDevTools(`window.location.pathname`, &path),
	)
	if err != nil {
		t.Fatalf("dirty navigation guard setup failed: %v", err)
	}
	if !unloadGuarded {
		t.Fatal("beforeunload should be guarded when settings draft is dirty")
	}
	if path != "/settings" {
		t.Fatalf("path after cancelled navigation = %q, want /settings", path)
	}

	err = chromedp.Run(ctx,
		chromedp.Evaluate(`window.confirm = function() { return true; }`, nil),
		chromedp.Evaluate(`document.querySelector('.nav-link[href="/skills"]').click()`, nil),
		chromedp.WaitVisible(".skills-view", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`window.location.pathname`, &path),
	)
	if err != nil {
		t.Fatalf("confirmed navigation failed: %v", err)
	}
	if path != "/skills" {
		t.Fatalf("path after confirmed navigation = %q, want /skills", path)
	}
}

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

	var sandboxBadgeExists bool
	var sandboxBadgeText string
	_ = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools("document.querySelector('.sandbox-badge') !== null", &sandboxBadgeExists),
		chromedp.Text(".sandbox-badge", &sandboxBadgeText, chromedp.ByQuery),
	)
	if !sandboxBadgeExists {
		t.Error(".sandbox-badge element not found in settings")
	}
	if sandboxBadgeText == "" {
		t.Error("sandbox badge text is empty")
	}
}

func TestBrowser_SettingsProviderEndpointDraftBehavior(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)
	server := newTestServer(t)
	putBrowserConfig(t, server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"gpt-4"}`, fakeProvider.URL))

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var endpointVisible bool
	var endpointEditable bool
	var endpointValue string
	var statusText string
	var saveDisabled bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#base_url", chromedp.ByQuery),
		chromedp.Evaluate(`
			(function() {
				var provider = document.querySelector('#provider');
				provider.value = 'opencode_go';
				provider.dispatchEvent(new Event('change', { bubbles: true }));
			})()
		`, nil),
		chromedp.Sleep(200*time.Millisecond),
		chromedp.EvaluateAsDevTools(`document.querySelector('#base_url').offsetParent !== null`, &endpointVisible),
		chromedp.EvaluateAsDevTools(`!document.querySelector('#base_url').disabled && !document.querySelector('#base_url').readOnly`, &endpointEditable),
		chromedp.Value("#base_url", &endpointValue, chromedp.ByQuery),
		chromedp.Text("#base-url-status", &statusText, chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`document.querySelector('#settings-save-btn').disabled`, &saveDisabled),
	)
	if err != nil {
		t.Fatalf("provider endpoint draft setup failed: %v", err)
	}
	if !endpointVisible {
		t.Fatal("Base URL should remain visible for built-in providers")
	}
	if !endpointEditable {
		t.Fatal("Base URL should remain editable for built-in providers")
	}
	if endpointValue != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("OpenCode Go endpoint = %q, want provider default", endpointValue)
	}
	if !strings.Contains(statusText, "provider default") {
		t.Fatalf("endpoint status = %q, want provider default indicator", statusText)
	}
	if saveDisabled {
		t.Fatal("provider endpoint change should mark Settings draft dirty")
	}

	cfg := getBrowserConfig(t, server)
	if cfg["provider"] != "custom_openai" || cfg["base_url"] != fakeProvider.URL {
		t.Fatalf("saved config changed before Save: provider=%q base_url=%q", cfg["provider"], cfg["base_url"])
	}

	err = chromedp.Run(ctx,
		chromedp.Clear("#base_url", chromedp.ByQuery),
		chromedp.SendKeys("#base_url", "https://override.example.com/v1", chromedp.ByQuery),
		chromedp.Text("#base-url-status", &statusText, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("endpoint override edit failed: %v", err)
	}
	if !strings.Contains(statusText, "override") {
		t.Fatalf("endpoint status after edit = %q, want override indicator", statusText)
	}

	err = chromedp.Run(ctx,
		chromedp.Click("#base-url-reset", chromedp.ByQuery),
		chromedp.Value("#base_url", &endpointValue, chromedp.ByQuery),
		chromedp.Text("#base-url-status", &statusText, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("endpoint reset failed: %v", err)
	}
	if endpointValue != "https://opencode.ai/zen/go/v1" || !strings.Contains(statusText, "provider default") {
		t.Fatalf("endpoint after reset = %q status %q, want default", endpointValue, statusText)
	}

	err = chromedp.Run(ctx,
		chromedp.Evaluate(`
			(function() {
				var provider = document.querySelector('#provider');
				provider.value = 'github_copilot';
				provider.dispatchEvent(new Event('change', { bubbles: true }));
			})()
		`, nil),
		chromedp.Sleep(200*time.Millisecond),
		chromedp.Value("#base_url", &endpointValue, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("github provider switch failed: %v", err)
	}
	if endpointValue != "https://api.githubcopilot.com" {
		t.Fatalf("GitHub Copilot endpoint = %q, want provider default", endpointValue)
	}

	var requiredHintVisible bool
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`
			(function() {
				var provider = document.querySelector('#provider');
				provider.value = 'custom_openai';
				provider.dispatchEvent(new Event('change', { bubbles: true }));
			})()
		`, nil),
		chromedp.Sleep(200*time.Millisecond),
		chromedp.Value("#base_url", &endpointValue, chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`document.querySelector('#base-url-required-hint').offsetParent !== null`, &requiredHintVisible),
	)
	if err != nil {
		t.Fatalf("custom provider switch failed: %v", err)
	}
	if endpointValue != "" {
		t.Fatalf("Custom OpenAI endpoint = %q, want cleared draft", endpointValue)
	}
	if !requiredHintVisible {
		t.Fatal("Custom OpenAI should indicate endpoint is required")
	}

	cfg = getBrowserConfig(t, server)
	if cfg["provider"] != "custom_openai" || cfg["base_url"] != fakeProvider.URL {
		t.Fatalf("saved config changed before Save after custom switch: provider=%q base_url=%q", cfg["provider"], cfg["base_url"])
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
		chromedp.Evaluate(`
			(function() {
				var model = document.querySelector('#model');
				model.value = 'gpt-3.5-turbo';
				model.dispatchEvent(new Event('change', { bubbles: true }));
			})()
		`, nil),
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
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		chromedp.Sleep(200*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("form fill/submit failed: %v", err)
	}

	var modelOptionsEmpty bool
	var providerValue string
	var feedbackText string
	err = chromedp.Run(ctx,
		chromedp.Value("#provider", &providerValue, chromedp.ByQuery),
		chromedp.Text("#settings-form", &feedbackText, chromedp.ByQuery),
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
	if !strings.Contains(feedbackText, "Provider authentication failed") && !strings.Contains(feedbackText, "model discovery failed") {
		t.Errorf("feedback text = %q, want auth or discovery guidance", feedbackText)
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

	// Wait for the swap to complete (after provider delay), then verify clean draft disables Save.
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
	if !submitDisabled {
		t.Error("submit button should be disabled after save clears dirty state")
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

	// Fill form with invalid credentials and save — expect visible feedback.
	var feedbackVisible bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		chromedp.SetValue("#provider", "custom_openai", chromedp.ByQuery),
		chromedp.Clear("#base_url", chromedp.ByQuery),
		chromedp.SendKeys("#base_url", fakeProvider.URL, chromedp.ByQuery),
		chromedp.Clear("#api_key", chromedp.ByQuery),
		chromedp.SendKeys("#api_key", "sk-bad", chromedp.ByQuery),
		chromedp.Click("button[type=submit]", chromedp.ByQuery),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		chromedp.Sleep(200*time.Millisecond),
		chromedp.EvaluateAsDevTools(`
			(function() {
				var el = document.querySelector('.error-toast') || document.querySelector('#settings-form .hint');
				if (!el) return false;
				var rect = el.getBoundingClientRect();
				return rect.top >= -200 && rect.left >= 0 &&
					rect.bottom <= (window.innerHeight || document.documentElement.clientHeight) + 200 &&
					rect.right <= (window.innerWidth || document.documentElement.clientWidth);
			})()
		`, &feedbackVisible),
	)
	if err != nil {
		t.Fatalf("error scroll test failed: %v", err)
	}
	if !feedbackVisible {
		t.Error("save feedback should be visible after failed save")
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

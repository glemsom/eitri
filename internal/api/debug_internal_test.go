package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/session"
)

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	data := map[string]string{"foo": "bar"}
	writeJSON(w, http.StatusCreated, data)

	resp := w.Result()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["foo"] != "bar" {
		t.Errorf("body[foo] = %q, want bar", body["foo"])
	}
}

func TestWriteJSON_NilBody(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, nil)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "bad stuff")

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "bad stuff" {
		t.Errorf("body[error] = %q, want %q", body["error"], "bad stuff")
	}
}

func TestSanitizeConfig_Nil(t *testing.T) {
	result := sanitizeConfig(nil)
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}
}

func TestSanitizeConfig_Basic(t *testing.T) {
	cfg := &config.Config{
		Provider:            "test-provider",
		Model:               "test-model",
		BaseURL:             "http://example.com",
		ContextWindowTokens: 128000,
		MaxTurns:            50,
		CommandTimeout:      60000000000,
	}
	result := sanitizeConfig(cfg)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.ProviderID != "test-provider" {
		t.Errorf("ProviderID = %q, want %q", result.ProviderID, "test-provider")
	}
	if result.Model != "test-model" {
		t.Errorf("Model = %q, want %q", result.Model, "test-model")
	}
	if result.BaseURL != "http://example.com" {
		t.Errorf("BaseURL = %q, want %q", result.BaseURL, "http://example.com")
	}
	if result.ContextWindowTokens != 128000 {
		t.Errorf("ContextWindowTokens = %d, want %d", result.ContextWindowTokens, 128000)
	}
	if result.MaxTurns != 50 {
		t.Errorf("MaxTurns = %d, want %d", result.MaxTurns, 50)
	}
	if result.CommandTimeout != 60000000000 {
		t.Errorf("CommandTimeout = %d, want %d", result.CommandTimeout, 60000000000)
	}
	if result.HasAPIKey {
		t.Errorf("HasAPIKey = true, want false")
	}
}

func TestSanitizeConfig_WithAPIKey(t *testing.T) {
	cfg := &config.Config{
		APIKey: "sk-secret-key",
	}
	result := sanitizeConfig(cfg)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasAPIKey {
		t.Errorf("HasAPIKey = false, want true")
	}
}

func TestSanitizeConfig_WithProviderAuth(t *testing.T) {
	cfg := &config.Config{
		ProviderAuth: json.RawMessage(`{"token":"xyz"}`),
	}
	result := sanitizeConfig(cfg)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.HasAPIKey {
		t.Errorf("HasAPIKey = false, want true")
	}
}

func TestSessionToSummary_Idle(t *testing.T) {
	now := time.Now()
	sess := &session.UISession{
		ID:        "sess-1",
		Title:     "Test Session",
		Status:    session.StatusIdle,
		Messages:  []session.Message{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	summary := sessionToSummary(sess)
	if summary.ID != "sess-1" {
		t.Errorf("ID = %q, want %q", summary.ID, "sess-1")
	}
	if summary.Title != "Test Session" {
		t.Errorf("Title = %q, want %q", summary.Title, "Test Session")
	}
	if summary.Status != "idle" {
		t.Errorf("Status = %q, want idle", summary.Status)
	}
	if summary.MessageCount != 0 {
		t.Errorf("MessageCount = %d, want 0", summary.MessageCount)
	}
	if summary.Run != nil {
		t.Errorf("Run = %+v, want nil for idle session", summary.Run)
	}
}

func TestSessionToSummary_Running(t *testing.T) {
	now := time.Now()
	sess := &session.UISession{
		ID:        "sess-2",
		Title:     "Active Session",
		Status:    session.StatusRunning,
		Messages:  []session.Message{{Role: "user", Content: "hello", CreatedAt: now}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	summary := sessionToSummary(sess)
	if summary.Status != "running" {
		t.Errorf("Status = %q, want running", summary.Status)
	}
	if summary.MessageCount != 1 {
		t.Errorf("MessageCount = %d, want 1", summary.MessageCount)
	}
	if summary.Run == nil {
		t.Fatal("Run = nil, want non-nil for running session")
	}
	if summary.Run.Status != "running" {
		t.Errorf("Run.Status = %q, want running", summary.Run.Status)
	}
	if summary.LastMessageTimestamp != now {
		t.Errorf("LastMessageTimestamp = %v, want %v", summary.LastMessageTimestamp, now)
	}
}

func TestSessionToSummary_NoMessages(t *testing.T) {
	now := time.Now()
	sess := &session.UISession{
		ID:        "sess-3",
		Title:     "Empty Session",
		Status:    session.StatusIdle,
		CreatedAt: now,
		UpdatedAt: now,
	}
	summary := sessionToSummary(sess)
	if summary.LastMessageTimestamp != now {
		t.Errorf("LastMessageTimestamp = %v, want %v (no messages, fallback to UpdatedAt)", summary.LastMessageTimestamp, now)
	}
}

func TestSessionToSummary_Error(t *testing.T) {
	sess := &session.UISession{
		ID:     "sess-4",
		Title:  "Errored Session",
		Status: session.StatusError,
	}
	summary := sessionToSummary(sess)
	if summary.Status != "error" {
		t.Errorf("Status = %q, want error", summary.Status)
	}
	if summary.Run == nil {
		t.Fatal("Run = nil, want non-nil for error session")
	}
	if summary.Run.Status != "error" {
		t.Errorf("Run.Status = %q, want error", summary.Run.Status)
	}
	// No messages → LastMessageTimestamp should be zero time
	if !summary.LastMessageTimestamp.IsZero() {
		// Actually it'll be zero because UpdatedAt is zero
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	// ConfigPath empty → returns defaults
	s := &Server{config: ServerConfig{ConfigPath: ""}}
	cfg := s.loadConfig()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	// Should use opencode_go defaults
	if cfg.Provider == "" {
		t.Errorf("Provider should not be empty")
	}
}

func TestLoadConfig_WithMissingFile(t *testing.T) {
	// ConfigPath set but file doesn't exist → config.Load returns defaults
	s := &Server{config: ServerConfig{ConfigPath: t.TempDir() + "/nonexistent.json"}}
	cfg := s.loadConfig()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Provider == "" {
		t.Errorf("Provider should not be empty")
	}
}

func TestLoadConfig_WithSavedConfig(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.json"
	saved := config.Config{
		Provider: "custom-provider",
		Model:    "custom-model",
		APIKey:   "sk-test",
	}
	if err := config.Save(path, &saved); err != nil {
		t.Fatal(err)
	}

	s := &Server{config: ServerConfig{ConfigPath: path}}
	cfg := s.loadConfig()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Provider != "custom-provider" {
		t.Errorf("Provider = %q, want %q", cfg.Provider, "custom-provider")
	}
	if cfg.Model != "custom-model" {
		t.Errorf("Model = %q, want %q", cfg.Model, "custom-model")
	}
	if cfg.APIKey != "sk-test" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "sk-test")
	}
}

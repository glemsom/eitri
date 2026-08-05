package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/persona"
	"github.com/glemsom/eitri/internal/provider"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
	"github.com/glemsom/eitri/internal/tool"
)

// testPersonaWithRequiredSkill writes a persona that requires a skill plus the
// skill itself into temp dirs, and returns a skills service rooted at the
// workspace skills dir. Mirrors the fixture in TestBatchRun_UsesActivePersona.
func testPersonaWithRequiredSkill(t *testing.T) (workspace, homeDir string, skillsSvc *skills.Service) {
	t.Helper()
	homeDir = t.TempDir()
	workspace = t.TempDir()

	personaDef := &persona.PersonaDefinition{
		Name:           "test-reviewer",
		SystemPrompt:   "You are a code reviewer. Be thorough and check for edge cases.",
		RequiredSkills: []string{"test-review-skill"},
	}
	if err := persona.SaveToHome(homeDir, personaDef); err != nil {
		t.Fatalf("save persona: %v", err)
	}

	skillDir := filepath.Join(workspace, ".eitri", "skills", "test-review-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillContent := "---\nname: test-review-skill\ndescription: A test skill for batch persona testing\n---\nReview the code for potential bugs and security issues."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	skillsSvc = skills.NewServiceWithRoots([]skills.Root{
		{Path: filepath.Join(workspace, ".eitri", "skills"), Scope: skills.ScopeProjectAgents},
	})
	return workspace, homeDir, skillsSvc
}

// TestPrepareRun_UIAndBatchParity verifies the unified run-preparation seam:
// the UI parent run and the batch parent run produce the same tool registry
// (batch gains skill; render_quick_replies stays UI-only), the same system
// prompt contract (skills catalog + <required_skills> directive), and the
// same LLM request behavior for the same config (issue #1091).
func TestPrepareRun_UIAndBatchParity(t *testing.T) {
	workspace, homeDir, skillsSvc := testPersonaWithRequiredSkill(t)

	uiSessionMgr := uisession.NewManager(10, t.TempDir())
	historyMgr := history.NewSessionManager(50)
	svc := NewRunService(RunServiceDeps{
		UISessionMgr:      uiSessionMgr,
		HistorySessionMgr: historyMgr,
		SkillsService:     skillsSvc,
	})

	cfg := RunConfig{
		ProviderID:      "opencode_go",
		BaseURL:         "http://127.0.0.1:1",
		APIKey:          "test-key",
		ModelName:       "gpt-5", // supports thinking levels (reasoning chat model)
		Workspace:       workspace,
		HomeDir:         homeDir,
		ActivePersona:   "test-reviewer",
		MaxOutputTokens: 12345,
		ThinkingLevel:   "high",
	}

	// UI session: create one and activate the persona-required skill the way
	// startRunWithConfig does, so the resolved skill context matches a browser
	// run that loaded the persona.
	uiSess, err := uiSessionMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("create UI session: %v", err)
	}
	if !uiSessionMgr.ActivateSkill(uiSess.ID, "test-review-skill") {
		t.Fatal("activate skill on UI session")
	}

	uiPrep, err := svc.prepareRun(context.Background(), cfg, runPrepOptions{
		sessionID:    uiSess.ID,
		skillCtx:     svc.resolveSessionSkillContext(uiSess.ID),
		uiSessionMgr: uiSessionMgr,
	})
	if err != nil {
		t.Fatalf("prepareRun (UI): %v", err)
	}

	batchPrep, err := svc.prepareRun(context.Background(), cfg, runPrepOptions{
		sessionID:    "batch-1",
		skillCtx:     sessionSkillContext{},
		uiSessionMgr: nil,
	})
	if err != nil {
		t.Fatalf("prepareRun (batch): %v", err)
	}

	// --- Tool registry parity ---
	commonTools := []string{
		"bash", "browser", "collect", "delegate", "edit", "grep",
		"read", "render_mermaid_diagram", "skill", "web_fetch", "write",
	}
	uiNames := uiPrep.toolReg.Names()
	batchNames := batchPrep.toolReg.Names()

	for _, name := range commonTools {
		if !containsStr(uiNames, name) {
			t.Errorf("UI registry missing tool %q (got %v)", name, uiNames)
		}
		if !containsStr(batchNames, name) {
			t.Errorf("batch registry missing tool %q (got %v)", name, batchNames)
		}
	}
	if !containsStr(uiNames, "render_quick_replies") {
		t.Errorf("UI registry missing UI-only render_quick_replies (got %v)", uiNames)
	}
	if containsStr(batchNames, "render_quick_replies") {
		t.Errorf("batch registry must not contain render_quick_replies (got %v)", batchNames)
	}

	// --- System prompt contract parity ---
	if uiPrep.systemPrompt != batchPrep.systemPrompt {
		t.Errorf("system prompt differs between UI and batch:\n--- UI ---\n%s\n--- batch ---\n%s", uiPrep.systemPrompt, batchPrep.systemPrompt)
	}
	for _, want := range []string{
		"Available skills:",
		"test-review-skill",
		"<required_skills>",
		"Required skills for this persona: test-review-skill.",
		"</required_skills>",
	} {
		if !strings.Contains(batchPrep.systemPrompt, want) {
			t.Errorf("batch system prompt missing %q:\n%s", want, batchPrep.systemPrompt)
		}
	}
	// Persona-required skill content must NOT be pre-injected (loaded via skill()).
	if strings.Contains(batchPrep.systemPrompt, "Review the code for potential bugs") {
		t.Errorf("system prompt must not pre-inject persona-required skill body:\n%s", batchPrep.systemPrompt)
	}

	// --- LLM request parity ---
	if uiPrep.req.Model != cfg.ModelName || batchPrep.req.Model != cfg.ModelName {
		t.Errorf("request model differs: UI=%q batch=%q want %q", uiPrep.req.Model, batchPrep.req.Model, cfg.ModelName)
	}
	// Both requests must be identical apart from the session-scoped
	// prompt_cache_key value.
	if !reflect.DeepEqual(uiPrep.req.MaxTokens, batchPrep.req.MaxTokens) {
		t.Errorf("request MaxTokens differs: UI=%v batch=%v", uiPrep.req.MaxTokens, batchPrep.req.MaxTokens)
	}
	if !reflect.DeepEqual(uiPrep.req.Thinking, batchPrep.req.Thinking) {
		t.Errorf("request Thinking differs: UI=%+v batch=%+v", uiPrep.req.Thinking, batchPrep.req.Thinking)
	}
	for name, want := range map[string]struct {
		prep      runPrep
		sessionID string
	}{
		"UI":    {uiPrep, uiSess.ID},
		"batch": {batchPrep, "batch-1"},
	} {
		if want.prep.req.MaxTokens == nil || *want.prep.req.MaxTokens != cfg.MaxOutputTokens {
			t.Errorf("%s request MaxTokens = %v, want %d", name, want.prep.req.MaxTokens, cfg.MaxOutputTokens)
		}
		if want.prep.req.Thinking == nil || want.prep.req.Thinking.Effort != cfg.ThinkingLevel {
			t.Errorf("%s request Thinking = %+v, want effort %q", name, want.prep.req.Thinking, cfg.ThinkingLevel)
		}
		key, ok := want.prep.req.ProviderOptions["prompt_cache_key"]
		if !ok {
			t.Errorf("%s request missing session-scoped prompt_cache_key", name)
		} else if key != want.sessionID {
			t.Errorf("%s request prompt_cache_key = %v, want %q", name, key, want.sessionID)
		}
	}
}

func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestBuildRunRequest_MaxOutputTokensAndPromptCacheKey pins the shared request
// builder: max_output_tokens comes from config, the prompt-cache key is
// session-scoped and skipped for Anthropic-routed models, and zero caps are
// left unset.
func TestBuildRunRequest_MaxOutputTokensAndPromptCacheKey(t *testing.T) {
	req := buildRunRequest(RunConfig{
		ProviderID:      "opencode_go",
		ModelName:       "test-model",
		MaxOutputTokens: 12345,
	}, "session-1")
	if req.MaxTokens == nil || *req.MaxTokens != 12345 {
		t.Fatalf("MaxTokens = %v, want 12345", req.MaxTokens)
	}
	if key, ok := req.ProviderOptions["prompt_cache_key"]; !ok || key != "session-1" {
		t.Fatalf("prompt_cache_key = %v, want session-1", req.ProviderOptions["prompt_cache_key"])
	}

	// Anthropic-routed models must not carry prompt_cache_key (provider rejects it).
	reqAnthropic := buildRunRequest(RunConfig{
		ProviderID:      "opencode_go",
		ModelName:       "qwen-max",
		MaxOutputTokens: 12345,
	}, "session-1")
	if _, ok := reqAnthropic.ProviderOptions["prompt_cache_key"]; ok {
		t.Fatal("prompt_cache_key must be skipped for Anthropic-routed models")
	}
	if reqAnthropic.MaxTokens == nil || *reqAnthropic.MaxTokens != 12345 {
		t.Fatalf("Anthropic-routed MaxTokens = %v, want 12345", reqAnthropic.MaxTokens)
	}

	// Zero MaxOutputTokens means "no explicit cap" — no MaxTokens field.
	reqZero := buildRunRequest(RunConfig{ProviderID: "opencode_go", ModelName: "test-model"}, "session-1")
	if reqZero.MaxTokens != nil {
		t.Fatalf("MaxTokens = %v, want nil for zero max_output_tokens", *reqZero.MaxTokens)
	}
}

// TestBatchRun_AppliesMaxOutputTokensAndPromptCacheKey verifies a headless
// batch run sends max_output_tokens from config and a session-scoped
// prompt_cache_key to the LLM — previously ignored by batch mode (issue #1091).
func TestBatchRun_AppliesMaxOutputTokensAndPromptCacheKey(t *testing.T) {
	const batchID = "test-batch-max-tokens"
	t.Setenv(batchSessionIDEnv, batchID)

	var sawMaxTokens, sawPromptCacheKey bool
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"max_tokens":12345`)) {
			sawMaxTokens = true
		}
		if bytes.Contains(body, []byte(`"prompt_cache_key":"test-batch-max-tokens"`)) {
			sawPromptCacheKey = true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"done"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer llm.Close()

	svc := NewRunService(RunServiceDeps{
		HistorySessionMgr: history.NewSessionManager(50),
	})
	cfg := RunConfig{
		ProviderID:      "opencode_go",
		BaseURL:         llm.URL,
		APIKey:          "test-key",
		ModelName:       "test-model",
		Workspace:       t.TempDir(),
		MaxTurns:        1,
		MaxOutputTokens: 12345,
	}

	var buf bytes.Buffer
	if _, err := svc.BatchRun(context.Background(), "hello", cfg, &buf); err != nil {
		t.Fatalf("batch run failed: %v", err)
	}
	if !sawMaxTokens {
		t.Error("batch LLM request did not carry max_tokens from config")
	}
	if !sawPromptCacheKey {
		t.Error("batch LLM request did not carry session-scoped prompt_cache_key")
	}
}

// TestBatchRun_PersonaRequiredSkillFlow verifies that a batch run with a
// persona that requires skills behaves exactly like the UI: the system prompt
// carries the skills catalog and <required_skills> directive, the skill tool
// is registered, and the agent can load the skill via skill() on its first
// turn — the loaded content flows into the conversation (issue #1091).
func TestBatchRun_PersonaRequiredSkillFlow(t *testing.T) {
	workspace, homeDir, skillsSvc := testPersonaWithRequiredSkill(t)

	var sawDirective, sawSkillTool, sawSkillContent bool
	var mu sync.Mutex
	reqCount := 0
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")

		mu.Lock()
		reqCount++
		thisReq := reqCount
		mu.Unlock()

		if bytes.Contains(body, []byte(`"name":"skill"`)) {
			sawSkillTool = true
		}
		// Go's encoding/json HTML-escapes < and > to \u003c/\u003e on the wire.
		if bytes.Contains(body, []byte(`\u003crequired_skills\u003e`)) {
			sawDirective = true
		}
		// First turn: the agent decides to load its required skill via skill().
		if thisReq == 1 {
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_skill","type":"function","function":{"name":"skill","arguments":"{\"name\":\"test-review-skill\"}"}}]},"finish_reason":"tool_calls"}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		// Second turn: the loaded skill content is in history; finish up.
		if bytes.Contains(body, []byte("Review the code for potential bugs and security issues.")) {
			sawSkillContent = true
		}
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"review complete"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer llm.Close()

	svc := NewRunService(RunServiceDeps{
		HistorySessionMgr: history.NewSessionManager(50),
		SkillsService:     skillsSvc,
	})
	cfg := RunConfig{
		ProviderID:    "opencode_go",
		BaseURL:       llm.URL,
		APIKey:        "test-key",
		ModelName:     "test-model",
		Workspace:     workspace,
		HomeDir:       homeDir,
		ActivePersona: "test-reviewer",
		MaxTurns:      5,
	}

	var buf bytes.Buffer
	content, err := svc.BatchRun(context.Background(), "review the code", cfg, &buf)
	if err != nil {
		t.Fatalf("batch run failed: %v", err)
	}
	if !strings.Contains(content, "review complete") {
		t.Fatalf("unexpected batch content: %q", content)
	}
	if !sawDirective {
		t.Error("batch system prompt did not carry the <required_skills> directive")
	}
	if !sawSkillTool {
		t.Error("batch request did not register the skill tool")
	}
	if !sawSkillContent {
		t.Error("loaded skill content did not flow into the conversation")
	}
}

// TestPrepareRun_BatchRegistryEndsBrowserSession verifies the batch-prep tool
// registry contains a SessionEnder browser tool — the mechanism BatchRun's
// deferred EndSession relies on to release browser-tool connections when a
// batch run ends (issue #1091).
func TestPrepareRun_BatchRegistryEndsBrowserSession(t *testing.T) {
	svc := NewRunService(RunServiceDeps{
		HistorySessionMgr: history.NewSessionManager(50),
	})
	cfg := RunConfig{
		ProviderID:   "opencode_go",
		BaseURL:      "http://127.0.0.1:1",
		APIKey:       "test-key",
		ModelName:    "test-model",
		Workspace:    t.TempDir(),
		BrowserWsUrl: "ws://127.0.0.1:9222/devtools/browser/test",
	}

	prep, err := svc.prepareRun(context.Background(), cfg, runPrepOptions{
		sessionID:    "batch-1",
		skillCtx:     sessionSkillContext{},
		uiSessionMgr: nil,
	})
	if err != nil {
		t.Fatalf("prepareRun: %v", err)
	}

	browser := prep.toolReg.Lookup("browser")
	if browser == nil {
		t.Fatal("batch registry missing browser tool")
	}
	if _, ok := browser.(tool.SessionEnder); !ok {
		t.Fatal("browser tool must implement tool.SessionEnder so the batch defer releases connections")
	}
	// EndSession on an idle session must be a safe no-op (mirrors UI runs).
	prep.toolReg.EndSession("batch-1")

	// Sanity: the opencode_go profile advertises prompt-cache support, so the
	// parity above exercises a real provider descriptor rather than a zero one.
	if desc, _ := provider.Describe("opencode_go"); !desc.SupportsPromptCache {
		t.Fatal("opencode_go must advertise prompt-cache support for this test to be meaningful")
	}
}

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/sandbox"
)

type configState struct {
	cfg    *config.Config
	models []string
	err    error
}

func (cs configState) valid() bool {
	return cs.cfg != nil && cs.err == nil
}

func (s *Server) loadConfigState(ctx context.Context) configState {
	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		return configState{err: err}
	}
	if err := config.Validate(cfg); err != nil {
		return configState{cfg: cfg, err: err}
	}
	models, _, _, err := s.fetchModelList(ctx, cfg)
	if err != nil {
		return configState{cfg: cfg, err: err}
	}
	if err := config.ValidateSelectedModel(cfg, models); err != nil {
		return configState{cfg: cfg, models: models, err: err}
	}
	return configState{cfg: cfg, models: models}
}

func maskedConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	masked := *cfg
	if masked.APIKey != "" {
		masked.APIKey = config.MaskAPIKey(masked.APIKey)
	}
	masked.ProviderAuth = nil
	return &masked
}

func writeSettingsForm(w http.ResponseWriter, r *http.Request, status int, cfg *config.Config, models []string, message string) {
	writeSettingsFormWithState(w, r, status, cfg, models, message, "", nil)
}

func writeConfigError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if isHTMXRequest(r) || !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_ = templates.ErrorToast(message).Render(r.Context(), w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	state := s.loadConfigState(r.Context())
	if state.cfg == nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	contextWindow := state.cfg.ContextWindowTokens
	if contextWindow == 0 {
		contextWindow = 256000
	}
	levels := provider.SupportedThinkingLevels(state.cfg.Provider, state.cfg.Model)
	component := templates.SettingsView(state.cfg, state.models, s.config.Workspace, s.chatPathForRequest(r), r.URL.Path, contextWindow, sandbox.BwrapAvailable(), levels)
	component.Render(r.Context(), w)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	state := s.loadConfigState(r.Context())
	models := state.models
	if state.cfg != nil {
		cfg = state.cfg
	}

	maskedCfg := maskedConfig(cfg)

	// HTMX-aware: return HTML fragment when HX-Request header is present
	if isHTMXRequest(r) {
		levels := provider.SupportedThinkingLevels(maskedCfg.Provider, maskedCfg.Model)
		component := templates.SettingsForm(maskedCfg, models, "", "", nil, "", sandbox.BwrapAvailable(), levels)
		component.Render(r.Context(), w)
		return
	}

	// Otherwise return JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(maskedCfg)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	patch, err := parseConfigPatch(r)
	if err != nil {
		if isRequestTooLarge(err) {
			writeRequestTooLarge(w)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Load current config
	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	newCfg := normalizePatchedConfig(cfg, patch)

	// Validate thinking_level against the selected model.
	// If the model does not support the chosen level, clear it and surface a notice.
	var notice string
	levels := provider.SupportedThinkingLevels(newCfg.Provider, newCfg.Model)
	if newCfg.ThinkingLevel != "" && newCfg.Model != "" {
		if len(levels) == 0 {
			notice = fmt.Sprintf("Thinking level %q cleared — model %q does not support reasoning effort.", newCfg.ThinkingLevel, newCfg.Model)
			newCfg.ThinkingLevel = ""
		}
	}

	// Validate field-level constraints
	if err := config.Validate(newCfg); err != nil {
		if isHTMXRequest(r) {
			writeSettingsForm(w, r, http.StatusOK, newCfg, nil, err.Error())
			return
		}
		writeConfigError(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}

	// Validate provider credentials by calling the profile's model discovery path.
	models, contextWindows, modelAPIs, err := s.fetchModelList(r.Context(), newCfg)
	if err != nil {
		if isHTMXRequest(r) {
			writeSettingsForm(w, r, http.StatusOK, newCfg, nil, err.Error())
			return
		}
		writeConfigError(w, r, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if strings.TrimSpace(newCfg.Model) != "" {
		if err := config.ValidateSelectedModel(newCfg, models); err != nil {
			// HTMX form submissions always include the model field (echoed from current
			// config). When the provider changes, the old model is likely invalid for the
			// new provider — silently clear it instead of blocking the save.
			providerChanged := newCfg.Provider != cfg.Provider
			if providerChanged && isHTMXRequest(r) {
				newCfg.Model = ""
				levels = nil
			} else {
				if isHTMXRequest(r) {
					writeSettingsForm(w, r, http.StatusOK, newCfg, models, err.Error())
					return
				}
				writeConfigError(w, r, http.StatusUnprocessableEntity, err.Error())
				return
			}
		}
	}

	// Auto-populate context window on model change if not manually overridden.
	if !newCfg.ContextWindowOverridden && newCfg.Model != "" && (newCfg.Model != cfg.Model || cfg.Model == "") {
		if cw, ok := contextWindows[newCfg.Model]; ok && cw > 0 {
			newCfg.ContextWindowTokens = cw
		} else if cw := provider.ContextWindowForModel(newCfg.Model); cw > 0 {
			newCfg.ContextWindowTokens = cw
		}
	}

	if newCfg.Provider == "github_copilot" && newCfg.Model != "" {
		newCfg.ModelAPI = modelAPIs[newCfg.Model]
	} else {
		newCfg.ModelAPI = ""
	}

	// Save
	if err := s.saveProviderConfig(newCfg); err != nil {
		http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Render form with success indicator (and notice if thinking_level was cleared)
	component := templates.SettingsForm(maskedConfig(newCfg), models, "", notice, nil, "✓ Saved", sandbox.BwrapAvailable(), levels)
	component.Render(r.Context(), w)
}

// discoverModelList calls Provider discovery seam. It passes PersistAuth if
// available so that refreshed auth state is persisted automatically.
func (s *Server) discoverModelList(ctx context.Context, cfg *config.Config) (*provider.DiscoveryResult, error) {
	return provider.DiscoverModels(ctx, provider.DiscoveryRequest{
		ProviderID:    cfg.Provider,
		BaseURL:       cfg.BaseURL,
		APIKey:        cfg.APIKey,
		ProviderAuth:  cfg.ProviderAuth,
		SelectedModel: cfg.Model,
	}, provider.DiscoveryOptions{
		HTTPClient:         s.httpClient,
		GitHubCopilotOAuth: s.copilotOAuth,
		PersistAuth:        s.persistAuth(),
	})
}

// fetchModelList calls Provider discovery seam. Auth refresh is persisted
// automatically via PersistAuth when configured, or manually when not.
// Returns discovered model IDs, per-model context windows, per-model API modes,
// and any error.
//
// When noPersist is true, auth updates are NOT saved to disk. This should be
// set when the cfg has been temporarily overridden (e.g., query-param overrides
// in handleGetModels) to avoid corrupting the saved config.
func (s *Server) fetchModelList(ctx context.Context, cfg *config.Config, noPersist ...bool) ([]string, map[string]int, map[string]string, error) {
	result, err := s.discoverModelList(ctx, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	if result == nil {
		return nil, nil, nil, nil
	}

	skipPersist := len(noPersist) > 0 && noPersist[0]

	if result.AuthUpdate != nil && !skipPersist {
		// PersistAuth was not set — save the auth update manually.
		applyAuthUpdate(cfg, result.AuthUpdate)
		if err := s.saveProviderConfig(cfg); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to save refreshed provider auth: %w", err)
		}
	} else if s.persistAuth() != nil && !skipPersist {
		// PersistAuth handled persistence; reload only ProviderAuth (refreshed by
		// auth handler). Do NOT overwrite APIKey — caller may have set a new key.
		loaded, loadErr := config.Load(s.config.ConfigPath)
		if loadErr == nil {
			cfg.ProviderAuth = append(json.RawMessage(nil), loaded.ProviderAuth...)
		}
	}
	return result.Models, result.ModelContextWindows, result.ModelAPIs, nil
}

func applyAuthUpdate(cfg *config.Config, update *provider.AuthUpdate) {
	if update == nil {
		return
	}
	cfg.APIKey = update.APIKey
	if len(update.ProviderAuth) == 0 {
		cfg.ProviderAuth = nil
		return
	}
	cfg.ProviderAuth = append(json.RawMessage(nil), update.ProviderAuth...)
}

// persistAuth returns the PersistAuth callback, or nil if none is configured.
func (s *Server) persistAuth() provider.PersistAuthFunc {
	return s.persistAuthFn
}

func (s *Server) saveProviderConfig(cfg *config.Config) error {
	return config.Save(s.config.ConfigPath, cfg)
}

// providerDisplayName returns the human-readable name for a provider ID.
func (s *Server) providerDisplayName(id string) string {
	if d, err := provider.Describe(id); err == nil {
		return d.DisplayName
	}
	return id
}

func (s *Server) handleGetModels(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	result, err := s.modelListForRequest(r.Context(), r, cfg)
	if err != nil {
		message := testConnectionErrorMessage(result.providerID, err)
		// When provider is overridden by query params, auth likely doesn't match
		// the saved credentials. Return empty models instead of an error so the
		// JS refreshModels() can update the select without showing a confusing
		// error toast. The user will see an empty dropdown and complete auth on save.
		if result.providerOverridden && !isHTMXRequest(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(testConnectionJSON(result.withModels(nil)))
			return
		}

		if isHTMXRequest(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			writeTestConnectionHTML(w, false, "Connection failed: "+message, nil, result.selectedModel)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": message})
		return
	}

	if isHTMXRequest(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		writeTestConnectionHTML(w, true, result.message, result.models, result.selectedModel)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(testConnectionJSON(result))
}

type modelListRequestResult struct {
	models                   []string
	providerID               string
	providerOverridden       bool
	selectedModel            string
	selectedModelVerified    bool
	selectedModelUnavailable bool
	message                  string
}

func (r modelListRequestResult) withModels(models []string) modelListRequestResult {
	r.models = models
	r.selectedModelVerified = false
	r.selectedModelUnavailable = false
	r.message = testConnectionMessage(r.models, r.selectedModel, false, false)
	return r
}

func (s *Server) modelListForRequest(ctx context.Context, r *http.Request, saved *config.Config) (modelListRequestResult, error) {
	patch, err := parseConfigPatch(r)
	if err != nil {
		return modelListRequestResult{}, err
	}
	providerID := saved.Provider
	providerOverridden := false
	if v, ok := patch["provider"].(string); ok && v != "" {
		providerID = v
		providerOverridden = v != saved.Provider
	}
	base := modelListRequestResult{providerID: providerID, providerOverridden: providerOverridden, selectedModel: saved.Model}
	if !hasModelConfigPatch(patch) {
		models, _, _, err := s.fetchModelList(ctx, saved)
		return modelListResult(models, saved.Model, providerID, providerOverridden), err
	}

	current := normalizePatchedConfig(saved, patch)
	base.providerID = current.Provider
	base.selectedModel = current.Model
	if err := validateModelDiscoveryDraft(current); err != nil {
		return base, err
	}
	result, err := provider.DiscoverModels(ctx, provider.DiscoveryRequest{
		ProviderID:    current.Provider,
		BaseURL:       current.BaseURL,
		APIKey:        current.APIKey,
		ProviderAuth:  current.ProviderAuth,
		SelectedModel: current.Model,
	}, provider.DiscoveryOptions{
		HTTPClient:         s.httpClient,
		GitHubCopilotOAuth: s.copilotOAuth,
		// No PersistAuth here: refresh/test must not save unsaved form values.
	})
	if err != nil {
		return base, err
	}
	if result == nil {
		return modelListResult(nil, current.Model, current.Provider, providerOverridden), nil
	}
	return modelListResult(result.Models, current.Model, current.Provider, providerOverridden), nil
}

func modelListResult(models []string, selectedModel, providerID string, providerOverridden bool) modelListRequestResult {
	verified := false
	if strings.TrimSpace(selectedModel) != "" {
		for _, model := range models {
			if model == selectedModel {
				verified = true
				break
			}
		}
	}
	unavailable := strings.TrimSpace(selectedModel) != "" && !verified
	return modelListRequestResult{
		models:                   models,
		providerID:               providerID,
		providerOverridden:       providerOverridden,
		selectedModel:            selectedModel,
		selectedModelVerified:    verified,
		selectedModelUnavailable: unavailable,
		message:                  testConnectionMessage(models, selectedModel, verified, unavailable),
	}
}

func testConnectionMessage(models []string, selectedModel string, verified, unavailable bool) string {
	count := len(models)
	modelWord := "models"
	if count == 1 {
		modelWord = "model"
	}
	message := fmt.Sprintf("Connection OK — discovered %d %s", count, modelWord)
	if verified {
		message += fmt.Sprintf("; %s verified", selectedModel)
	} else if unavailable {
		message += fmt.Sprintf("; %s unavailable", selectedModel)
	}
	return message
}

func testConnectionErrorMessage(providerID string, err error) string {
	if providerID == "github_copilot" && strings.Contains(err.Error(), "token is required") {
		return "Authenticate with GitHub or enter a token, then test connection again."
	}
	return err.Error()
}

func testConnectionJSON(result modelListRequestResult) map[string]any {
	models := result.models
	if models == nil {
		models = []string{}
	}
	return map[string]any{
		"object":                     "list",
		"data":                       models,
		"count":                      len(models),
		"selected_model":             result.selectedModel,
		"selected_model_verified":    result.selectedModelVerified,
		"selected_model_unavailable": result.selectedModelUnavailable,
		"message":                    result.message,
	}
}

func writeTestConnectionHTML(w http.ResponseWriter, success bool, message string, models []string, selectedModel string) {
	className := "connection-err"
	prefix := ""
	if success {
		className = "connection-ok"
		prefix = "✓ "
	}
	fmt.Fprintf(w, `<span class="%s">%s%s</span>`, className, prefix, html.EscapeString(message))
	if models == nil {
		return
	}
	fmt.Fprint(w, `<select id="model" name="model" hx-swap-oob="outerHTML">`)
	fmt.Fprint(w, `<option value="" disabled`)
	if selectedModel == "" {
		fmt.Fprint(w, ` selected`)
	}
	fmt.Fprint(w, `>Select a model...</option>`)
	selectedFound := false
	for _, model := range models {
		if model == selectedModel {
			selectedFound = true
		}
		selectedAttr := ""
		if model == selectedModel {
			selectedAttr = ` selected`
		}
		escaped := html.EscapeString(model)
		fmt.Fprintf(w, `<option value="%s"%s>%s</option>`, escaped, selectedAttr, escaped)
	}
	if selectedModel != "" && !selectedFound {
		escaped := html.EscapeString(selectedModel)
		fmt.Fprintf(w, `<option value="%s" selected>%s (unavailable)</option>`, escaped, escaped)
	}
	fmt.Fprint(w, `</select>`)
}

func validateModelDiscoveryDraft(cfg *config.Config) error {
	if _, err := provider.Describe(cfg.Provider); err != nil {
		return err
	}
	if err := provider.ValidateCredentials(cfg.Provider, cfg.APIKey, cfg.ProviderAuth); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return fmt.Errorf("base_url is required")
	}
	if _, err := url.ParseRequestURI(cfg.BaseURL); err != nil {
		return fmt.Errorf("base_url is not a valid URL: %v", err)
	}
	return nil
}

func hasModelConfigPatch(patch map[string]any) bool {
	for _, key := range []string{"provider", "base_url", "api_key", "clear_api_key", "model"} {
		if _, ok := patch[key]; ok {
			return true
		}
	}
	return false
}

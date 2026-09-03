package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/glemsom/eitri/internal/config"
)

// ErrReauthRequired is returned by a Copilot batch run when no usable credential is available — a valid access token nor a refresh path.
var ErrReauthRequired = errors.New("copilot: no valid credential; re-authenticate in the TUI, which saves a fresh token to config")

// copilotIntegrationID identifies Eitri to GitHub's Copilot backend as a chat
// surface. GitHub's Copilot API gates response shape (including whether
// reasoning/CoT deltas are included in the stream) on this header; requests
// that omit it can still succeed with 200s while silently missing reasoning
// content. copilot-developer-cli is accepted for both OAuth device-flow
// tokens and PATs (unlike vscode-chat, which PATs reject).
const copilotIntegrationID = "copilot-developer-cli"

// copilotEditorVersion is the paired Editor-Version identity header GitHub's
// Copilot API expects alongside Copilot-Integration-Id.
const copilotEditorVersion = "Eitri/1.0.0"

// RefreshFunc renews a Copilot credential from a refresh token, non-interactively, returning a fresh token set.
type RefreshFunc func(ctx context.Context, refreshToken string) (config.CopilotConfig, error)

// CopilotProvider is the GitHub Copilot provider (device-flow OAuth via the TUI).
type CopilotProvider struct {
	url  string
	http *http.Client

	cfg     config.CopilotConfig
	refresh RefreshFunc
	persist func(config.CopilotConfig) error

	mu              sync.RWMutex
	responsesModels map[string]bool

	chat      Dialect
	responses Dialect
}

// NewCopilot returns a Copilot provider talking to the Chat-Completions url (e.g. https://api.githubcopilot.com/chat/completions) with the stored credential cfg. refresh provides the non-interactive renewal path (nil means no refresh is available); persist saves renewed tokens to config (nil skips).
func NewCopilot(cfg config.CopilotConfig, url string, httpc *http.Client, refresh RefreshFunc, persist func(config.CopilotConfig) error) *CopilotProvider {
	return &CopilotProvider{
		url:             url,
		http:            httpc,
		cfg:             cfg,
		refresh:         refresh,
		persist:         persist,
		responsesModels: map[string]bool{},
		chat:            newCopilotChatCompletionsDialect(),
		responses:       NewResponsesDialect(),
	}
}

// SupportedGenerationControls delegates to the Copilot chat dialect's declared capabilities: the Generation Budget control (streamed through the same Chat-Completions wire as the primary provider and emitted as max_completion_tokens on special turns) plus Thinking Suppression, carried as an explicit thinking:{type:disabled} toggle when thinking is off.
func (cp *CopilotProvider) SupportedGenerationControls(context.Context) ([]GenerationControl, error) {
	return cp.chat.Capabilities(), nil
}

// bearer resolves the bearer token for a run.
func (cp *CopilotProvider) bearer(ctx context.Context) (string, error) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cfg := cp.cfg
	switch {
	case cfg.AccessToken != "" && (cfg.ExpiresAt == 0 || time.Now().Unix() < cfg.ExpiresAt):
		return cfg.AccessToken, nil
	case cfg.RefreshToken != "":
		if cp.refresh == nil {
			return "", ErrReauthRequired
		}
		fresh, err := cp.refresh(ctx, cfg.RefreshToken)
		if err != nil {
			return "", fmt.Errorf("copilot: refresh token renewal failed: %w", err)
		}
		if cp.persist != nil {
			_ = cp.persist(fresh)
		}
		cp.cfg = fresh
		if fresh.AccessToken == "" {
			return "", ErrReauthRequired
		}
		return fresh.AccessToken, nil
	default:
		return "", ErrReauthRequired
	}
}

// Stream implements Provider, authenticating with the resolved bearer token and streaming Copilot's chat endpoint by default, with an automatic retry on the Responses endpoint for models Copilot rejects as responses-only.
func (cp *CopilotProvider) Stream(ctx context.Context, req Request) (Stream, error) {
	tok, err := cp.bearer(ctx)
	if err != nil {
		return nil, err
	}
	if cp.usesResponses(req.Model) {
		return cp.streamResponses(ctx, tok, req)
	}
	s, err := cp.streamChatCompletions(ctx, tok, req)
	if retryResponses(err) {
		cp.rememberResponsesModel(req.Model)
		return cp.streamResponses(ctx, tok, req)
	}
	return s, err
}

func (cp *CopilotProvider) streamChatCompletions(ctx context.Context, tok string, req Request) (Stream, error) {
	body, err := cp.chat.Build(req)
	if err != nil {
		return nil, err
	}
	resp, err := cp.do(ctx, tok, cp.url, body)
	if err != nil {
		return nil, err
	}
	return closeBodyOnDone(cp.chat.Stream(resp.Body), resp.Body), nil
}

func (cp *CopilotProvider) streamResponses(ctx context.Context, tok string, req Request) (Stream, error) {
	body, err := cp.responses.Build(req)
	if err != nil {
		return nil, err
	}
	resp, err := cp.do(ctx, tok, cp.responsesURL(), body)
	if err != nil {
		return nil, err
	}
	return closeBodyOnDone(cp.responses.Stream(resp.Body), resp.Body), nil
}

func (cp *CopilotProvider) do(ctx context.Context, tok, url string, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+tok)
	setCopilotIdentityHeaders(httpReq)
	client := resolveClient(cp.http)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		return nil, &HTTPError{Code: resp.StatusCode, Body: string(body)}
	}
	return resp, nil
}

// setCopilotIdentityHeaders stamps the client-identity headers GitHub's
// Copilot API expects on every request (chat/completions, responses, and
// model discovery alike).
func setCopilotIdentityHeaders(httpReq *http.Request) {
	httpReq.Header.Set("Copilot-Integration-Id", copilotIntegrationID)
	httpReq.Header.Set("Editor-Version", copilotEditorVersion)
}

func (cp *CopilotProvider) responsesURL() string {
	base := strings.TrimSuffix(cp.url, "/chat/completions")
	return strings.TrimSuffix(base, "/") + "/responses"
}

func (cp *CopilotProvider) usesResponses(model string) bool {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	return cp.responsesModels[model]
}

func (cp *CopilotProvider) rememberResponsesModel(model string) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.responsesModels[model] = true
}

func retryResponses(err error) bool {
	var he *HTTPError
	if !errors.As(err, &he) || he.Code != http.StatusBadRequest {
		return false
	}
	return (strings.Contains(he.Body, "unsupported_api_for_model") && strings.Contains(he.Body, "/chat/completions")) ||
		(strings.Contains(he.Body, "Function tools with reasoning_effort are not supported") && strings.Contains(he.Body, "/chat/completions"))
}

// Models implements the optional ModelLister capability so the TUI Settings surface can surface the Copilot model lineup.
func (cp *CopilotProvider) Models(ctx context.Context) ([]ModelInfo, error) {
	tok, err := cp.bearer(ctx)
	if err != nil {
		return nil, err
	}
	base := strings.TrimSuffix(cp.url, "/chat/completions")
	modelsURL := strings.TrimSuffix(base, "/") + "/models"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+tok)
	setCopilotIdentityHeaders(httpReq)
	client := resolveClient(cp.http)
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, &HTTPError{Code: resp.StatusCode, Body: "copilot model discovery returned non-2xx"}
	}
	var out modelList
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	models := make([]ModelInfo, 0, len(out.Data))
	for _, m := range out.Data {
		kind := inferEndpointKind(m)
		if kind == EndpointResponses {
			cp.rememberResponsesModel(m.ID)
		}
		models = append(models, ModelInfo{ID: m.ID, EndpointKind: kind})
	}
	return models, nil
}

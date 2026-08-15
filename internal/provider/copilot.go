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
	"time"

	"github.com/glemsom/eitri/internal/config"
)

// ErrReauthRequired is returned by a Copilot batch run when no usable
// credential is available — a valid access token nor a refresh path. Batch
// never runs the interactive device flow; the message directs the user to
// re-authenticate in the TUI, which persists a fresh token to config (T11).
var ErrReauthRequired = errors.New("Copilot: no valid credential; re-authenticate in the TUI, which saves a fresh token to config")

// RefreshFunc renews a Copilot credential from a refresh token, non-interactively
// returning a fresh token set. It is the batch-sanctioned automatic renewal path
// (T11): full device-flow OAuth is TUI-only.
type RefreshFunc func(ctx context.Context, refreshToken string) (config.CopilotConfig, error)

// CopilotProvider is the GitHub Copilot provider (device-flow OAuth via the
// TUI). It re-expresses through the same Chat-Completions wire seam as the
// primary provider — only authentication differs — so a Copilot model maps into
// the same reasoning/tool channel the engine already handles (acceptance: Copilot
// streaming/reasoning through the shared seam). Batch consumes a stored/refreshed
// bearer token non-interactively; the interactive device-flow handshake is the
// TUI's job.
type CopilotProvider struct {
	url  string
	http *http.Client

	cfg config.CopilotConfig
	// refresh renews an expired/absent access token via the refresh path.
	refresh RefreshFunc
	// persist stores a renewed token set back to config for later runs.
	persist func(config.CopilotConfig) error
}

// NewCopilot returns a Copilot provider talking to the Chat-Completions url
// (e.g. https://api.githubcopilot.com/chat/completions) with the stored
// credential cfg. refresh provides the non-interactive renewal path (nil means
// no refresh is available); persist saves renewed tokens to config (nil skips).
func NewCopilot(cfg config.CopilotConfig, url string, httpc *http.Client, refresh RefreshFunc, persist func(config.CopilotConfig) error) *CopilotProvider {
	return &CopilotProvider{url: url, http: httpc, cfg: cfg, refresh: refresh, persist: persist}
}

// SupportedGenerationControls declares that Copilot can honor the Generation
// Budget control, since it streams through the same Chat-Completions wire as the
// primary provider and emits max_completion_tokens on special turns
// (issue #60). The other three generation controls are not supported here.
func (cp *CopilotProvider) SupportedGenerationControls(context.Context) ([]GenerationControl, error) {
	return []GenerationControl{GenerationControlGenerationBudget}, nil
}

// bearer resolves the bearer token for a run. Batch logic: a valid stored
// access token is used directly; otherwise a refresh token, when present,
// renews non-interactively; otherwise no usable credential — ErrReauthRequired.
func (cp *CopilotProvider) bearer(ctx context.Context) (string, error) {
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

// Stream implements Provider, authenticating with the resolved bearer token and
// streaming the shared Chat-Completions SSE wire (same parse/accumulator as the
// primary provider: reasoning_content, tool_calls, streamed usage).
func (cp *CopilotProvider) Stream(ctx context.Context, req Request) (Stream, error) {
	tok, err := cp.bearer(ctx)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(chatCompletionBody{
		Model:           req.Model,
		Messages:        req.Messages,
		Tools:           req.Tools,
		ToolChoice:      req.ToolChoice,
		Stream:          true,
		StreamOptions:   &streamOptions{IncludeUsage: true},
		ReasoningEffort: reasoningEffortControl(req),
		MaxOutputTokens: maxOutputTokens(req),
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, cp.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+tok)

	client := cp.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		return nil, &HTTPError{Code: resp.StatusCode, Body: string(body)}
	}
	return &openAIStream{ev: newSSE(resp.Body), acc: newToolAccumulator()}, nil
}

// Models implements the optional ModelLister capability so the TUI Settings
// surface can surface the Copilot model lineup. The models URL is derived from
// the Chat-Completions url by stripping the /chat/completions suffix, mirroring
// the primary provider's derivation (research/opencode-endpoints.md §3).
func (cp *CopilotProvider) Models(ctx context.Context) ([]string, error) {
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
	client := cp.http
	if client == nil {
		client = http.DefaultClient
	}
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
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

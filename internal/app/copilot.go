package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/glemsom/eitri/internal/config"
)

// GitHub's Copilot OAuth token endpoint: same host as the device-flow handshake,
// reused for the non-interactive refresh path batch is allowed to take (T11).
const copilotTokenURL = "https://github.com/login/oauth/access_token"

// copilotRefresh returns a provider.RefreshFunc that renews a Copilot
// credential from a refresh token via GitHub's OAuth token endpoint. It is the
// batch-sanctioned automatic renewal path: no device flow, no user interaction.
func copilotRefresh(httpc *http.Client) func(ctx context.Context, refreshToken string) (config.CopilotConfig, error) {
	return func(ctx context.Context, refreshToken string) (config.CopilotConfig, error) {
		form := url.Values{}
		form.Set("grant_type", "refresh_token")
		form.Set("refresh_token", refreshToken)
		form.Set("client_id", "Iv1.b507a08c87ecfe98")

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, copilotTokenURL, strings.NewReader(form.Encode()))
		if err != nil {
			return config.CopilotConfig{}, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")

		client := httpc
		if client == nil {
			client = http.DefaultClient
		}
		resp, err := client.Do(req)
		if err != nil {
			return config.CopilotConfig{}, err
		}
		defer resp.Body.Close()

		var tok struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int64  `json:"expires_in"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
			return config.CopilotConfig{}, err
		}
		if tok.AccessToken == "" && tok.RefreshToken == "" {
			return config.CopilotConfig{}, fmt.Errorf("copilot token endpoint returned no credential (HTTP %d)", resp.StatusCode)
		}
		cfg := config.CopilotConfig{
			AccessToken:  tok.AccessToken,
			RefreshToken: tok.RefreshToken,
		}
		if tok.ExpiresIn > 0 {
			cfg.ExpiresAt = time.Now().Unix() + tok.ExpiresIn
		}
		return cfg, nil
	}
}

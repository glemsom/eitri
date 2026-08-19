package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/provider"
)

// copilotTokenURL is GitHub's Copilot OAuth access-token endpoint: the same host
// as the device-flow handshake, reused for the non-interactive refresh path
// batch may take. It is a package-level var (not a const) so the renewal path
// can be pointed at an httptest server in tests.
var copilotTokenURL = "https://github.com/login/oauth/access_token"

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

// CopilotConnect runs the TUI-side GitHub device-flow handshake end to end:
// it starts the flow, presents the user code + verification URI to stdErr, polls
// to completion, and persists the fresh token set to config. It is the
// interactive re-auth surface driveable by the TUI; batch never calls it. onCode
// is called with the code to display once the flow starts (nil → the code is
// printed to stderr).
// newDeviceFlow constructs the device-flow client; package-level seam so tests stub the GitHub endpoints.
var newDeviceFlow = provider.NewDeviceFlow

func CopilotConnect(ctx context.Context, cfgPath string, httpc *http.Client, onCode func(provider.DeviceCode)) (config.Config, error) {
	flow := newDeviceFlow(httpc, nil)
	cd, err := flow.Start(ctx)
	if err != nil {
		return config.Config{}, err
	}
	if onCode != nil {
		onCode(cd)
	} else {
		fmt.Fprintf(os.Stderr, "Open https://github.com/login/device and enter code: %s\n", cd.UserCode)
	}
	interval := time.Duration(cd.Interval) * time.Second
	if interval <= 0 {
		interval = time.Second
	}
	var tok config.CopilotConfig
	for {
		tok, err = flow.Poll(ctx, cd.DeviceCode)
		if err == nil {
			break
		}
		if !provider.IsAuthorizationPending(err) {
			return config.Config{}, err
		}
		select {
		case <-ctx.Done():
			return config.Config{}, ctx.Err()
		case <-time.After(interval):
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return config.Config{}, err
	}
	cfg.Provider = provider.ProviderCopilot
	cfg.Copilot = tok
	if err := config.Save(cfg, cfgPath); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

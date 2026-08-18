package provider

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/glemsom/eitri/internal/config"
)

// errAuthorizationPending is the device-flow interim "user hasn't approved yet"
// signal; Poll returns it wrapped so callers retry on interval.
var errAuthorizationPending = errors.New("device flow: authorization pending")

// IsAuthorizationPending reports whether err is the "wait, keep polling" state
// of a device code not yet approved.
func IsAuthorizationPending(err error) bool {
	return errors.Is(err, errAuthorizationPending)
}

// Defaulted GitHub device-flow endpoints. Test doubles override these via
// DeviceFlow{...}.endpoints.
const (
	githubDeviceCodeURL  = "https://github.com/login/device/code"
	githubAccessTokenURL = "https://github.com/login/oauth/access_token"
	githubOAuthClientID  = "Iv1.b507a08c87ecfe98"
)

// DeviceCode is the intermediate device-flow handshake state: the user is shown
// the code + verification URI and asked to approve; Poll then exchanges it.
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// DeviceFlow is the GitHub device-flow OAuth client behind the TUI-only
// Copilot approval screen. Batch never runs this — it consumes stored/refreshed
// credentials instead; the interactive handshake is the TUI's job.
type DeviceFlow struct {
	http      *http.Client
	endpoints map[string]string
	mu        sync.Mutex
}

// NewDeviceFlow returns a device-flow client. overrides, when non-nil, lets
// tests stub the code/token endpoints keyed by "code" and "token".
func NewDeviceFlow(httpc *http.Client, overrides map[string]string) *DeviceFlow {
	ep := map[string]string{
		"code":  githubDeviceCodeURL,
		"token": githubAccessTokenURL,
	}
	maps.Copy(ep, overrides)
	return &DeviceFlow{http: httpc, endpoints: ep}
}

// Start begins a device-flow handshake, returning the code to display for
// in-UI approval. It is the first half of the TUI re-auth path.
func (d *DeviceFlow) Start(ctx context.Context) (DeviceCode, error) {
	form := url.Values{}
	form.Set("client_id", githubOAuthClientID)
	form.Set("scope", "read:user user:email")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint("code"), strings.NewReader(form.Encode()))
	if err != nil {
		return DeviceCode{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	var cd DeviceCode
	if err := d.doJSON(req, &cd); err != nil {
		return DeviceCode{}, err
	}
	return cd, nil
}

// Poll exchanges an approved device code for a fresh credential. It returns
// a pending signal (IsAuthorizationPending) until the user approves, then the
// token set the TUI persists to config. Re-entrant safe.
func (d *DeviceFlow) Poll(ctx context.Context, deviceCode string) (config.CopilotConfig, error) {
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	form.Set("client_id", githubOAuthClientID)
	form.Set("device_code", deviceCode)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint("token"), strings.NewReader(form.Encode()))
	if err != nil {
		return config.CopilotConfig{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	var resp struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		Error           string `json:"error"`
		AccessToken     string `json:"access_token"`
		RefreshToken    string `json:"refresh_token"`
		ExpiresIn       int64  `json:"expires_in"`
	}
	if err := d.doJSON(req, &resp); err != nil {
		return config.CopilotConfig{}, err
	}
	if resp.Error == "authorization_pending" || resp.Error == "slow_down" {
		return config.CopilotConfig{}, errAuthorizationPending
	}
	if resp.AccessToken == "" && resp.RefreshToken == "" {
		return config.CopilotConfig{}, &flowError{msg: "device flow token exchange failed: " + resp.Error}
	}
	cfg := config.CopilotConfig{AccessToken: resp.AccessToken, RefreshToken: resp.RefreshToken}
	if resp.ExpiresIn > 0 {
		cfg.ExpiresAt = time.Now().Unix() + resp.ExpiresIn
	}
	return cfg, nil
}

func (d *DeviceFlow) endpoint(key string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.endpoints[key]
}

// doJSON POSTs req and decodes a JSON response.
func (d *DeviceFlow) doJSON(req *http.Request, out any) error {
	client := resolveClient(d.http)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// flowError is a mismatch-value device-flow error.
type flowError struct{ msg string }

func (e *flowError) Error() string { return e.msg }

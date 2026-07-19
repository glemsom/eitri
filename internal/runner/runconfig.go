package runner

import (
	"encoding/json"
	"time"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/history"
)

// RunConfig bundles all per-run configuration that StartRun needs.
// Extracted from RunService fields so StartRun can be called directly
// with explicit config instead of relying on mutable service state.
type RunConfig struct {
	ProviderID          string
	BaseURL             string
	APIKey              string
	ModelName           string
	SystemPrompt        string
	MaxTurns            int
	MaxHistory          int
	AllowedReadPaths    []string
	ProviderAuth        json.RawMessage
	Workspace           string
	CmdTimeout          time.Duration
	ContextWindowTokens int
}

// FromConfig builds a RunConfig from a Config value object plus
// environment-specific workspace and command timeout.
func FromConfig(cfg *config.Config, workspace string, cmdTimeout time.Duration) RunConfig {
	sp := cfg.SystemPrompt
	if sp == "" {
		sp = history.DefaultSystemPrompt
	}

	return RunConfig{
		ProviderID:          cfg.Provider,
		BaseURL:             cfg.BaseURL,
		APIKey:              cfg.APIKey,
		ModelName:           cfg.Model,
		SystemPrompt:        sp,
		MaxTurns:            cfg.MaxTurns,
		MaxHistory:          cfg.MaxHistory,
		AllowedReadPaths:    cfg.AllowedReadPaths,
		ProviderAuth:        cfg.ProviderAuth,
		Workspace:           workspace,
		CmdTimeout:          cmdTimeout,
		ContextWindowTokens: cfg.ContextWindowTokens,
	}
}

// Package runconfig provides per-run configuration types.
//
// Extracted from the runner monolith to keep data types independent of
// run lifecycle logic.
package runconfig

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/sandbox"
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
	ThinkingLevel       string
	DebugPrompt         bool
	DebugRequest        bool
	DebugLLMDir         string
	Sandbox             sandbox.Config

	// Compaction controls automatic compression of old tool results
	// to stay within the context window.
	CompactionEnabled          bool
	CompactionThresholdPercent int // 0-100; high-water mark as % of context window
	CompactionLowWaterPercent  int // 0-100; stop compaction when below this % of context window
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
		ThinkingLevel:       cfg.ThinkingLevel,
		SystemPrompt:        sp,
		MaxTurns:            cfg.MaxTurns,
		MaxHistory:          cfg.MaxHistory,
		AllowedReadPaths:    cfg.AllowedReadPaths,
		ProviderAuth:        cfg.ProviderAuth,
		Workspace:           workspace,
		CmdTimeout:          cmdTimeout,
		ContextWindowTokens: cfg.ContextWindowTokens,
		DebugPrompt:         cfg.DebugPrompt,
		DebugRequest:        cfg.DebugRequest,
		DebugLLMDir:         cfg.DebugLLMDir,
		Sandbox:             cfg.Sandbox,
		CompactionEnabled:        cfg.CompactionEnabled,
		CompactionThresholdPercent: cfg.CompactionThresholdPercent,
		CompactionLowWaterPercent:  cfg.CompactionLowWaterPercent,
	}
}

// MaxTurnsExceededError reports that a run hit its configured turn cap.
type MaxTurnsExceededError struct {
	Limit int
}

func (e *MaxTurnsExceededError) Error() string {
	return fmt.Sprintf("max turns limit reached: %d", e.Limit)
}

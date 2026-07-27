// Package runner provides the run lifecycle seam.
//
// RunConfig is the per-run configuration type moved from the former
// internal/runner/runconfig sub-package (merged per issue #858).

package runner

import (
	"encoding/json"
	"time"

	"github.com/glemsom/eitri/internal/config"
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

	// BrowserWsUrl is the WebSocket URL of a remote Chrome DevTools Protocol
	// endpoint (e.g. "ws://127.0.0.1:9222/devtools/browser/...").
	// If empty, the browser tool returns a descriptive error.
	BrowserWsUrl string

	// ActivePersona is the name of the currently selected persona, if any.
	// An empty string means no persona selected (use default system prompt).
	ActivePersona string

	// Compaction controls automatic compression of old tool results
	// to stay within the context window.
	CompactionEnabled              bool
	CompactionThresholdPercent     int // 0-100; high-water mark as % of context window
	CompactionLowWaterPercent      int // 0-100; stop compaction when below this % of context window
	CompactionMessageSizeThreshold int // estimated-token threshold; messages below this are skipped
	CompactionToolCallRetentionTurns int // number of recent assistant messages whose ToolCall arguments are preserved
	CompactionSalienceEnabled       bool // use salience-scored ordering (default: true)
}

// FromConfig builds a RunConfig from a Config value object plus
// environment-specific workspace and command timeout.
// SystemPrompt is passed through as-is (may be empty). The caller
// or the system prompt builder applies persona resolution and defaults.
func FromConfig(cfg *config.Config, workspace string, cmdTimeout time.Duration) RunConfig {
	return RunConfig{
		ProviderID:          cfg.Provider,
		BaseURL:             cfg.BaseURL,
		APIKey:              cfg.APIKey,
		ModelName:           cfg.Model,
		ThinkingLevel:       cfg.ThinkingLevel,
		SystemPrompt:        cfg.SystemPrompt,
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
		BrowserWsUrl:        cfg.BrowserWsUrl,
		ActivePersona:       cfg.ActivePersona,
		CompactionEnabled:        cfg.CompactionEnabled,
		CompactionThresholdPercent: cfg.CompactionThresholdPercent,
		CompactionLowWaterPercent:  cfg.CompactionLowWaterPercent,
		CompactionMessageSizeThreshold: cfg.CompactionMessageSizeThreshold,
		CompactionToolCallRetentionTurns: cfg.CompactionToolCallRetentionTurns,
		CompactionSalienceEnabled:        cfg.CompactionSalienceEnabled,
	}
}



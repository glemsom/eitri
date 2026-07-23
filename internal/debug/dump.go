package debug

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"time"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/session"
)

// DumpOptions contains all data needed to write a crash dump.
// Callers assemble this from their available services — no hidden imports.
type DumpOptions struct {
	// Error string (backward-compatible single line).
	Error string `json:"error"`
	// ErrorChain is the full wrapped error chain via fmt.Sprintf("%+v", err).
	ErrorChain string `json:"error_chain,omitempty"`
	// Stack trace at crash moment.
	Stack string `json:"stack,omitempty"`
	// Application version.
	Version string `json:"version"`
	// Runtime summary: uptime, active run count, session count, trace count.
	RuntimeSummary *RuntimeSummary `json:"runtime_summary,omitempty"`
	// Sanitized config summary (secrets redacted).
	ConfigSummary map[string]interface{} `json:"config_summary,omitempty"`
	// All UI sessions (full message history).
	Sessions []*session.UISession `json:"sessions,omitempty"`
	// All completed + in-flight HTTP traces.
	Traces []*HTTPTrace `json:"traces,omitempty"`
	// In-flight HTTP traces.
	InFlightTraces []*HTTPTrace `json:"in_flight_traces,omitempty"`
	// Conversation context: last user message, last assistant message, turn number.
	ConversationContext *ConversationContext `json:"conversation_context,omitempty"`
	// Failing HTTP trace: most recent non-2xx response (never evicted by ring buffer).
	FailingHTTPTrace *HTTPTrace `json:"failing_http_trace,omitempty"`
	// Buffered structured log entries leading up to the crash.
	Logs []LogEntry `json:"logs,omitempty"`
}

// ConversationContext captures the conversation leading up to a crash.
type ConversationContext struct {
	LastUserMessage      string `json:"last_user_message,omitempty"`
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`
	TurnNumber           int    `json:"turn_number,omitempty"`
	TotalTokensUsed      int    `json:"total_tokens_used,omitempty"`
}

// RuntimeSummary captures lightweight runtime state for the crash dump.
type RuntimeSummary struct {
	UpSince           time.Time `json:"up_since"`
	ActiveRunCount    int       `json:"active_run_count"`
	SessionCount      int       `json:"session_count"`
	RecordedHTTPTraces int      `json:"recorded_http_traces"`
}

// crashDirName returns the crash directory name for the given timestamp.
// ISO8601 colons replaced with dashes for cross-platform filesystem compat.
func crashDirName(ts time.Time) string {
	name := ts.Format("crash-2006-01-02T15-04-05")
	return name
}

// SanitizeConfig returns a map of config fields with secrets replaced by "***".
// Uses the same pattern as the debug API's sanitizeConfig.
func SanitizeConfig(cfg *config.Config) map[string]interface{} {
	if cfg == nil {
		return nil
	}
	m := make(map[string]interface{})
	m["provider"] = cfg.Provider
	m["base_url"] = cfg.BaseURL
	m["model"] = cfg.Model
	m["thinking_level"] = cfg.ThinkingLevel
	m["context_window_tokens"] = cfg.ContextWindowTokens
	m["max_turns"] = cfg.MaxTurns
	m["max_history"] = cfg.MaxHistory
	m["command_timeout_ns"] = cfg.CommandTimeout
	m["session_timeout_ns"] = cfg.SessionTimeout

	if cfg.APIKey != "" {
		m["api_key"] = "***"
	}
	if len(cfg.ProviderAuth) > 0 {
		m["provider_auth"] = "***"
	}
	return m
}

// WriteCrashDump writes a structured crash dump to ~/.eitri/crash-dump/crash-<timestamp>/.
// Creates the directory and writes crash.json, sessions.json, traces.json, goroutines.txt,
// and logs.json (if any log entries were captured).
// Returns the path to the crash dump directory, or an error.
func WriteCrashDump(opts DumpOptions) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}

	baseDir := filepath.Join(home, ".eitri", "crash-dump")
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return "", fmt.Errorf("cannot create crash dump dir %s: %w", baseDir, err)
	}

	ts := time.Now()
	crashDir := filepath.Join(baseDir, crashDirName(ts))
	if err := os.MkdirAll(crashDir, 0700); err != nil {
		return "", fmt.Errorf("cannot create crash dir %s: %w", crashDir, err)
	}

	// 1. crash.json — error chain, version, sanitized config, runtime summary, conversation context, failing http trace
	crashInfo := struct {
		Error               string                 `json:"error"`
		ErrorChain          string                 `json:"error_chain,omitempty"`
		Stack               string                 `json:"stack,omitempty"`
		Version             string                 `json:"version"`
		Timestamp           time.Time              `json:"timestamp"`
		ConfigSummary       map[string]interface{} `json:"config_summary,omitempty"`
		RuntimeSummary      *RuntimeSummary        `json:"runtime_summary,omitempty"`
		ConversationContext *ConversationContext   `json:"conversation_context,omitempty"`
		FailingHTTPTrace    *HTTPTrace             `json:"failing_http_trace,omitempty"`
	}{
		Error:               opts.Error,
		ErrorChain:          opts.ErrorChain,
		Stack:               opts.Stack,
		Version:             opts.Version,
		Timestamp:           ts,
		ConfigSummary:       opts.ConfigSummary,
		RuntimeSummary:      opts.RuntimeSummary,
		ConversationContext: opts.ConversationContext,
		FailingHTTPTrace:    opts.FailingHTTPTrace,
	}

	if err := writeJSONFile(filepath.Join(crashDir, "crash.json"), crashInfo); err != nil {
		return "", fmt.Errorf("write crash.json: %w", err)
	}

	// 2. sessions.json — all UI sessions with full message history
	if opts.Sessions != nil {
		if err := writeJSONFile(filepath.Join(crashDir, "sessions.json"), opts.Sessions); err != nil {
			return "", fmt.Errorf("write sessions.json: %w", err)
		}
	}

	// 3. traces.json — all completed + in-flight HTTP traces
	tracesPayload := struct {
		Traces   []*HTTPTrace `json:"traces"`
		InFlight []*HTTPTrace `json:"in_flight"`
	}{
		Traces:   opts.Traces,
		InFlight: opts.InFlightTraces,
	}
	if err := writeJSONFile(filepath.Join(crashDir, "traces.json"), tracesPayload); err != nil {
		return "", fmt.Errorf("write traces.json: %w", err)
	}

	// 4. goroutines.txt — pprof goroutine dump
	goroPath := filepath.Join(crashDir, "goroutines.txt")
	f, err := os.Create(goroPath)
	if err != nil {
		return "", fmt.Errorf("create goroutines.txt: %w", err)
	}
	defer f.Close()

	if err := pprof.Lookup("goroutine").WriteTo(f, 2); err != nil {
		return "", fmt.Errorf("write goroutine dump: %w", err)
	}

	// 5. logs.json — buffered structured log entries
	if len(opts.Logs) > 0 {
		if err := writeJSONFile(filepath.Join(crashDir, "logs.json"), opts.Logs); err != nil {
			return "", fmt.Errorf("write logs.json: %w", err)
		}
	}

	return crashDir, nil
}

// writeJSONFile marshals v as indented JSON and writes it to path.
// Creates file with 0600 permissions.
func writeJSONFile(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0600)
}

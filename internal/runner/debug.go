package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glemsom/eitri/internal/llm"
)

// dumpRequestOnError writes the full chat request as JSON to the debug directory
// when EITRI_DEBUG_LLM_DIR is set and an LLM request fails.
func dumpRequestOnError(req *llm.Request, err error, attempt int) {
	dir := os.Getenv("EITRI_DEBUG_LLM_DIR")
	if dir == "" {
		return
	}
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		slog.Warn("cannot create LLM debug dir", slog.String("dir", dir), slog.Any("error", err))
		return
	}

	timestamp := time.Now().UnixNano()
	filename := fmt.Sprintf("runner-llm-request-%d-attempt-%d.json", timestamp, attempt)
	path := filepath.Join(dir, filename)

	type debugEntry struct {
		Request llm.Request `json:"request"`
		Error   string      `json:"error"`
		Attempt int         `json:"attempt"`
	}

	entry := debugEntry{
		Request: *req,
		Error:   err.Error(),
		Attempt: attempt,
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		slog.Warn("failed to marshal LLM request dump", slog.Any("error", err))
		return
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		slog.Warn("failed to write LLM request dump", slog.String("path", path), slog.Any("error", err))
		return
	}

	slog.Warn("LLM request dump written", slog.String("path", path), slog.Int("attempt", attempt))
}

// isRetryableLLMError checks if an LLM error is likely transient and worth retrying.
// Returns false for auth errors, context-length errors, rate-limits, and context cancellation.
func isRetryableLLMError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	msg := err.Error()
	// Don't retry auth errors
	if strings.Contains(msg, "401") || strings.Contains(msg, "Authentication") {
		return false
	}
	// Don't retry rate limits
	if strings.Contains(msg, "429") || strings.Contains(msg, "Rate limit") {
		return false
	}
	// Don't retry context length errors
	if strings.Contains(msg, "Context length") || strings.Contains(msg, "context length") {
		return false
	}
	// Don't retry bad request (400) — indicates a problem with the request itself
	// that won't resolve by re-sending (e.g. unknown model, invalid parameters).
	// Exception: some providers (e.g. OpenCode Go) return 400 for upstream
	// failures (e.g. "Upstream request failed"), which are transient and
	// should be retried.
	if strings.Contains(msg, "Bad Request") {
		return false
	}
	if strings.Contains(msg, "400") && !strings.Contains(msg, "Upstream") {
		return false
	}
	// Everything else (5xx, upstream failures, connection errors) is potentially transient
	return true
}

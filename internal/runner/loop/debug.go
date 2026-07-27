package loop

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/voocel/litellm"
)

// dumpRequestOnError writes the full chat request as JSON to the debug directory
// when debugLLMDir is non-empty and an LLM request fails.
func dumpRequestOnError(req *litellm.Request, err error, attempt int, debugLLMDir string) {
	if debugLLMDir == "" {
		return
	}
	if mkErr := os.MkdirAll(debugLLMDir, 0o755); mkErr != nil {
		slog.Warn("cannot create LLM debug dir", slog.String("dir", debugLLMDir), slog.Any("error", err))
		return
	}

	timestamp := time.Now().UnixNano()
	filename := fmt.Sprintf("runner-llm-request-%d-attempt-%d.json", timestamp, attempt)
	path := filepath.Join(debugLLMDir, filename)

	type debugEntry struct {
		Request litellm.Request `json:"request"`
		Error   string          `json:"error"`
		Attempt int             `json:"attempt"`
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

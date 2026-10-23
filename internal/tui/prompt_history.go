package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// PromptHistory records every real user prompt and `/skill ...` activation, but
// never control slash commands and never empty drafts. It lives on the Model
// rather than the transcript or session so a submitted prompt survives a `/new`.
type PromptHistory struct {
	cap     int
	items   []string
	persist string // full path of the backing file, or "" for an in-memory-only ring
}

func NewPromptHistory(capacity int) *PromptHistory {
	return &PromptHistory{cap: capacity}
}

// push reports whether the ring actually changed, i.e. whether the prompt is
// worth persisting.
func (h *PromptHistory) push(prompt string) bool {
	if strings.TrimSpace(prompt) == "" {
		return false
	}
	if n := len(h.items); n > 0 && h.items[n-1] == prompt {
		return false
	}
	h.items = append(h.items, prompt)
	if len(h.items) > h.cap {
		h.items = h.items[len(h.items)-h.cap:]
	}
	return true
}

// Push records a submitted prompt, ignoring empties and consecutive duplicates,
// and rewrites the backing file when the ring is persisted.
func (h *PromptHistory) Push(prompt string) {
	if !h.push(prompt) {
		return
	}
	if h.persist != "" {
		_ = savePromptHistory(h.persist, h.items)
	}
}

func (h *PromptHistory) Entries() []string {
	out := make([]string, len(h.items))
	copy(out, h.items)
	return out
}

func (h *PromptHistory) Len() int { return len(h.items) }

// restore seeds the ring with entries previously persisted, keeping only the most
// recent ones so an oversized file cannot grow the ring past its capacity.
func (h *PromptHistory) restore(entries []string) {
	if n := len(entries); n > h.cap {
		entries = entries[n-h.cap:]
	}
	h.items = entries
}

// NewPersistedPromptHistory builds a history ring of the given capacity backed by
// path. A missing or corrupt file falls back to an empty ring rather than error,
// since a lost history is not worth failing startup over.
func NewPersistedPromptHistory(capacity int, path string) *PromptHistory {
	h := &PromptHistory{cap: capacity, persist: path}
	if entries, err := loadPromptHistory(path); err == nil {
		h.restore(entries)
	}
	return h
}

func PromptHistoryPath(dataDir string) string {
	return filepath.Join(dataDir, "prompt_history.json")
}

// loadPromptHistory reads the persisted ring from path. A missing file yields an
// empty ring with no error; an unreadable or corrupt file yields an error.
func loadPromptHistory(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var entries []string
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func savePromptHistory(path string, entries []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

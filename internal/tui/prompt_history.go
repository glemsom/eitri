package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// records every real user prompt and `/skill ...` activation, but never control
// slash commands and never empty drafts, capped at a fixed depth with consecutive
// duplicates stored once. It lives on the Model rather than the transcript or
// session so a submitted prompt survives a `/new`.
type PromptHistory struct {
	cap     int
	items   []string
	persist string // full path of the backing file, or "" for an in-memory-only ring
}

// NewPromptHistory builds an empty history ring with the given capacity that is
// not persisted to disk.
func NewPromptHistory(capacity int) *PromptHistory {
	return &PromptHistory{cap: capacity}
}

// push is the internal record step. It reports whether the ring actually
// changed (true for a non-empty, non-consecutive-duplicate prompt worth
// persisting).
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

// Push records a submitted prompt onto the ring. Empty prompts are ignored,
// and a prompt equal to the most recent entry (a consecutive duplicate) is not
// stored twice. When the ring is full, the oldest entry is dropped. When the
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

// restore seeds the ring with entries previously persisted, truncating to the
// ring capacity so an oversized or corrupt file cannot grow the ring; only the
// most recent entries (up to capacity) are kept.
func (h *PromptHistory) restore(entries []string) {
	if n := len(entries); n > h.cap {
		entries = entries[n-h.cap:]
	}
	h.items = entries
}

// NewPersistedPromptHistory builds a history ring with the given capacity backed
// entries are restored at construction, and every successful Push rewrites the
// file. A missing or corrupt file falls back to an empty ring rather than error.
func NewPersistedPromptHistory(capacity int, path string) *PromptHistory {
	h := &PromptHistory{cap: capacity, persist: path}
	if entries, err := loadPromptHistory(path); err == nil {
		h.restore(entries)
	}
	return h
}

// PromptHistoryPath returns the on-disk JSON file for persisted submitted
// prompts. It lives as a sibling of config.json in the data directory (issue
func PromptHistoryPath(dataDir string) string {
	return filepath.Join(dataDir, "prompt_history.json")
}

// loadPromptHistory reads the persisted ring from path. A missing file yields an
// empty ring with no error; an unreadable or corrupt file yields an error so the
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

// savePromptHistory writes entries to path as JSON, creating the parent data
// directory if needed. The ring always holds strings, so the file round-trips
// through a JSON list of strings.
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

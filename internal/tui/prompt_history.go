package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// PromptHistory is the Model-owned ring of submitted user prompts (issue #610,
// part of #608). It is the data source the arrow-key recall reads from: it
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

// push is the internal record step. It returns the new item when the ring
// actually changed (a prompt worth persisting), else nil.
func (h *PromptHistory) push(prompt string) *string {
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	if n := len(h.items); n > 0 && h.items[n-1] == prompt {
		return nil
	}
	h.items = append(h.items, prompt)
	if len(h.items) > h.cap {
		h.items = h.items[len(h.items)-h.cap:]
	}
	return &prompt
}

// Push records a submitted prompt onto the ring. Empty prompts are ignored,
// and a prompt equal to the most recent entry (a consecutive duplicate) is not
// stored twice. When the ring is full, the oldest entry is dropped. When the
// ring is backed by a file, the whole ring is saved on change (issue #612,
// part of #608); a failed save is ignored so prompt history never blocks a turn.
func (h *PromptHistory) Push(prompt string) {
	if h.push(prompt) == nil {
		return
	}
	if h.persist != "" {
		_ = savePromptHistory(h.persist, h.items)
	}
}

// Entries returns a copy of the recorded prompts in submission order.
func (h *PromptHistory) Entries() []string {
	out := make([]string, len(h.items))
	copy(out, h.items)
	return out
}

// Len reports how many prompts the ring currently holds.
func (h *PromptHistory) Len() int { return len(h.items) }

// restore seeds the ring with entries previously persisted, truncating to the
// devicewide capacity so an oversized or corrupt file cannot grow the ring.
// entries beyond the capacity (most recent retained) are kept.
func (h *PromptHistory) restore(entries []string) {
	if n := len(entries); n > h.cap {
		entries = entries[n-h.cap:]
	}
	h.items = entries
}

// NewPersistedPromptHistory builds a history ring with the given capacity backed
// by the file at path (issue #612, part of #608): any previously persisted
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
// #612, part of #608), so it survives a `/new` and a program restart.
func PromptHistoryPath(dataDir string) string {
	return filepath.Join(dataDir, "prompt_history.json")
}

// loadPromptHistory reads the persisted ring from path. A missing file yields an
// empty ring with no error; an unreadable or corrupt file yields an error so the
// TUI can fall back to an empty ring (issue #612).
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

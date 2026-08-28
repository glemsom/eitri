package tui

import "strings"

// PromptHistory is the Model-owned in-memory ring of submitted user prompts
// (issue #610, part of #608). It is the data source the arrow-key recall reads
// from: it records every real user prompt and `/skill ...` activation, but
// never control slash commands and never empty drafts, capped at a fixed depth
// with consecutive duplicates stored once. It lives on the Model rather than
// the transcript or session so a submitted prompt survives a `/new`.
type PromptHistory struct {
	cap   int
	items []string
}

// NewPromptHistory builds an empty history ring with the given capacity.
func NewPromptHistory(capacity int) *PromptHistory {
	return &PromptHistory{cap: capacity}
}

// Push records a submitted prompt onto the ring. Empty prompts are ignored,
// and a prompt equal to the most recent entry (a consecutive duplicate) is not
// stored twice. When the ring is full, the oldest entry is dropped.
func (h *PromptHistory) Push(prompt string) {
	if strings.TrimSpace(prompt) == "" {
		return
	}
	if n := len(h.items); n > 0 && h.items[n-1] == prompt {
		return
	}
	h.items = append(h.items, prompt)
	if len(h.items) > h.cap {
		h.items = h.items[len(h.items)-h.cap:]
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

package app

import (
	"context"
	"sync"

	"github.com/glemsom/eitri/internal/provider"
)

// hotProvider is a mutable provider handle shared by the engine and TUI: the current concrete provider can be swapped after an in-TUI config mutation (Settings save, Copilot login) without rebuilding the whole session.
type hotProvider struct {
	mu sync.RWMutex
	p  provider.Provider
}

func newHotProvider(p provider.Provider) *hotProvider { return &hotProvider{p: p} }

// Set swaps the active provider used for subsequent calls.
func (h *hotProvider) Set(p provider.Provider) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.p = p
}

func (h *hotProvider) current() provider.Provider {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.p
}

func (h *hotProvider) Stream(ctx context.Context, req provider.Request) (provider.Stream, error) {
	return h.current().Stream(ctx, req)
}

func (h *hotProvider) Models(ctx context.Context) ([]provider.ModelInfo, error) {
	l, ok := h.current().(provider.ModelLister)
	if !ok {
		return nil, provider.ErrNoDiscovery
	}
	return l.Models(ctx)
}

func (h *hotProvider) SupportedGenerationControls(ctx context.Context) ([]provider.GenerationControl, error) {
	gp, ok := h.current().(provider.GenerationControlProvider)
	if !ok {
		return nil, nil
	}
	return gp.SupportedGenerationControls(ctx)
}

// Package tokenizer provides token estimation and calibration utilities.
package tokenizer

import (
	"math"
	"sync"
)

// DefaultCPT is the default chars-per-token ratio used when no calibration
// data exists for a model.
const DefaultCPT = 4.0

// EMAAlpha is the smoothing factor for the exponential moving average.
// Higher values give more weight to recent observations.
const EMAAlpha = 0.3

// CalibrationStore tracks per-model chars-per-token (CPT) averages using
// exponential moving average. Thread-safe.
type CalibrationStore struct {
	mu    sync.Mutex
	store map[string]*modelEntry
}

// modelEntry holds the smoothed CPT value for a single model.
type modelEntry struct {
	sync.Mutex
	cpt float64
}

// NewCalibrationStore creates a new store initialized with default CPT (4.0)
// for all models. Models are lazily initialized on first access.
func NewCalibrationStore() *CalibrationStore {
	return &CalibrationStore{
		store: make(map[string]*modelEntry),
	}
}

// getOrCreateEntry returns the entry for a model, creating it with the
// default CPT if it does not exist.
func (cs *CalibrationStore) getOrCreateEntry(model string) *modelEntry {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	entry, ok := cs.store[model]
	if !ok {
		entry = &modelEntry{cpt: DefaultCPT}
		cs.store[model] = entry
	}
	return entry
}

// Update records a new chars-per-token observation for the given model
// and updates the smoothed average using exponential moving average:
//
//	newCPT = α * observed + (1-α) * oldCPT
//
// where α = EMAAlpha (0.3).
func (cs *CalibrationStore) Update(model string, actualCPT float64) {
	if actualCPT <= 0 {
		return
	}

	entry := cs.getOrCreateEntry(model)
	entry.Lock()
	defer entry.Unlock()

	entry.cpt = EMAAlpha*actualCPT + (1-EMAAlpha)*entry.cpt
}

// Lookup returns the smoothed chars-per-token ratio for the given model.
// Returns DefaultCPT (4.0) if no calibration data exists.
func (cs *CalibrationStore) Lookup(model string) float64 {
	cs.mu.Lock()
	entry, ok := cs.store[model]
	cs.mu.Unlock()

	if !ok {
		return DefaultCPT
	}

	entry.Lock()
	cpt := entry.cpt
	entry.Unlock()

	// Round to 2 decimal places for stable estimates
	return math.Round(cpt*100) / 100
}

// Reset clears the calibration data for a model, so subsequent Lookup calls
// return the default CPT of 4.0.
func (cs *CalibrationStore) Reset(model string) {
	cs.mu.Lock()
	delete(cs.store, model)
	cs.mu.Unlock()
}

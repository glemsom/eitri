// Package tokenizer provides token estimation and calibration utilities.
package tokenizer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
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

// Count returns the number of models that currently have calibration entries.
func (cs *CalibrationStore) Count() int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return len(cs.store)
}

// Snapshot returns a copy of the current per-model CPT values keyed by model
// name. Mutating the returned map does not affect the store.
func (cs *CalibrationStore) Snapshot() map[string]float64 {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	out := make(map[string]float64, len(cs.store))
	for model, entry := range cs.store {
		entry.Lock()
		out[model] = entry.cpt
		entry.Unlock()
	}
	return out
}

// Restore replaces the store's calibration data with the given per-model CPT
// values. Models absent from the data fall back to DefaultCPT on next access.
func (cs *CalibrationStore) Restore(data map[string]float64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.store = make(map[string]*modelEntry, len(data))
	for model, cpt := range data {
		cs.store[model] = &modelEntry{cpt: cpt}
	}
}

// Save persists the store's calibration data to path as JSON using an atomic
// write (temp file + rename), so a crash mid-write never leaves a corrupt
// calibration file behind.
func (cs *CalibrationStore) Save(path string) error {
	data, err := json.MarshalIndent(cs.Snapshot(), "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "calibration-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = ""
	return nil
}

// Load reads calibration data from path into the store, replacing any current
// entries. An absent or empty file is treated as a no-op so the store keeps
// its defaults. A corrupt file returns an error and leaves the store unchanged.
func (cs *CalibrationStore) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}

	var snapshot map[string]float64
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("parse calibration data %s: %w", path, err)
	}
	cs.Restore(snapshot)
	return nil
}

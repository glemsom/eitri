package tokenizer

import (
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNewCalibrationStore_DefaultLookup(t *testing.T) {
	t.Parallel()

	cs := NewCalibrationStore()
	if cs == nil {
		t.Fatal("NewCalibrationStore returned nil")
	}

	cpt := cs.Lookup("gpt-4")
	if cpt != DefaultCPT {
		t.Errorf("Lookup() = %f, want %f", cpt, DefaultCPT)
	}
}

func TestLookup_DefaultForUnknownModel(t *testing.T) {
	t.Parallel()

	cs := NewCalibrationStore()
	cpt := cs.Lookup("unknown-model")
	if cpt != DefaultCPT {
		t.Errorf("Lookup() = %f, want %f", cpt, DefaultCPT)
	}
}

func TestUpdate_AndLookup(t *testing.T) {
	t.Parallel()

	cs := NewCalibrationStore()
	model := "gpt-4"

	// First update: only observed value matters (starting from DefaultCPT)
	cs.Update(model, 5.0)
	cpt := cs.Lookup(model)
	expected := EMAAlpha*5.0 + (1-EMAAlpha)*DefaultCPT
	expected = math.Round(expected*100) / 100
	if cpt != expected {
		t.Errorf("After first update: Lookup() = %f, want %f", cpt, expected)
	}

	// Second update: smooth further
	cs.Update(model, 3.0)
	cpt = cs.Lookup(model)
	expected = EMAAlpha*3.0 + (1-EMAAlpha)*expected
	expected = math.Round(expected*100) / 100
	if cpt != expected {
		t.Errorf("After second update: Lookup() = %f, want %f", cpt, expected)
	}
}

func TestUpdate_IgnoresNonPositiveValues(t *testing.T) {
	t.Parallel()

	cs := NewCalibrationStore()
	model := "gpt-4"

	cs.Update(model, 0)
	cpt := cs.Lookup(model)
	if cpt != DefaultCPT {
		t.Errorf("Lookup() after Update(0) = %f, want %f", cpt, DefaultCPT)
	}

	cs.Update(model, -1)
	cpt = cs.Lookup(model)
	if cpt != DefaultCPT {
		t.Errorf("Lookup() after Update(-1) = %f, want %f", cpt, DefaultCPT)
	}
}

func TestReset(t *testing.T) {
	t.Parallel()

	cs := NewCalibrationStore()
	model := "gpt-4"

	cs.Update(model, 8.0)
	if cs.Lookup(model) == DefaultCPT {
		t.Fatal("Lookup() should differ from default after update")
	}

	cs.Reset(model)
	cpt := cs.Lookup(model)
	if cpt != DefaultCPT {
		t.Errorf("After Reset: Lookup() = %f, want %f", cpt, DefaultCPT)
	}
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()

	cs := NewCalibrationStore()
	models := []string{"gpt-4", "gpt-3.5-turbo", "claude-3", "llama-3"}
	var wg sync.WaitGroup

	// Concurrent updates
	for _, m := range models {
		wg.Add(1)
		go func(model string) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				cs.Update(model, float64(i%10)+1)
			}
		}(m)
	}

	// Concurrent lookups
	for _, m := range models {
		wg.Add(1)
		go func(model string) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = cs.Lookup(model)
			}
		}(m)
	}

	// Concurrent resets
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			cs.Reset(models[i%len(models)])
		}
	}()

	wg.Wait()

	// Should not panic or deadlock
	for _, m := range models {
		cpt := cs.Lookup(m)
		if cpt <= 0 {
			t.Errorf("Lookup(%q) = %f, want > 0", m, cpt)
		}
	}
}

func TestDifferentModels_Independent(t *testing.T) {
	t.Parallel()

	cs := NewCalibrationStore()

	cs.Update("model-a", 2.0)
	cs.Update("model-b", 8.0)

	cptA := cs.Lookup("model-a")
	cptB := cs.Lookup("model-b")

	if cptA >= cptB {
		t.Errorf("model-a CPT (%f) should be less than model-b CPT (%f)", cptA, cptB)
	}

	// model-c should still return default
	cptC := cs.Lookup("model-c")
	if cptC != DefaultCPT {
		t.Errorf("model-c Lookup() = %f, want %f", cptC, DefaultCPT)
	}
}

func TestEMASmoothingConvergence(t *testing.T) {
	t.Parallel()

	cs := NewCalibrationStore()
	model := "stable-model"

	// Feed many observations of the same value; the EMA should converge towards it.
	const trueCPT = 6.0
	for i := 0; i < 50; i++ {
		cs.Update(model, trueCPT)
	}

	cpt := cs.Lookup(model)
	if math.Abs(cpt-trueCPT) > 0.1 {
		t.Errorf("EMA did not converge: Lookup() = %f, want ≈%f", cpt, trueCPT)
	}
}

func TestSnapshotRestore(t *testing.T) {
	t.Parallel()

	cs := NewCalibrationStore()
	cs.Update("model-a", 5.0)
	cs.Update("model-b", 2.5)
	wantA := cs.Lookup("model-a")
	wantB := cs.Lookup("model-b")

	restored := NewCalibrationStore()
	restored.Restore(cs.Snapshot())

	if got := restored.Lookup("model-a"); got != wantA {
		t.Errorf("restored model-a Lookup() = %f, want %f", got, wantA)
	}
	if got := restored.Lookup("model-b"); got != wantB {
		t.Errorf("restored model-b Lookup() = %f, want %f", got, wantB)
	}
	// Models absent from the snapshot fall back to the default.
	if got := restored.Lookup("model-c"); got != DefaultCPT {
		t.Errorf("restored model-c Lookup() = %f, want %f", got, DefaultCPT)
	}
	// The original store is unaffected by the caller mutating the snapshot.
	snap := cs.Snapshot()
	delete(snap, "model-a")
	if got := cs.Lookup("model-a"); got != wantA {
		t.Errorf("original store mutated by snapshot edit: Lookup() = %f, want %f", got, wantA)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	t.Parallel()

	cs := NewCalibrationStore()
	cs.Update("model-a", 5.0)
	cs.Update("model-b", 2.5)
	wantA := cs.Lookup("model-a")
	wantB := cs.Lookup("model-b")

	path := filepath.Join(t.TempDir(), "calibration.json")
	if err := cs.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded := NewCalibrationStore()
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := loaded.Lookup("model-a"); got != wantA {
		t.Errorf("loaded model-a Lookup() = %f, want %f", got, wantA)
	}
	if got := loaded.Lookup("model-b"); got != wantB {
		t.Errorf("loaded model-b Lookup() = %f, want %f", got, wantB)
	}
	// Models absent from the file fall back to the default.
	if got := loaded.Lookup("model-c"); got != DefaultCPT {
		t.Errorf("loaded model-c Lookup() = %f, want %f", got, DefaultCPT)
	}
}

func TestLoad_AbsentFileFallsBackToDefaults(t *testing.T) {
	t.Parallel()

	loaded := NewCalibrationStore()
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load(absent) should be a no-op, got error: %v", err)
	}
	if got := loaded.Lookup("any-model"); got != DefaultCPT {
		t.Errorf("Lookup() = %f, want %f", got, DefaultCPT)
	}
}

func TestLoad_EmptyFileFallsBackToDefaults(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "calibration.json")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loaded := NewCalibrationStore()
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load(empty) should be a no-op, got error: %v", err)
	}
	if got := loaded.Lookup("any-model"); got != DefaultCPT {
		t.Errorf("Lookup() = %f, want %f", got, DefaultCPT)
	}
}

func TestLoad_CorruptFileReturnsError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "calibration.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loaded := NewCalibrationStore()
	if err := loaded.Load(path); err == nil {
		t.Fatal("Load(corrupt) should return an error")
	}
	// The store must be unaffected by a failed load.
	if got := loaded.Lookup("any-model"); got != DefaultCPT {
		t.Errorf("Lookup() = %f, want %f", got, DefaultCPT)
	}
}

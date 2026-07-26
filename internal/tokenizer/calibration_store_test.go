package tokenizer

import (
	"math"
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

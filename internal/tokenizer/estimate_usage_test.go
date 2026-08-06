package tokenizer

import "testing"

// ---------------------------------------------------------------------------
// EstimateUsage
// ---------------------------------------------------------------------------

func TestEstimateUsage_EmptyText(t *testing.T) {
	t.Parallel()

	result := EstimateUsage("", nil, "")
	if result == nil {
		t.Fatal("EstimateUsage returned nil")
	}
	// For empty text: len=0 -> 0/4=0, clamped to 1 total, 1/3=0 clamped to 1 completion, prompt=0
	if result.TotalTokens != 1 {
		t.Errorf("TotalTokens = %d, want 1", result.TotalTokens)
	}
	if result.PromptTokens != 0 {
		t.Errorf("PromptTokens = %d, want 0", result.PromptTokens)
	}
	if result.CompletionTokens != 1 {
		t.Errorf("CompletionTokens = %d, want 1", result.CompletionTokens)
	}
}

func TestEstimateUsage_ShortText(t *testing.T) {
	t.Parallel()

	result := EstimateUsage("Hello", nil, "")
	if result == nil {
		t.Fatal("EstimateUsage returned nil")
	}
	if result.TotalTokens != 1 {
		t.Errorf("TotalTokens = %d, want 1 (len=5 -> 5/4=1)", result.TotalTokens)
	}
}

func TestEstimateUsage_LongText(t *testing.T) {
	t.Parallel()

	// 400 chars -> ~100 tokens
	text := make([]byte, 400)
	for i := range text {
		text[i] = 'a'
	}
	result := EstimateUsage(string(text), nil, "")
	if result == nil {
		t.Fatal("EstimateUsage returned nil")
	}
	if result.TotalTokens != 100 {
		t.Errorf("TotalTokens = %d, want 100 (400 chars / 4)", result.TotalTokens)
	}
	// Prompt: ~2/3 of total, completion: ~1/3
	if result.TotalTokens != result.PromptTokens+result.CompletionTokens {
		t.Errorf("TotalTokens(%d) != PromptTokens(%d) + CompletionTokens(%d)",
			result.TotalTokens, result.PromptTokens, result.CompletionTokens)
	}
}

func TestEstimateUsage_TokenBreakdown(t *testing.T) {
	t.Parallel()

	// 4000 chars -> ~1000 tokens
	text := make([]byte, 4000)
	for i := range text {
		text[i] = 'a'
	}
	result := EstimateUsage(string(text), nil, "")
	if result.TotalTokens != 1000 {
		t.Errorf("TotalTokens = %d, want 1000", result.TotalTokens)
	}
	// completion = total / 3 = 333 (rounded), prompt = total - completion = 667
	expectedCompletion := 1000 / 3 // 333
	if result.CompletionTokens != expectedCompletion {
		t.Errorf("CompletionTokens = %d, want %d", result.CompletionTokens, expectedCompletion)
	}
	if result.PromptTokens != 1000-expectedCompletion {
		t.Errorf("PromptTokens = %d, want %d", result.PromptTokens, 1000-expectedCompletion)
	}
}

func TestEstimateUsage_CalibratedStore(t *testing.T) {
	t.Parallel()

	store := NewCalibrationStore()
	model := "calibrated-model"
	// Feed calibration data that should produce CPT=5.0
	// With EMA α=0.3: starts at 4.0, one observation at 5.0 → 0.3*5 + 0.7*4 = 4.3
	store.Update(model, 5.0)
	expectedCPT := store.Lookup(model) // should be 4.3

	// 430 chars -> 430/4.3 = 100 tokens
	text := make([]byte, 430)
	for i := range text {
		text[i] = 'a'
	}

	result := EstimateUsage(string(text), store, model)
	if result == nil {
		t.Fatal("EstimateUsage returned nil")
	}
	if result.TotalTokens != 100 {
		t.Errorf("TotalTokens = %d, want 100 (430 chars / CPT %.2f)", result.TotalTokens, expectedCPT)
	}
	if result.TotalTokens != result.PromptTokens+result.CompletionTokens {
		t.Errorf("TotalTokens(%d) != PromptTokens(%d) + CompletionTokens(%d)",
			result.TotalTokens, result.PromptTokens, result.CompletionTokens)
	}
}

func TestEstimateUsage_CalibratedStoreDifferentModel(t *testing.T) {
	t.Parallel()

	store := NewCalibrationStore()
	// Calibrate model A with different CPT
	store.Update("model-a", 5.0)
	// model-b should still use default 4.0

	text := make([]byte, 400)
	for i := range text {
		text[i] = 'a'
	}

	// model-a uses calibrated CPT (~4.3), so 400/4.3 = 93 tokens
	resultA := EstimateUsage(string(text), store, "model-a")
	// model-b uses default CPT 4.0, so 400/4.0 = 100 tokens
	resultB := EstimateUsage(string(text), store, "model-b")

	if resultA.TotalTokens == resultB.TotalTokens {
		t.Errorf("Calibrated model-a (%d) should differ from uncalibrated model-b (%d)",
			resultA.TotalTokens, resultB.TotalTokens)
	}
}

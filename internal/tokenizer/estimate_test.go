package tokenizer

import "testing"

func TestEstimate_EquivalenceTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"a", 1},
		{"hello", 1},               // 5 chars / 4 = 1
		{"hello world", 2},         // 11 chars / 4 = 2
		{"a b c d e f g h i j", 4}, // 19 chars / 4 = 4
		{"1234567890", 2},          // 10/4=2
	}
	for _, tt := range tests {
		got := Estimate(tt.input, nil, "")
		if got != tt.want {
			t.Errorf("Estimate(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestEstimate_UsesCalibratedCPT(t *testing.T) {
	t.Parallel()

	store := NewCalibrationStore()
	// Converge the calibrated chars-per-token ratio toward 8.0 (default is 4.0).
	for i := 0; i < 50; i++ {
		store.Update("calibrated-model", 8.0)
	}
	if cpt := store.Lookup("calibrated-model"); cpt < 7.9 {
		t.Fatalf("calibrated-model CPT = %f, want ≈8.0", cpt)
	}

	text := "01234567890123456789012345678901234567890123456789" // 50 chars
	if got := Estimate(text, store, "calibrated-model"); got != 6 {
		t.Errorf("Estimate with CPT 8.0 = %d, want 6 (50 chars / 8)", got)
	}
	if got := Estimate(text, nil, ""); got != 12 {
		t.Errorf("Estimate with default CPT 4.0 = %d, want 12 (50 chars / 4)", got)
	}
}

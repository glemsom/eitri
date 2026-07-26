package provider_test

import (
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

func TestContextWindowForModel_ExactMatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		model string
		want  int
	}{
		{"gpt-4o", 128_000},
		{"gpt-4o-mini", 128_000},
		{"gpt-4o-turbo", 128_000},
		{"gpt-5", 200_000},
		{"o1", 200_000},
		{"o1-mini", 128_000},
		{"o1-preview", 128_000},
		{"o3-mini", 200_000},
		{"deepseek-chat", 128_000},
		{"deepseek-reasoner", 128_000},
		{"deepseek", 128_000},
		{"gemini-2.0-flash", 1_048_576},
		{"gemini-2.5-pro", 1_048_576},
		{"gemini-3", 1_048_576},
		{"claude-3.5-sonnet", 200_000},
		{"claude-3.5-haiku", 200_000},
		{"claude-3-sonnet", 200_000},
		{"claude-3-haiku", 200_000},
		{"claude-4", 200_000},
		{"qwen-max", 131_072},
		{"qwen-plus", 131_072},
		{"qwen-turbo", 131_072},
		{"minimax", 131_072},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			got := provider.ContextWindowForModel(tc.model)
			if got != tc.want {
				t.Errorf("ContextWindowForModel(%q) = %d, want %d", tc.model, got, tc.want)
			}
		})
	}
}

func TestContextWindowForModel_PrefixMatch(t *testing.T) {
	t.Parallel()

	// These model names are not exact keys but should match a prefix.
	cases := []struct {
		model string
		want  int
	}{
		// GPT-4o prefix covers all 4o variants
		{"gpt-4o-some-new-variant", 128_000},
		// o1 prefix covers unknown o1 variants (but not o1-mini which is more specific)
		{"o1-some-new-variant", 200_000},
		// deepseek catch-all covers unknown deepseek variants
		{"deepseek-r1", 128_000},
		{"deepseek-v3", 128_000},
		// gemini-2.5 prefix covers all 2.5 variants
		{"gemini-2.5-flash-lite", 1_048_576},
		// gemini-2.0 prefix covers all 2.0 variants
		{"gemini-2.0-flash-lite", 1_048_576},
		// gemini-3 prefix covers future variants
		{"gemini-3-ultra", 1_048_576},
		// claude-3.5 prefix covers 3.5 variants
		{"claude-3.5-opus", 200_000},
		// claude-4 prefix covers 4 variants
		{"claude-4-sonnet", 200_000},
		// qwen catch-all covers unknown qwen variants
		{"qwen-72b", 131_072},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			got := provider.ContextWindowForModel(tc.model)
			if got != tc.want {
				t.Errorf("ContextWindowForModel(%q) = %d, want %d", tc.model, got, tc.want)
			}
		})
	}
}

func TestContextWindowForModel_LongestPrefixWins(t *testing.T) {
	t.Parallel()

	// "o1" → 200K is a prefix of "o1-mini", but "o1-mini" → 128K is longer,
	// so the more specific entry wins.
	if got := provider.ContextWindowForModel("o1-mini"); got != 128_000 {
		t.Errorf("ContextWindowForModel(%q) = %d, want %d", "o1-mini", got, 128_000)
	}

	// "o1" → 200K is a prefix of "o1-preview", but "o1-preview" → 128K is longer.
	if got := provider.ContextWindowForModel("o1-preview"); got != 128_000 {
		t.Errorf("ContextWindowForModel(%q) = %d, want %d", "o1-preview", got, 128_000)
	}

	// "deepseek" → 128K is a prefix of "deepseek-chat", but "deepseek-chat" → 128K is longer.
	if got := provider.ContextWindowForModel("deepseek-chat"); got != 128_000 {
		t.Errorf("ContextWindowForModel(%q) = %d, want %d", "deepseek-chat", got, 128_000)
	}

	// "qwen" → 131K is a prefix of "qwen-max", but "qwen-max" → 131K is longer.
	if got := provider.ContextWindowForModel("qwen-max"); got != 131_072 {
		t.Errorf("ContextWindowForModel(%q) = %d, want %d", "qwen-max", got, 131_072)
	}
}

func TestContextWindowForModel_NoMatch(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"unknown-model",
		"gpt-3.5-turbo",   // not in table
		"llama-3",          // not in table
		"command-r",        // not in table
		"dall-e-3",         // not in table
		"gpt-4",            // "gpt-4o" is not a prefix of "gpt-4" (needs the "o")
		"gpt-4-turbo",      // "gpt-4o" is not a prefix of "gpt-4-turbo"
	}
	for _, model := range cases {
		t.Run(model, func(t *testing.T) {
			got := provider.ContextWindowForModel(model)
			if got != 0 {
				t.Errorf("ContextWindowForModel(%q) = %d, want 0", model, got)
			}
		})
	}
}

func TestContextWindowForModel_EmptyString(t *testing.T) {
	t.Parallel()

	if got := provider.ContextWindowForModel(""); got != 0 {
		t.Errorf("ContextWindowForModel(%q) = %d, want 0", "", got)
	}
}

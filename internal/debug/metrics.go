package debug

import (
	"net/http"
	"strings"
	"time"
)

// ErrorClass is a structured, capture-time classification of an LLM call
// failure. The class is derived once, when the trace is recorded, from the
// HTTP status code and the error message present at that moment. Metrics
// endpoints read the stored class — they never re-derive classification by
// scanning error text at display time.
type ErrorClass string

const (
	// ErrorClassRateLimit covers HTTP 429 and provider "overloaded" (529)
	// responses: the request was understood but capacity/cost throttling
	// refused it.
	ErrorClassRateLimit ErrorClass = "rate_limit"
	// ErrorClassTimeout covers client-side deadline exceeded, HTTP 408 and
	// HTTP 504: the call did not complete in the allotted time.
	ErrorClassTimeout ErrorClass = "timeout"
	// ErrorClassAuth covers HTTP 401/403: missing, invalid, or forbidden
	// credentials.
	ErrorClassAuth ErrorClass = "auth"
	// ErrorClassContextLength covers requests that exceeded the model's
	// context window (prompt too long, context window full). Providers report
	// this with varying statuses (400/413/422) and message wording, so it is
	// detected from the error body at capture time.
	ErrorClassContextLength ErrorClass = "context_length"
	// ErrorClassNetwork covers transport-level failures (connection refused,
	// DNS failure, TLS errors) where no HTTP response was received.
	ErrorClassNetwork ErrorClass = "network"
	// ErrorClassOther is the fallback for every other non-2xx outcome.
	ErrorClassOther ErrorClass = "other"
)

// allErrorClasses is the ordered list of error classes, used for stable
// iteration and rendering in metrics snapshots.
var allErrorClasses = []ErrorClass{
	ErrorClassRateLimit,
	ErrorClassTimeout,
	ErrorClassAuth,
	ErrorClassContextLength,
	ErrorClassNetwork,
	ErrorClassOther,
}

// contextLengthTokens are stable markers providers use for context-window
// overflows. They are matched case-insensitively against the error message at
// capture time; messages that match none fall back to status-based
// classification.
var contextLengthTokens = []string{
	"maximum context length",
	"context length",
	"context_length_exceeded",
	"context window",
	"context_window",
	"max context",
	"too many tokens",
	"token limit",
	"prompt is too long",
	"input is too long",
	"maximum input tokens",
	"max_input_tokens",
	"maximum tokens",
	"reduce prompt",
}

// ClassifyError maps an HTTP status code plus the error message observed at
// capture time to a structured ErrorClass.
//
// Status codes win when they are unambiguous (429/529 → rate_limit, 401/403 →
// auth, 408/504 → timeout). Context-length overflows are signalled by
// providers with varied statuses, so the message is consulted when the status
// does not decide. A zero status with a non-empty message is a transport
// failure (network, or timeout when the message indicates a deadline).
func ClassifyError(status int, message string) ErrorClass {
	switch status {
	case http.StatusTooManyRequests, 529:
		return ErrorClassRateLimit
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrorClassAuth
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return ErrorClassTimeout
	}

	if isContextLengthMessage(message) {
		return ErrorClassContextLength
	}

	if status == 0 && message != "" {
		if isTimeoutMessage(message) {
			return ErrorClassTimeout
		}
		return ErrorClassNetwork
	}

	return ErrorClassOther
}

func isContextLengthMessage(message string) bool {
	lower := strings.ToLower(message)
	for _, token := range contextLengthTokens {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func isTimeoutMessage(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "client.timeout") ||
		strings.Contains(lower, "request canceled while waiting")
}

// latencyBucket defines one latency histogram bucket: count of calls with
// duration less than or equal to UpperMs. The final bucket (le +inf) has no
// upper bound and is represented with UpperMs = 0.
type latencyBucket struct {
	Label string
	Upper int64 // ms; 0 means +inf
}

// latencyBuckets are the histogram buckets for call durations in milliseconds.
var latencyBuckets = []latencyBucket{
	{Label: "le_100", Upper: 100},
	{Label: "le_250", Upper: 250},
	{Label: "le_500", Upper: 500},
	{Label: "le_1000", Upper: 1000},
	{Label: "le_2500", Upper: 2500},
	{Label: "le_5000", Upper: 5000},
	{Label: "le_10000", Upper: 10000},
	{Label: "le_30000", Upper: 30000},
	{Label: "inf", Upper: 0},
}

// UsageTotals captures the provider-reported token usage for one LLM call.
// Fields that the provider did not report remain zero.
type UsageTotals struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
	TotalTokens      int `json:"total_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
}

// HasTokens reports whether any token count was reported.
func (u *UsageTotals) HasTokens() bool {
	return u != nil &&
		(u.PromptTokens > 0 || u.CompletionTokens > 0 || u.CacheReadTokens > 0 ||
			u.CacheWriteTokens > 0 || u.TotalTokens > 0)
}

// providerModelKey identifies the per-provider-per-model aggregate.
type providerModelKey struct {
	ProviderID string
	Model      string
}

// providerModelMetrics is the running aggregate for one provider+model pair.
type providerModelMetrics struct {
	calls            int
	retries          int
	errors           map[ErrorClass]int
	latency          []int64 // parallel to latencyBuckets
	promptTokens     int
	completionTokens int
	cacheReadTokens  int
	cacheWriteTokens int
	cacheHits        int
	cacheMisses      int
	lastCalled       time.Time
	lastErrorClass   ErrorClass
}

func newProviderModelMetrics() *providerModelMetrics {
	return &providerModelMetrics{
		errors:  make(map[ErrorClass]int),
		latency: make([]int64, len(latencyBuckets)),
	}
}

// MetricsSnapshot is the JSON shape returned by GET /api/debug/metrics.
type MetricsSnapshot struct {
	GeneratedAt time.Time         `json:"generated_at"`
	TotalCalls  int               `json:"total_calls"`
	TotalErrors int               `json:"total_errors"`
	Providers   []ProviderMetrics `json:"providers"`
}

// ProviderMetrics groups the per-model aggregates of one provider.
type ProviderMetrics struct {
	ProviderID string         `json:"provider_id"`
	TotalCalls int            `json:"total_calls"`
	Models     []ModelMetrics `json:"models"`
}

// ModelMetrics is the aggregate for one provider+model pair.
type ModelMetrics struct {
	Model      string             `json:"model"`
	Calls      int                `json:"calls"`
	Retries    int                `json:"retries"`
	Errors     map[ErrorClass]int `json:"errors"`
	Latency    map[string]int64   `json:"latency_ms"`
	Tokens     UsageTotals        `json:"tokens"`
	Cache      CacheCounts        `json:"cache"`
	LastCalled time.Time          `json:"last_called,omitempty"`
	LastError  ErrorClass         `json:"last_error,omitempty"`
}

// CacheCounts breaks calls with measured usage into cache hits (the provider
// served some prompt tokens from cache) and misses (no cached prompt tokens).
type CacheCounts struct {
	Hits   int `json:"hits"`
	Misses int `json:"misses"`
}

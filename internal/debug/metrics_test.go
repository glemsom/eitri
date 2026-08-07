package debug

import "testing"

// TestClassifyError is the classification table for the interaction metrics
// error classes. Classification happens at capture time from the HTTP status
// code plus the error message (provider error body for HTTP failures, the
// transport error string for network failures). It is never re-derived from
// display strings when serving metrics.
func TestClassifyError(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		message string
		want    ErrorClass
	}{
		{"http 429 rate limit", 429, "", ErrorClassRateLimit},
		{"http 529 overloaded", 529, "", ErrorClassRateLimit},
		{"http 401 auth", 401, "", ErrorClassAuth},
		{"http 403 auth", 403, "", ErrorClassAuth},
		{"http 408 timeout", 408, "", ErrorClassTimeout},
		{"http 504 gateway timeout", 504, "", ErrorClassTimeout},
		{"http 400 with context-length body", 400, "This model's maximum context length is 128000 tokens. You requested 130000.", ErrorClassContextLength},
		{"http 413 with context-length code", 413, "context_length_exceeded", ErrorClassContextLength},
		{"http 422 context window message", 422, "The context window is full; reduce prompt size", ErrorClassContextLength},
		{"http 400 unknown validation", 400, "invalid request body", ErrorClassOther},
		{"http 500 provider", 500, "", ErrorClassOther},
		{"http 404 model not found", 404, "", ErrorClassOther},
		{"transport connection refused", 0, "Post \"http://127.0.0.1:1\": dial tcp 127.0.0.1:1: connect: connection refused", ErrorClassNetwork},
		{"transport dns failure", 0, "dial tcp: lookup api.example.com: no such host", ErrorClassNetwork},
		{"transport deadline exceeded", 0, "Post \"https://api.example.com\": context deadline exceeded", ErrorClassTimeout},
		{"transport timeout marker", 0, "net/http: request canceled while waiting for connection (Client.Timeout exceeded while awaiting headers)", ErrorClassTimeout},
		{"empty error no status", 0, "", ErrorClassOther},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyError(tc.status, tc.message); got != tc.want {
				t.Fatalf("ClassifyError(%d, %q) = %q, want %q", tc.status, tc.message, got, tc.want)
			}
		})
	}
}

// TestUsageTotalsTokenTotal pins the derived-total derivation shared by the
// recorder's metrics snapshot and the window aggregate (issue #1240): the sum
// of the four usage components, not each trace's stored total (which may be
// zero on traces recorded before provider-usage enrichment).
func TestUsageTotalsTokenTotal(t *testing.T) {
	u := UsageTotals{
		PromptTokens:     10,
		CompletionTokens: 20,
		CacheReadTokens:  5,
		CacheWriteTokens: 2,
	}
	if got, want := u.TokenTotal(), 37; got != want {
		t.Fatalf("TokenTotal() = %d, want %d (sum of the four components)", got, want)
	}
	var zero UsageTotals
	if got := zero.TokenTotal(); got != 0 {
		t.Fatalf("zero UsageTotals TokenTotal() = %d, want 0", got)
	}
}

// TestErrorClassJSON ensures error classes serialize with stable string names
// so consumers of /api/debug/metrics do not depend on Go type spelling.
func TestErrorClassJSON(t *testing.T) {
	classes := []ErrorClass{
		ErrorClassRateLimit,
		ErrorClassTimeout,
		ErrorClassAuth,
		ErrorClassContextLength,
		ErrorClassNetwork,
		ErrorClassOther,
	}
	want := []string{"rate_limit", "timeout", "auth", "context_length", "network", "other"}
	for i, c := range classes {
		if got := string(c); got != want[i] {
			t.Fatalf("error class %d serializes as %q, want %q", i, got, want[i])
		}
	}
}

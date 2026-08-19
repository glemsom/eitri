package provider

import (
	"strings"
	"testing"
)

// TestIsContextOverflowSentinel recognizes the explicit ErrContextOverflow
// sentinel regardless of HTTP mapping.
func TestIsContextOverflowSentinel(t *testing.T) {
	t.Parallel()
	if !IsContextOverflow(ErrContextOverflow) {
		t.Fatal("IsContextOverflow(ErrContextOverflow) = false, want true")
	}
	// An unrelated error must not be an overflow.
	if IsContextOverflow(&HTTPError{Code: 400, Body: "bad request"}) {
		t.Fatal("IsContextOverflow(HTTP 400 bad request) = true, want false")
	}
}

// TestIsContextOverflowBodyNamesOverflow asserts a 4xx whose body names a
// context-length / token-limit condition is treated as overflow, so the engine
// emergency-compacts and retries (the DeepSeek/OpenCode surface).
func TestIsContextOverflowBodyNamesOverflow(t *testing.T) {
	t.Parallel()
	bodies := []string{
		`{"error":{"message":"This model's maximum context length is 16385 tokens."}}`,
		`{"error":"context window full"}`,
		`too many tokens`,
		`prompt exceeds the maximum context`,
		`the request exceeds the token limit`,
	}
	for _, b := range bodies {
		if !IsContextOverflow(&HTTPError{Code: 400, Body: b}) {
			t.Fatalf("IsContextOverflow(400 body %q) = false, want true", b)
		}
	}
}

// TestIsContextOverflowNonOverflow400 asserts a 400 that is NOT a context
// overflow (e.g. DeepSeek's json_object "must contain the word json" rejection,
// or a bad model/schema rejection) is NOT classified as overflow, so Eitri does
// not waste an emergency compaction on an unrelated client error and instead
// surfaces the real cause.
func TestIsContextOverflowNonOverflow400(t *testing.T) {
	t.Parallel()
	bodies := []string{
		`{"error":{"message":"Prompt must contain the word 'json' in some form to use 'response_format'."}}`,
		`{"error":"model does not exist"}`,
		`{"error":"invalid tool schema"}`,
		`bad request`,
		``,
	}
	for _, b := range bodies {
		if IsContextOverflow(&HTTPError{Code: 400, Body: b}) {
			t.Fatalf("IsContextOverflow(400 body %q) = true, want false", b)
		}
	}
	// A non-4xx status is never overflow.
	if IsContextOverflow(&HTTPError{Code: 500, Body: "context length exceeded"}) {
		t.Fatal("IsContextOverflow(500 context length) = true, want false")
	}
}

// TestHTTPErrorErrorIncludesBody pins the diagnostic fix: the provider's actual
// rejection body surfaces instead of an opaque status code.
func TestHTTPErrorErrorIncludesBody(t *testing.T) {
	t.Parallel()
	e := &HTTPError{Code: 400, Body: "Prompt must contain the word 'json' in some form"}
	got := e.Error()
	if !strings.HasPrefix(got, "provider returned HTTP 400") {
		t.Fatalf("Error() = %q, want it to prefix the status", got)
	}
	if !strings.Contains(got, "must contain the word") {
		t.Fatalf("Error() = %q, want it to carry the provider body", got)
	}
}

// TestHTTPErrorErrorOmitsEmptyBody keeps the legacy short form when no body was
// captured.
func TestHTTPErrorErrorOmitsEmptyBody(t *testing.T) {
	t.Parallel()
	e := &HTTPError{Code: 401}
	if got := e.Error(); got != "provider returned HTTP 401" {
		t.Fatalf("Error() = %q, want short form without body", got)
	}
}

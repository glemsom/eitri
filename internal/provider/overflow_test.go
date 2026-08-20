package provider

import (
	"strings"
	"testing"
)

func TestIsContextOverflowSentinel(t *testing.T) {
	t.Parallel()
	if !IsContextOverflow(ErrContextOverflow) {
		t.Fatal("IsContextOverflow(ErrContextOverflow) = false, want true")
	}
	if IsContextOverflow(&HTTPError{Code: 400, Body: "bad request"}) {
		t.Fatal("IsContextOverflow(HTTP 400 bad request) = true, want false")
	}
}

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
	if IsContextOverflow(&HTTPError{Code: 500, Body: "context length exceeded"}) {
		t.Fatal("IsContextOverflow(500 context length) = true, want false")
	}
}

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

func TestHTTPErrorErrorOmitsEmptyBody(t *testing.T) {
	t.Parallel()
	e := &HTTPError{Code: 401}
	if got := e.Error(); got != "provider returned HTTP 401" {
		t.Fatalf("Error() = %q, want short form without body", got)
	}
}

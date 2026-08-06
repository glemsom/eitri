package uixt

import (
	"fmt"
	"testing"
)

// ---------------------------------------------------------------------------
// FormatErrorMessage
// ---------------------------------------------------------------------------

func TestFormatErrorMessage_ConnectionRefused(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("dial tcp 127.0.0.1:11434: connect: connection refused")
	msg := FormatErrorMessage(err)
	want := "Connection refused: LLM provider is not reachable. Check that your provider is running."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_Authentication(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("401 Unauthorized: invalid API key")
	msg := FormatErrorMessage(err)
	want := "Authentication failed. Check your API key in Settings."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_AuthenticationAlt(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("Authentication failed: bad credentials")
	msg := FormatErrorMessage(err)
	want := "Authentication failed. Check your API key in Settings."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_RateLimit(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("429 Too Many Requests: rate limit exceeded")
	msg := FormatErrorMessage(err)
	want := "Rate limited by provider. Please wait a moment and try again."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_RateLimitAlt(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("Rate limit exceeded, retry after 60s")
	msg := FormatErrorMessage(err)
	want := "Rate limited by provider. Please wait a moment and try again."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_ContextLength(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("context length exceeded: maximum is 128000 tokens")
	msg := FormatErrorMessage(err)
	want := "Context length exceeded. Try a shorter message or reduce conversation history."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_ContextLengthAlt(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("This model's maximum context length is 128000 tokens")
	msg := FormatErrorMessage(err)
	want := "Context length exceeded. Try a shorter message or reduce conversation history."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_ModelNoLongerAvailable(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("model no longer available: gpt-4")
	msg := FormatErrorMessage(err)
	want := "Selected model no longer available. Choose another model in Settings."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_ModelNotFound(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("model not found: claude-v1")
	msg := FormatErrorMessage(err)
	want := "Selected model no longer available. Choose another model in Settings."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_StreamingToolCalls(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("streaming tool calls are not supported by this provider")
	msg := FormatErrorMessage(err)
	want := "Provider does not support required streaming tool calls. Use OpenCode Go or another compatible provider."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_StreamingNotSupported(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("streaming not supported")
	msg := FormatErrorMessage(err)
	want := "Provider does not support required streaming tool calls. Use OpenCode Go or another compatible provider."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_Timeout(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("request timeout after 30s")
	msg := FormatErrorMessage(err)
	want := "Request timed out. The provider took too long to respond."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_PortInUse(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("listen tcp :8080: bind: address already in use")
	msg := FormatErrorMessage(err)
	want := "Cannot bind port: address already in use. Try EITRI_ADDR=127.0.0.1:8081 eitri."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_PortInUseAlt(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("port already in use")
	msg := FormatErrorMessage(err)
	want := "Cannot bind port: address already in use. Try EITRI_ADDR=127.0.0.1:8081 eitri."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_NoSuchHost(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("dial tcp: lookup api.example.com: no such host")
	msg := FormatErrorMessage(err)
	want := "Cannot reach provider at the configured URL. Check base_url in Settings."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_LookupFail(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("lookup: api.example.com not found")
	msg := FormatErrorMessage(err)
	want := "Cannot reach provider at the configured URL. Check base_url in Settings."
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_Fallback(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("some unknown provider error")
	msg := FormatErrorMessage(err)
	want := "LLM error: some unknown provider error"
	if msg != want {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestFormatErrorMessage_EmptyError(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("")
	msg := FormatErrorMessage(err)
	if msg != "LLM error: " {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestStripHTMLTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple HTML tags",
			input: "<b>hello</b> world",
			want:  "hello world",
		},
		{
			name:  "style tag content stripped",
			input: "<style>[data-component=\"top\"]{min-height:80px}</style>text",
			want:  "text",
		},
		{
			name:  "script tag content stripped",
			input: "<script>window._$HY||function(e){}</script>text",
			want:  "text",
		},
		{
			name:  "script with src attribute stripped",
			input: "<script src=\"/bundle.js\"></script>text",
			want:  "text",
		},
		{
			name:  "Full SolidJS HTML page",
			input: "<!DOCTYPE html><html><head><style>[data-component=\"top\"]{min-height:80px}</style></head><body><script>window._$HY||function(e){}</script><p>hello</p></body></html>",
			want:  "hello",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "no HTML",
			input: "just plain text",
			want:  "just plain text",
		},
		{
			name:  "multiline JS",
			input: "<script>\nwindow._$HY = {};\nconsole.log('test');\n</script>result",
			want:  "result",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripHTMLTags(tt.input)
			if got != tt.want {
				t.Errorf("stripHTMLTags() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatErrorMessage_HTMLError(t *testing.T) {
	t.Parallel()

	// Simulate an error whose message contains HTML with style/script content.
	err := fmt.Errorf("<html><head><style>[data-component=\"top\"]{min-height:80px}</style></head><body><script>window._$HY||function(e){}</script></body></html>")
	msg := FormatErrorMessage(err)
	want := "LLM error: "
	if msg != want {
		t.Errorf("FormatErrorMessage() = %q, want %q", msg, want)
	}
}

// ---------------------------------------------------------------------------
// MaxTurnsMessage
// ---------------------------------------------------------------------------

func TestMaxTurnsMessage_Singular(t *testing.T) {
	t.Parallel()

	msg := MaxTurnsMessage(1)
	want := "Stopped after reaching max turns limit (1). Increase Max Turns in Settings if this task needs tool follow-up steps."
	if msg != want {
		t.Errorf("MaxTurnsMessage(1) = %q, want %q", msg, want)
	}
}

func TestMaxTurnsMessage_Plural(t *testing.T) {
	t.Parallel()

	msg := MaxTurnsMessage(5)
	want := "Stopped after reaching max turns limit (5). Increase Max Turns in Settings if this task needs more tool/model steps."
	if msg != want {
		t.Errorf("MaxTurnsMessage(5) = %q, want %q", msg, want)
	}
}

func TestMaxTurnsMessage_Zero(t *testing.T) {
	t.Parallel()

	msg := MaxTurnsMessage(0)
	want := "Stopped after reaching max turns limit (0). Increase Max Turns in Settings if this task needs more tool/model steps."
	if msg != want {
		t.Errorf("MaxTurnsMessage(0) = %q, want %q", msg, want)
	}
}

package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestLogRequestWritesStructuredFields covers the per-request log emission.
// The middleware suppresses http_request lines under test binaries
// (issue #1031), so the emission logic is tested directly.
func TestLogRequestWritesStructuredFields(t *testing.T) {
	var logs bytes.Buffer
	s := &Server{logger: slog.New(slog.NewJSONHandler(&logs, nil))}

	req, err := http.NewRequest(http.MethodGet, "http://example.com/sessions/abc123/chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.logRequest(req, http.StatusOK, 42*time.Millisecond)

	out := logs.String()
	for _, want := range []string{
		`"method":"GET"`,
		`"path":"/sessions/abc123/chat"`,
		`"status":200`,
		`"duration_ms":42`,
		`"session_id":"abc123"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q:\n%s", want, out)
		}
	}
}

func TestExtractSessionIDFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/sessions/abc123", "abc123"},
		{"/api/sessions/def456/chat", "def456"},
		{"/api/debug/sessions/ghi789", ""},
		{"/", ""},
		{"/sessions", ""},
		{"/sessions/", ""},
	}
	for _, tc := range cases {
		if got := extractSessionIDFromPath(tc.path); got != tc.want {
			t.Errorf("extractSessionIDFromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// defaultHTTPClient is reused across all adapters.
var defaultHTTPClient = &http.Client{Timeout: 5 * time.Minute}

// debugLogPayload dumps full LLM request JSON to slog.Info when set.
// Enable via EITRI_DEBUG_PROMPT=1 or EITRI_DEBUG_REQUEST=1 env var.
var debugLogPayload = os.Getenv("EITRI_DEBUG_PROMPT") == "1" || os.Getenv("EITRI_DEBUG_REQUEST") == "1"

// doRequest sends the HTTP request and returns the response.
func doRequest(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, classifyNetError(err)
	}
	return resp, nil
}

// classifyNetError maps network errors to user-facing messages.
func classifyNetError(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return fmt.Errorf("connection refused: provider is not reachable. Check that your LLM provider is running and accessible")
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline exceeded"):
		return fmt.Errorf("request timed out. The provider took too long to respond")
	default:
		return fmt.Errorf("LLM request failed: %w", err)
	}
}

// readAll reads and closes the response body.
func readAll(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// marshalJSONNoEscape marshals v to JSON without escaping HTML characters (<, >, &).
// Go's json.Marshal escapes these by default (safe for embedding in HTML),
// but for LLM API calls the literal characters are preferred and expected.
func marshalJSONNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Encode appends a newline; strip it for consistency with json.Marshal.
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}

// classifyHTTPError creates a user-facing error from an HTTP error response.
func classifyHTTPError(statusCode int, body []byte) error {
	var errBody struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	var msg string
	if len(body) > 0 {
		if err := json.Unmarshal(body, &errBody); err == nil && errBody.Error.Message != "" {
			msg = errBody.Error.Message
		}
	}

	lower := strings.ToLower(msg)
	switch {
	case statusCode == 401:
		if msg != "" {
			return fmt.Errorf("Authentication failed (401): %s", msg)
		}
		return fmt.Errorf("Authentication failed (401). Check your API key")
	case statusCode == 429:
		if msg != "" {
			return fmt.Errorf("Rate limited (429): %s", msg)
		}
		return fmt.Errorf("Rate limited (429). Try again later")
	case statusCode == 400 && (strings.Contains(lower, "context_length") || strings.Contains(lower, "context length")):
		return fmt.Errorf("Context length exceeded. Your message is too long for the selected model")
	case statusCode >= 500:
		if msg != "" {
			return fmt.Errorf("Server error (%d): %s", statusCode, msg)
		}
		return fmt.Errorf("Server error (%d). The provider encountered an error", statusCode)
	default:
		if msg != "" {
			return fmt.Errorf("Provider returned HTTP %d: %s", statusCode, msg)
		}
		return fmt.Errorf("Provider returned HTTP %d", statusCode)
	}
}

// writeLLMDebugFile writes the full request and response to a JSON debug file
// when EITRI_DEBUG_LLM_DIR is set and an LLM request fails.
func writeLLMDebugFile(url string, reqBody, respBody []byte, statusCode int, prefix string) {
	dir := os.Getenv("EITRI_DEBUG_LLM_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("cannot create LLM debug dir", slog.String("dir", dir), slog.Any("error", err))
		return
	}

	timestamp := time.Now().UnixNano()
	filename := fmt.Sprintf("%s-llm-debug-%d.json", prefix, timestamp)
	path := filepath.Join(dir, filename)

	type debugEntry struct {
		URL            string          `json:"url"`
		Request        json.RawMessage `json:"request"`
		ResponseStatus int             `json:"response_status"`
		ResponseBody   string          `json:"response_body"`
	}

	entry := debugEntry{
		URL:            url,
		Request:        json.RawMessage(reqBody),
		ResponseStatus: statusCode,
		ResponseBody:   string(respBody),
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		slog.Warn("failed to marshal LLM debug entry", slog.Any("error", err))
		return
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		slog.Warn("failed to write LLM debug file", slog.String("path", path), slog.Any("error", err))
		return
	}

	slog.Warn("LLM debug file written", slog.String("path", path), slog.Int("status", statusCode))
}

// ————— shared HTTP request helpers —————

// doChatRequest is a generic helper for non-streaming chat requests.
// It marshals req to JSON, sends an HTTP POST, reads the full response body,
// and unmarshals the JSON into a new *Resp.
//
// setHeaders is called after Content-Type and Accept are set so the adapter
// can add auth, tracking, or other headers.
func doChatRequest[Req, Resp any](ctx context.Context, client *http.Client, url string, req Req, setHeaders func(*http.Request)) (*Resp, error) {
	reqBody, err := marshalJSONNoEscape(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	setHeaders(httpReq)

	if debugLogPayload {
		slog.Info("llm request",
			slog.String("endpoint", url),
			slog.String("body", string(reqBody)),
		)
	}
	resp, err := doRequest(ctx, client, httpReq)
	if err != nil {
		return nil, err
	}

	respBody, err := readAll(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		writeLLMDebugFile(url, reqBody, respBody, resp.StatusCode, "chat")
		return nil, classifyHTTPError(resp.StatusCode, respBody)
	}

	var wireResp Resp
	if err := json.Unmarshal(respBody, &wireResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &wireResp, nil
}

// doChatStreamRequest is a generic helper for streaming chat requests.
// It marshals req to JSON, sends an HTTP POST with Accept: text/event-stream,
// checks the response status, and returns the raw *http.Response.
// The caller must start a goroutine to read the stream from resp.Body.
//
// setHeaders is called after Content-Type and Accept are set.
func doChatStreamRequest[Req any](ctx context.Context, client *http.Client, url string, req Req, setHeaders func(*http.Request)) (*http.Response, error) {
	reqBody, err := marshalJSONNoEscape(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	setHeaders(httpReq)

	if debugLogPayload {
		slog.Info("llm request",
			slog.String("endpoint", url),
			slog.String("body", string(reqBody)),
		)
	}
	resp, err := doRequest(ctx, client, httpReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := readAll(resp)
		writeLLMDebugFile(url, reqBody, respBody, resp.StatusCode, "stream")
		return nil, classifyHTTPError(resp.StatusCode, respBody)
	}

	return resp, nil
}

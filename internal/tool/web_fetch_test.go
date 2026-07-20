package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/voocel/litellm"
)

func TestWebFetch_Schema(t *testing.T) {
	t.Parallel()
	tool := NewWebFetchTool()
	if tool.Name() != "web_fetch" {
		t.Errorf("Name = %q, want 'web_fetch'", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description should not be empty")
	}
	schema := tool.JSONSchema()
	if schema == nil {
		t.Fatal("JSONSchema is nil")
	}
	if !json.Valid(schema) {
		t.Error("JSONSchema is not valid JSON")
	}
}

func TestWebFetch_SchemaHasURLParam(t *testing.T) {
	t.Parallel()
	schema := NewWebFetchTool().JSONSchema()
	var schemaObj map[string]interface{}
	if err := json.Unmarshal(schema, &schemaObj); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	props, ok := schemaObj["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("schema missing properties")
	}
	urlProp, ok := props["url"]
	if !ok {
		t.Fatal("schema missing 'url' property")
	}
	urlMap, ok := urlProp.(map[string]interface{})
	if !ok {
		t.Fatal("url property is not a map")
	}
	if urlMap["type"] != "string" {
		t.Errorf("url type = %v, want 'string'", urlMap["type"])
	}
	required, ok := schemaObj["required"].([]interface{})
	if !ok {
		t.Fatal("schema missing required array")
	}
	hasURL := false
	for _, r := range required {
		if r == "url" {
			hasURL = true
			break
		}
	}
	if !hasURL {
		t.Error("'url' not in required array")
	}
}

func TestWebFetch_InvalidArgs(t *testing.T) {
	t.Parallel()
	tool := NewWebFetchTool()
	_, err, _ := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestWebFetch_EmptyURL(t *testing.T) {
	t.Parallel()
	tool := NewWebFetchTool()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"url":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true for empty URL")
	}
	_ = blocks
}

func TestWebFetch_MissingURL(t *testing.T) {
	t.Parallel()
	tool := NewWebFetchTool()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true (url is required)")
	}
	_ = blocks
}

func TestWebFetch_InvalidURL(t *testing.T) {
	t.Parallel()
	tool := NewWebFetchTool()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"url":"not-a-valid-url"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true for invalid URL")
	}
	_ = blocks
}

func TestWebFetch_FetchSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Test Page</title></head><body><h1>Hello</h1><p>World</p></body></html>`)
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	tb, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block type = %T, want TextBlock", blocks[0])
	}
	if !strings.Contains(tb.Text, "Hello") || !strings.Contains(tb.Text, "World") {
		t.Errorf("unexpected text content: %q", tb.Text)
	}
}

func TestWebFetch_FetchLargePage(t *testing.T) {
	t.Parallel()
	largeBody := "<html><body>" + strings.Repeat("<p>content</p>", 10000) + "</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, largeBody)
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks for large page")
	}
}

func TestWebFetch_HTTPServerError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "internal error")
	}))
	defer srv.Close()

	tool := NewWebFetchTool()
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true for HTTP 500")
	}
	_ = blocks
}

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/api"
)

func TestHealthEndpoint(t *testing.T) {
	srv := api.NewServer(api.ServerConfig{})
	server := httptest.NewServer(srv.Handler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /health status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Errorf(`body["status"] = %q, want "ok"`, body["status"])
	}
}

func TestRootPage(t *testing.T) {
	srv := api.NewServer(api.ServerConfig{})
	server := httptest.NewServer(srv.Handler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET / status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestRootPageTitle(t *testing.T) {
	srv := api.NewServer(api.ServerConfig{})
	server := httptest.NewServer(srv.Handler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Check title is present in body
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])
	if !strings.Contains(content, "Eitri — AI Assistant") {
		t.Errorf("page body missing title, got: %s", content)
	}
}

func TestHealthMethodNotAllowed(t *testing.T) {
	srv := api.NewServer(api.ServerConfig{})
	server := httptest.NewServer(srv.Handler())
	defer server.Close()

	resp, err := http.Post(server.URL+"/health", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /health status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

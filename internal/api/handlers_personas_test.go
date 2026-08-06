package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/persona"
)

// newPersonaTestServer creates a test server with an isolated persona home dir
// and returns the server plus that home dir so tests can seed personas via
// persona.SaveToHome without touching the process HOME env (issue #1023).
func newPersonaTestServer(t *testing.T, workspace string) (*httptest.Server, string) {
	t.Helper()
	homeDir := t.TempDir()
	if err := persona.EnsureGenericWithHome(homeDir); err != nil {
		t.Fatalf("ensure generic persona: %v", err)
	}
	return newTestServerAtWorkspaceWithHome(t, workspace, homeDir), homeDir
}

// ————— handleGetPersonas —————

func TestHandleGetPersonas_ReturnsJSON(t *testing.T) {
	workspace := t.TempDir()
	server, _ := newPersonaTestServer(t, workspace)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/personas")
	if err != nil {
		t.Fatalf("GET /api/personas failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	var personas []*persona.PersonaDefinition
	if err := json.Unmarshal(body, &personas); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}

	// Generic persona should exist after first call
	foundGeneric := false
	for _, p := range personas {
		if p.Name == "generic" {
			foundGeneric = true
			if p.SystemPrompt == "" {
				t.Error("generic persona has empty SystemPrompt")
			}
			break
		}
	}
	if !foundGeneric {
		t.Error("generic persona not found in list")
	}
}

func TestHandleGetPersonas_HTMX(t *testing.T) {
	workspace := t.TempDir()
	server, _ := newPersonaTestServer(t, workspace)
	defer server.Close()

	req, err := http.NewRequest("GET", server.URL+"/api/personas", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("HX-Request", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/personas (HTMX) failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

// ————— handleCreatePersona —————

func TestHandleCreatePersona_JSON(t *testing.T) {
	workspace := t.TempDir()
	server, homeDir := newPersonaTestServer(t, workspace)
	defer server.Close()

	body := `{"name":"test-agent","system_prompt":"You are a test agent.","required_skills":["read","write"]}`
	resp, err := http.Post(server.URL+"/api/personas", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/personas failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected status 201, got %d", resp.StatusCode)
	}

	// Verify persona was saved
	def, err := persona.LoadWithHome(workspace, homeDir, "test-agent")
	if err != nil {
		t.Fatalf("failed to load created persona: %v", err)
	}
	if def.SystemPrompt != "You are a test agent." {
		t.Errorf("SystemPrompt = %q, want %q", def.SystemPrompt, "You are a test agent.")
	}
}

func TestHandleCreatePersona_EmptyName(t *testing.T) {
	workspace := t.TempDir()
	server, _ := newPersonaTestServer(t, workspace)
	defer server.Close()

	body := `{"name":"","system_prompt":"test"}`
	resp, err := http.Post(server.URL+"/api/personas", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/personas failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected status 422, got %d", resp.StatusCode)
	}
}

func TestHandleCreatePersona_Duplicate(t *testing.T) {
	workspace := t.TempDir()
	server, _ := newPersonaTestServer(t, workspace)
	defer server.Close()

	// Create first
	body := `{"name":"dup-agent","system_prompt":"first"}`
	resp, err := http.Post(server.URL+"/api/personas", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("first POST failed: %v", err)
	}
	resp.Body.Close()

	// Create duplicate
	resp, err = http.Post(server.URL+"/api/personas", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("second POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Errorf("expected status 409 for duplicate, got %d", resp.StatusCode)
	}
}

func TestHandleCreatePersona_FormEncoded(t *testing.T) {
	workspace := t.TempDir()
	server, homeDir := newPersonaTestServer(t, workspace)
	defer server.Close()

	formBody := "name=form-agent&system_prompt=Created+via+form"
	resp, err := http.Post(server.URL+"/api/personas", "application/x-www-form-urlencoded", strings.NewReader(formBody))
	if err != nil {
		t.Fatalf("POST /api/personas (form) failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		// Verify persona exists
		def, err := persona.LoadWithHome(workspace, homeDir, "form-agent")
		if err != nil {
			t.Fatalf("failed to load form-created persona: %v", err)
		}
		if def.SystemPrompt != "Created via form" {
			t.Errorf("SystemPrompt = %q, want %q", def.SystemPrompt, "Created via form")
		}
	} else {
		t.Errorf("expected 201 or 200, got %d", resp.StatusCode)
	}
}

// ————— handleGetPersona —————

func TestHandleCreatePersona_VisibleSkillsJSON(t *testing.T) {
	workspace := t.TempDir()
	server, homeDir := newPersonaTestServer(t, workspace)
	defer server.Close()

	body := `{"name":"scoped","system_prompt":"You are scoped.","required_skills":["read"],"visible_skills":["alpha","beta"]}`
	resp, err := http.Post(server.URL+"/api/personas", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/personas failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected status 201, got %d", resp.StatusCode)
	}

	def, err := persona.LoadWithHome(workspace, homeDir, "scoped")
	if err != nil {
		t.Fatalf("load created persona: %v", err)
	}
	if len(def.VisibleSkills) != 2 || def.VisibleSkills[0] != "alpha" || def.VisibleSkills[1] != "beta" {
		t.Errorf("VisibleSkills = %v, want [alpha beta]", def.VisibleSkills)
	}
	if len(def.RequiredSkills) != 1 || def.RequiredSkills[0] != "read" {
		t.Errorf("RequiredSkills = %v, want [read]", def.RequiredSkills)
	}
}

func TestHandleCreatePersona_VisibleSkillsForm(t *testing.T) {
	workspace := t.TempDir()
	server, homeDir := newPersonaTestServer(t, workspace)
	defer server.Close()

	formBody := "name=form-scoped&system_prompt=Form+scoped&visible_skills=alpha&visible_skills=beta&visible_skills=gamma"
	resp, err := http.Post(server.URL+"/api/personas", "application/x-www-form-urlencoded", strings.NewReader(formBody))
	if err != nil {
		t.Fatalf("POST /api/personas (form) failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected status 201, got %d", resp.StatusCode)
	}

	def, err := persona.LoadWithHome(workspace, homeDir, "form-scoped")
	if err != nil {
		t.Fatalf("load form-created persona: %v", err)
	}
	if len(def.VisibleSkills) != 3 {
		t.Errorf("VisibleSkills = %v, want 3 entries", def.VisibleSkills)
	}

	// Create with a comma-separated single value is also supported by listFormField.
	formBody2 := "name=comma-scoped&system_prompt=Comma&visible_skills=alpha,beta"
	resp2, err := http.Post(server.URL+"/api/personas", "application/x-www-form-urlencoded", strings.NewReader(formBody2))
	if err != nil {
		t.Fatalf("POST (comma form) failed: %v", err)
	}
	defer resp2.Body.Close()

	def2, err := persona.LoadWithHome(workspace, homeDir, "comma-scoped")
	if err != nil {
		t.Fatalf("load comma persona: %v", err)
	}
	if len(def2.VisibleSkills) != 2 {
		t.Errorf("VisibleSkills = %v, want 2 entries", def2.VisibleSkills)
	}
}

func TestHandleUpdatePersona_VisibleSkills(t *testing.T) {
	workspace := t.TempDir()
	server, homeDir := newPersonaTestServer(t, workspace)
	defer server.Close()

	persona.SaveToHome(homeDir, &persona.PersonaDefinition{
		Name:         "updatable-scoped",
		SystemPrompt: "Original prompt.",
	})

	body := `{"system_prompt":"Updated.","visible_skills":["gamma"]}`
	req, _ := http.NewRequest(http.MethodPut, server.URL+"/api/personas/updatable-scoped", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	def, err := persona.LoadWithHome(workspace, homeDir, "updatable-scoped")
	if err != nil {
		t.Fatalf("load after update: %v", err)
	}
	if len(def.VisibleSkills) != 1 || def.VisibleSkills[0] != "gamma" {
		t.Errorf("VisibleSkills = %v, want [gamma]", def.VisibleSkills)
	}
}

func TestHandleGetPersona_Single(t *testing.T) {
	workspace := t.TempDir()
	server, homeDir := newPersonaTestServer(t, workspace)
	defer server.Close()

	// Create persona first
	persona.SaveToHome(homeDir, &persona.PersonaDefinition{
		Name:         "single",
		SystemPrompt: "Single persona test.",
	})

	resp, err := http.Get(server.URL + "/api/personas/single")
	if err != nil {
		t.Fatalf("GET /api/personas/single failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var def persona.PersonaDefinition
	if err := json.NewDecoder(resp.Body).Decode(&def); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if def.Name != "single" {
		t.Errorf("Name = %q, want %q", def.Name, "single")
	}
}

func TestHandleGetPersona_NotFound(t *testing.T) {
	workspace := t.TempDir()
	server, _ := newPersonaTestServer(t, workspace)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/personas/nonexistent")
	if err != nil {
		t.Fatalf("GET /api/personas/nonexistent failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

// ————— handleUpdatePersona —————

func TestHandleUpdatePersona(t *testing.T) {
	workspace := t.TempDir()
	server, homeDir := newPersonaTestServer(t, workspace)
	defer server.Close()

	// Create
	persona.SaveToHome(homeDir, &persona.PersonaDefinition{
		Name:         "updatable",
		SystemPrompt: "Original prompt.",
	})

	// Update via PUT
	body := `{"system_prompt":"Updated prompt.","required_skills":["skill1"]}`
	req, err := http.NewRequest(http.MethodPut, server.URL+"/api/personas/updatable", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/personas/updatable failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Verify update
	def, err := persona.LoadWithHome(workspace, homeDir, "updatable")
	if err != nil {
		t.Fatalf("load after update: %v", err)
	}
	if def.SystemPrompt != "Updated prompt." {
		t.Errorf("SystemPrompt = %q, want %q", def.SystemPrompt, "Updated prompt.")
	}
}

func TestHandleUpdatePersona_NotFound(t *testing.T) {
	workspace := t.TempDir()
	server, _ := newPersonaTestServer(t, workspace)
	defer server.Close()

	body := `{"system_prompt":"test"}`
	req, err := http.NewRequest(http.MethodPut, server.URL+"/api/personas/nonexistent", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

// ————— handleDeletePersona —————

func TestHandleDeletePersona(t *testing.T) {
	workspace := t.TempDir()
	server, homeDir := newPersonaTestServer(t, workspace)
	defer server.Close()

	// Create
	persona.SaveToHome(homeDir, &persona.PersonaDefinition{
		Name:         "delete-me",
		SystemPrompt: "To be deleted.",
	})

	// Delete
	req, err := http.NewRequest(http.MethodDelete, server.URL+"/api/personas/delete-me", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/personas/delete-me failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", resp.StatusCode)
	}

	// Verify deletion
	_, err = persona.LoadWithHome(workspace, homeDir, "delete-me")
	if err == nil {
		t.Error("persona still exists after deletion")
	}
}

func TestHandleDeletePersona_GenericFails(t *testing.T) {
	workspace := t.TempDir()
	server, _ := newPersonaTestServer(t, workspace)
	defer server.Close()

	// Generic persona exists because newPersonaTestServer ensures it

	req, err := http.NewRequest(http.MethodDelete, server.URL+"/api/personas/generic", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/personas/generic failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected status 422 for deleting generic, got %d", resp.StatusCode)
	}
}

func TestHandleDeletePersona_NotFound(t *testing.T) {
	workspace := t.TempDir()
	server, _ := newPersonaTestServer(t, workspace)
	defer server.Close()

	req, err := http.NewRequest(http.MethodDelete, server.URL+"/api/personas/nonexistent", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

// ————— handleGetPersonaAddForm —————

func TestHandleGetPersonaAddForm(t *testing.T) {
	workspace := t.TempDir()
	server, _ := newPersonaTestServer(t, workspace)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/personas/add-form")
	if err != nil {
		t.Fatalf("GET /api/personas/add-form failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

// ————— 10-persona limit —————

func TestHandleCreatePersona_LimitEnforced(t *testing.T) {
	workspace := t.TempDir()
	server, _ := newPersonaTestServer(t, workspace)
	defer server.Close()

	// Create 10 custom personas (the max)
	for i := 1; i <= 10; i++ {
		name := fmt.Sprintf("persona-%d", i)
		body := fmt.Sprintf(`{"name":"%s","system_prompt":"Persona %d"}`, name, i)
		resp, err := http.Post(server.URL+"/api/personas", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST /api/personas (iteration %d) failed: %v", i, err)
		}
		if resp.StatusCode != http.StatusCreated {
			t.Errorf("expected status 201 for persona %d, got %d", i, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// Attempt to create an 11th persona
	body := `{"name":"persona-11","system_prompt":"Extra persona"}`
	resp, err := http.Post(server.URL+"/api/personas", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/personas (11th) failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity && resp.StatusCode != http.StatusConflict {
		t.Errorf("expected status 422 or 409 for exceeding limit, got %d", resp.StatusCode)
	}

	// Verify the response body mentions the limit
	respBody, _ := io.ReadAll(resp.Body)
	msg := strings.ToLower(string(respBody))
	if !strings.Contains(msg, "limit") && !strings.Contains(msg, "maximum") && !strings.Contains(msg, "10") {
		t.Errorf("response body should mention the limit: %s", string(respBody))
	}
}

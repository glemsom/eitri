package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/persona"
)

// handleGetPersonas returns all persona definitions as JSON or HTML fragment.
func (s *Server) handleGetPersonas(w http.ResponseWriter, r *http.Request) {
	workspace := s.config.Workspace

	// Ensure generic persona exists
	_ = persona.EnsureGeneric(workspace)

	names, err := persona.List(workspace)
	if err != nil {
		http.Error(w, "Failed to list personas: "+err.Error(), http.StatusInternalServerError)
		return
	}

	defs := make([]*persona.PersonaDefinition, 0, len(names))
	for _, name := range names {
		def, err := persona.Load(workspace, name)
		if err != nil {
			continue // skip unloadable personas
		}
		defs = append(defs, def)
	}

	if isHTMXRequest(r) {
		cfg, _ := config.Load(s.config.ConfigPath)
		activePersona := ""
		if cfg != nil {
			activePersona = cfg.ActivePersona
		}
		component := templates.PersonaList(defs, activePersona)
		component.Render(r.Context(), w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(defs)
}

// handleGetPersonaAddForm returns the HTML fragment for the add-persona form.
func (s *Server) handleGetPersonaAddForm(w http.ResponseWriter, r *http.Request) {
	component := templates.PersonaAddForm()
	component.Render(r.Context(), w)
}

// parsePersonaRequest extracts persona fields from either a JSON body or form data.
func parsePersonaRequest(r *http.Request) (name, systemPrompt string, injectedSkills []string, err error) {
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var req struct {
			Name           string   `json:"name"`
			SystemPrompt   string   `json:"system_prompt"`
			InjectedSkills []string `json:"injected_skills,omitempty"`
		}
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			err = readErr
			return
		}
		defer r.Body.Close()
		if jsonErr := json.Unmarshal(body, &req); jsonErr != nil {
			err = jsonErr
			return
		}
		name = req.Name
		systemPrompt = req.SystemPrompt
		injectedSkills = req.InjectedSkills
		return
	}

	// Form-encoded
	if parseErr := r.ParseForm(); parseErr != nil {
		err = parseErr
		return
	}
	name = r.Form.Get("name")
	systemPrompt = r.Form.Get("system_prompt")
	// Skills may come as a comma-separated list or repeated form fields
	if skillsStr := r.Form.Get("injected_skills"); skillsStr != "" {
		for _, s := range strings.Split(skillsStr, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				injectedSkills = append(injectedSkills, s)
			}
		}
	}
	return
}

// handleCreatePersona creates a new persona.
func (s *Server) handleCreatePersona(w http.ResponseWriter, r *http.Request) {
	workspace := s.config.Workspace

	name, systemPrompt, injectedSkills, err := parsePersonaRequest(r)
	if err != nil {
		writeConfigError(w, r, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}

	if strings.TrimSpace(name) == "" {
		writeConfigError(w, r, http.StatusUnprocessableEntity, "Persona name must not be empty")
		return
	}

	// Check name doesn't already exist
	if existing, _ := persona.Load(workspace, name); existing != nil {
		writeConfigError(w, r, http.StatusConflict, "Persona \""+name+"\" already exists")
		return
	}

	// Check limit (not counting generic)
	names, _ := persona.List(workspace)
	customCount := 0
	for _, n := range names {
		if n != persona.GenericName {
			customCount++
		}
	}
	if customCount >= persona.MaxCustomPersonas {
		writeConfigError(w, r, http.StatusUnprocessableEntity,
			"Cannot create persona: maximum of "+strconv.Itoa(persona.MaxCustomPersonas)+" custom personas reached")
		return
	}

	def := &persona.PersonaDefinition{
		Name:           name,
		SystemPrompt:   systemPrompt,
		InjectedSkills: injectedSkills,
	}

	if err := persona.Save(workspace, def); err != nil {
		writeConfigError(w, r, http.StatusInternalServerError, "Failed to save persona: "+err.Error())
		return
	}

	// Update catalog in config
	s.updatePersonaCatalog(workspace)

	if isHTMXRequest(r) {
		// Return updated persona list with the new persona visible
		s.handleGetPersonas(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(def)
}

// handleGetPersona returns a single persona definition as JSON or an edit form fragment.
func (s *Server) handleGetPersona(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	workspace := s.config.Workspace

	def, err := persona.Load(workspace, name)
	if err != nil {
		writeConfigError(w, r, http.StatusNotFound, "Persona \""+name+"\" not found")
		return
	}

	if isHTMXRequest(r) {
		// Return the edit form fragment
		component := templates.PersonaEditForm(def)
		component.Render(r.Context(), w)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(def)
}

// handleUpdatePersona updates an existing persona.
func (s *Server) handleUpdatePersona(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	workspace := s.config.Workspace

	// Check persona exists
	_, err := persona.Load(workspace, name)
	if err != nil {
		writeConfigError(w, r, http.StatusNotFound, "Persona \""+name+"\" not found")
		return
	}

	_, systemPrompt, injectedSkills, err := parsePersonaRequest(r)
	if err != nil {
		writeConfigError(w, r, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}

	def := &persona.PersonaDefinition{
		Name:           name,
		SystemPrompt:   systemPrompt,
		InjectedSkills: injectedSkills,
	}

	if err := persona.Save(workspace, def); err != nil {
		writeConfigError(w, r, http.StatusInternalServerError, "Failed to save persona: "+err.Error())
		return
	}

	// Update catalog in config
	s.updatePersonaCatalog(workspace)

	if isHTMXRequest(r) {
		// Return updated persona list
		s.handleGetPersonas(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(def)
}

// handleDeletePersona deletes a persona.
func (s *Server) handleDeletePersona(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	workspace := s.config.Workspace

	if name == persona.GenericName {
		writeConfigError(w, r, http.StatusUnprocessableEntity, "Cannot delete the generic persona")
		return
	}

	if err := persona.Delete(workspace, name); err != nil {
		writeConfigError(w, r, http.StatusNotFound, "Persona \""+name+"\" not found")
		return
	}

	// Update catalog in config
	s.updatePersonaCatalog(workspace)

	// If the deleted persona was active, reset to generic
	cfg, cfgErr := config.Load(s.config.ConfigPath)
	if cfgErr == nil && cfg.ActivePersona == name {
		cfg.ActivePersona = persona.GenericName
		_ = config.Save(s.config.ConfigPath, cfg)
	}

	if isHTMXRequest(r) {
		s.handleGetPersonas(w, r)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleActivatePersona sets the active persona in the config and returns
// the updated persona selector fragment for the header.
func (s *Server) handleActivatePersona(w http.ResponseWriter, r *http.Request) {
	workspace := s.config.Workspace

	// Read persona name from form body (submitted by the <select> element)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	name := r.FormValue("active_persona")

	// Verify persona exists
	if name != "" {
		_, err := persona.Load(workspace, name)
		if err != nil {
			writeConfigError(w, r, http.StatusNotFound, "Persona \""+name+"\" not found")
			return
		}
	}

	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	cfg.ActivePersona = name
	if err := config.Save(s.config.ConfigPath, cfg); err != nil {
		http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Return updated selector fragment
	s.handlePersonaSelector(w, r)
}

// updatePersonaCatalog refreshes the persona_catalog field in the config file
// to reflect the current set of persona files on disk.
func (s *Server) updatePersonaCatalog(workspace string) {
	names, err := persona.List(workspace)
	if err != nil {
		return
	}

	cfg, err := config.Load(s.config.ConfigPath)
	if err != nil {
		return
	}

	// Rebuild catalog from current files
	newCatalog := make(map[string]string, len(names))
	for _, name := range names {
		newCatalog[name] = ".eitri/personas/" + name + ".yaml"
	}
	cfg.PersonaCatalog = newCatalog

	_ = config.Save(s.config.ConfigPath, cfg)
}

// handlePersonaSelector returns an HTML fragment containing a <select>
// dropdown of all available personas, with the active one selected.
// Used by the header persona selector on every page.
func (s *Server) handlePersonaSelector(w http.ResponseWriter, r *http.Request) {
	workspace := s.config.Workspace
	_ = persona.EnsureGeneric(workspace)

	names, err := persona.List(workspace)
	if err != nil {
		http.Error(w, "Failed to list personas", http.StatusInternalServerError)
		return
	}

	defs := make([]*persona.PersonaDefinition, 0, len(names))
	for _, name := range names {
		def, err := persona.Load(workspace, name)
		if err != nil {
			continue
		}
		defs = append(defs, def)
	}

	cfg, _ := config.Load(s.config.ConfigPath)
	activePersona := persona.GenericName
	if cfg != nil && cfg.ActivePersona != "" {
		activePersona = cfg.ActivePersona
	}

	component := templates.PersonaSelector(defs, activePersona)
	component.Render(r.Context(), w)
}

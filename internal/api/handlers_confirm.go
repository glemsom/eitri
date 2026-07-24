package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/session"
)

// unifiedRenderRequest is the JSON body for the unified render route.
type unifiedRenderRequest struct {
	Kind        string          `json:"kind"`
	Tool        string          `json:"tool,omitempty"`
	Args        json.RawMessage `json:"args,omitempty"`
	Output      string          `json:"output,omitempty"`
	Status      string          `json:"status,omitempty"`
	ToolCallKey string          `json:"tool_call_key,omitempty"`
	Elapsed     string          `json:"elapsed,omitempty"`
	Message     string          `json:"message,omitempty"`
	MessageID   string          `json:"message_id,omitempty"`
	Name        string          `json:"name,omitempty"`
	Data        map[string]any  `json:"data,omitempty"`
}

func (s *Server) handleConfirm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	if s.config.RunService == nil {
		http.Error(w, "No run service", http.StatusInternalServerError)
		return
	}

	var body struct {
		Path     string `json:"path"`
		Approved bool   `json:"approved"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if body.Path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	// On approval, save the path to config before resolving so it persists
	if body.Approved {
		cfg, err := config.Load(s.config.ConfigPath)
		if err != nil {
			s.logger.Warn("failed to load config for approval", slog.Any("error", err))
		} else {
			// Add path to allowed_read_paths if not already present
			found := false
			for _, p := range cfg.AllowedReadPaths {
				if p == body.Path {
					found = true
					break
				}
			}
			if !found {
				cfg.AllowedReadPaths = append(cfg.AllowedReadPaths, body.Path)
				if err := config.Save(s.config.ConfigPath, cfg); err != nil {
					s.logger.Warn("failed to save config after approval", slog.Any("error", err))
				}
				// Config persisted; RunService picks up allowedReadPaths via RunConfig on next StartRun
			}
		}
	}

	resolved := s.config.RunService.ResolveConfirmation(id, body.Path, body.Approved)
	if !resolved {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "no pending confirmation for this session"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "ok", "approved": body.Approved})
}

func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		if isRequestTooLarge(err) {
			writeRequestTooLarge(w)
			return
		}
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req unifiedRenderRequest
	if err := json.Unmarshal(body, &req); err != nil {
		// HTMX 2.0 ajax() sends form-urlencoded even with contentType: 'application/json'
		// (issue #195 follow-up). Try parsing body as URL-encoded form data.
		if vals, parseErr := url.ParseQuery(string(body)); parseErr == nil && vals.Get("kind") != "" {
			req.Kind = vals.Get("kind")
			req.Tool = vals.Get("tool")
			req.Output = vals.Get("output")
			req.Status = vals.Get("status")
			req.ToolCallKey = vals.Get("tool_call_key")
			req.Elapsed = vals.Get("elapsed")
			req.Message = vals.Get("message")
			req.MessageID = vals.Get("message_id")
			req.Name = vals.Get("name")
			if args := vals.Get("args"); args != "" {
				req.Args = json.RawMessage(args)
			}
			if data := vals.Get("data"); data != "" {
				json.Unmarshal([]byte(data), &req.Data)
			}
		} else {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
	}

	// Error rendering doesn't require a valid session (may happen during setup)
	if req.Kind == "error" {
		component := templates.ErrorToast(req.Message)
		component.Render(r.Context(), w)
		return
	}

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Deduplicate markdown rendering by message_id
	if req.Kind == "markdown" && req.MessageID != "" {
		if s.config.SessionManager.HasRenderedMessageID(id, req.MessageID) {
			// Already rendered this message_id — return empty no-op response
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	switch req.Kind {
	case "error":
		component := templates.ErrorToast(req.Message)
		component.Render(r.Context(), w)

	case "markdown":
		var content string
		var components []session.ComponentData
		var quickReplies []string
		if sess != nil {
			for i := len(sess.Messages) - 1; i >= 0; i-- {
				if sess.Messages[i].Role == "assistant" {
					content = sess.Messages[i].Content
					components = sess.Messages[i].Components
					quickReplies = sess.Messages[i].QuickReplies
					break
				}
			}
		}
		if hasMermaidComponent(components) {
			content = stripMermaidCodeBlocks(content)
		}
		contentHTML := renderMarkdownToHTML(content)
		// Only inline components that belong inside the assistant bubble.
		// FileEditCard is already rendered as a tool card; MermaidDiagram
		// is the visual output of the LLM response and belongs inline.
		componentsHTML := renderInlineComponentsToHTML(r.Context(), id, components)
		if componentsHTML != "" {
			contentHTML += "\n" + componentsHTML
		}
		component := templates.AssistantBubble(id, contentHTML, quickReplies)
		component.Render(r.Context(), w)

		// Track rendered message_id for dedup
		if req.MessageID != "" {
			s.config.SessionManager.AddRenderedMessageID(id, req.MessageID)
		}

	case "component":
		switch req.Name {
		case "MermaidDiagram":
			code := ""
			if req.Data != nil {
				if c, ok := req.Data["code"].(string); ok {
					code = c
				}
			}
			component := templates.MermaidDiagram(code)
			component.Render(r.Context(), w)

		case "QuickReplies":
			var options []string
			if req.Data != nil {
				if opts, ok := req.Data["options"]; ok {
					if optsArr, ok := opts.([]string); ok {
						options = optsArr
					} else if optsArr, ok := opts.([]any); ok {
						for _, o := range optsArr {
							if s, ok := o.(string); ok {
								options = append(options, s)
							}
						}
					}
				}
			}
			component := templates.QuickReplies(id, options)
			component.Render(r.Context(), w)

		case "DiffCard":
			oldCode := ""
			newCode := ""
			lang := ""
			if req.Data != nil {
				if o, ok := req.Data["old"].(string); ok {
					oldCode = o
				}
				if n, ok := req.Data["new"].(string); ok {
					newCode = n
				}
				if l, ok := req.Data["lang"].(string); ok {
					lang = l
				}
			}
			component := templates.DiffCard(oldCode, newCode, lang)
			component.Render(r.Context(), w)

		case "FileEditCard":
			path := ""
			mode := ""
			oldContent := ""
			newContent := ""
			bytesWritten := 0
			if req.Data != nil {
				if p, ok := req.Data["path"].(string); ok {
					path = p
				}
				if m, ok := req.Data["mode"].(string); ok {
					mode = m
				}
				if o, ok := req.Data["old"].(string); ok {
					oldContent = o
				}
				if n, ok := req.Data["new"].(string); ok {
					newContent = n
				}
				if bw, ok := req.Data["bytes_written"].(float64); ok {
					bytesWritten = int(bw)
				}
			}
			component := templates.FileEditCard(path, mode, oldContent, newContent, bytesWritten, nil)
			component.Render(r.Context(), w)

		default:
			http.Error(w, "Unknown component", http.StatusBadRequest)
		}

	default:
		http.Error(w, "Unknown render kind", http.StatusBadRequest)
	}
}

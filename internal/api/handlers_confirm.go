package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/config"
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

	meta := s.config.SessionManager.GetMetaShared(id)
	if meta == nil || meta.BrowserID != browserID {
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

	meta := s.config.SessionManager.GetMetaShared(id)
	if meta == nil || meta.BrowserID != browserID {
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
		// Shared read: only the values needed for the bubbles are copied out of
		// the conversation; components/quickReplies are rendered read-only.
		convo := s.config.SessionManager.GetConversationShared(id)
		if convo != nil {
			// Collect every assistant message since the last user message that has
			// user-visible content. A multi-turn run commits one message per turn
			// (ADR-0028 per-turn live-sync), so the final render must reproduce ALL
			// of them as separate bubbles — not just the last one — or intermediate
			// turns disappear from the live view until a page refresh. Tool-call-only
			// assistant messages (empty content, no components, no quick replies) are
			// mid-run tool commands, not user-visible replies, and are skipped.
			//
			// Keying off "after the last user message" also guards stale content: a
			// run that produced no text (e.g. a tool-only run) still fires a "done"
			// SSE event, and here it simply produces zero bubbles instead of
			// re-rendering the previous run's assistant bubble.
			lastUserIdx := -1
			for i := len(convo.Messages) - 1; i >= 0; i-- {
				if convo.Messages[i].Role == "user" {
					lastUserIdx = i
					break
				}
			}

			renderedAny := false
			for i := lastUserIdx + 1; i < len(convo.Messages); i++ {
				m := convo.Messages[i]
				if m.Role != "assistant" {
					continue
				}
				// Skip tool-call-only assistant messages (no user-visible output).
				if strings.TrimSpace(m.Content) == "" && len(m.Components) == 0 && len(m.QuickReplies) == 0 {
					continue
				}
				content := m.Content
				if hasMermaidComponent(m.Components) {
					content = stripMermaidCodeBlocks(content)
				}
				contentHTML := renderMarkdownToHTML(content)
				// Only inline components that belong inside the assistant bubble.
				// MermaidDiagram is the visual output of the LLM response and belongs inline.
				componentsHTML := renderInlineComponentsToHTML(r.Context(), id, m.Components)
				if componentsHTML != "" {
					contentHTML += "\n" + componentsHTML
				}
				component := templates.AssistantBubble(id, contentHTML, m.QuickReplies)
				component.Render(r.Context(), w)
				renderedAny = true
			}
			// No visible assistant output since the last user message — nothing to
			// render (tool-only run). Return an empty response.
			if !renderedAny {
				w.WriteHeader(http.StatusOK)
				return
			}
		}

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

		case "Screenshot":
			sessionID := id
			filename := ""
			timestamp := ""
			if req.Data != nil {
				if f, ok := req.Data["filename"].(string); ok {
					filename = f
				}
				if t, ok := req.Data["timestamp"].(string); ok {
					timestamp = t
				}
			}
			component := templates.ScreenshotDisplay(sessionID, filename, timestamp)
			component.Render(r.Context(), w)

		default:
			http.Error(w, "Unknown component", http.StatusBadRequest)
		}

	default:
		http.Error(w, "Unknown render kind", http.StatusBadRequest)
	}
}

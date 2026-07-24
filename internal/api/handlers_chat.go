package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/glemsom/eitri/internal/runner"

	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/runstate"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Parse message
	if err := r.ParseForm(); err != nil {
		if isRequestTooLarge(err) {
			writeRequestTooLarge(w)
			return
		}
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	message := r.FormValue("message")
	if message == "" {
		http.Error(w, "Message required", http.StatusBadRequest)
		return
	}

	_ = s.refreshSkillsRegistry()

	// Check for slash commands
	slashResult, slashErr := skills.ParseSlashInput(message, func(name string) *skills.Skill {
		return s.config.SkillsService.Lookup(name)
	})
	if slashErr != nil {
		// Unknown slash command
		if _, ok := slashErr.(*skills.UnknownCommandError); ok {
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Header().Set("Content-Type", "text/html")
			component := templates.ErrorToast(slashErr.Error())
			component.Render(r.Context(), w)
			return
		}
	}

	// Activate skills from slash command
	var justActivatedSkills []string
	if slashResult != nil && len(slashResult.ActivatedSkills) > 0 {
		for _, skillName := range slashResult.ActivatedSkills {
			if s.config.SessionManager.ActivateSkill(id, skillName) {
				justActivatedSkills = append(justActivatedSkills, skillName)
			}
		}
	}

	// Determine the actual prompt to send

	prompt := message
	if slashResult != nil && slashResult.Prompt != "" {
		prompt = slashResult.Prompt
	} else if slashResult != nil && slashResult.IsSlashOnly && len(slashResult.ActivatedSkills) > 0 {
		// Slash-only activation: use skill names as the user prompt
		prompt = strings.Join(slashResult.ActivatedSkills, " ")
	}

	cfgState := s.loadConfigState(r.Context())
	if !cfgState.valid() {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusUnprocessableEntity)
		component := templates.ErrorToast(cfgState.err.Error())
		component.Render(r.Context(), w)
		return
	}
	cmdTimeout := time.Duration(cfgState.cfg.CommandTimeout)
	runCfg := runner.FromConfig(cfgState.cfg, sess.Workspace, cmdTimeout)

	// Check for active run (concurrent run protection)
	if s.config.RunService.ActiveRun(id) != nil {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("HX-Retarget", "#error-toasts")
		w.WriteHeader(http.StatusOK)
		component := templates.ErrorToast("This session already has an active run. Wait for it to complete or cancel it.")
		component.Render(r.Context(), w)
		return
	}

	// Append user message to session
	s.config.SessionManager.AppendMessage(id, session.Message{
		Role:      "user",
		Content:   prompt,
		CreatedAt: time.Now(),
	})

	// Start run in background
	// Use context.Background() instead of r.Context() so the run survives
	// the HTTP handler returning (which cancels the request context).
	// Cancel() provides explicit cancellation via state.Cancel().
	skillWarnings, err := s.config.RunService.StartRun(context.Background(), id, prompt, runCfg)
	if err != nil {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusInternalServerError)
		component := templates.ErrorToast(err.Error())
		component.Render(r.Context(), w)
		return
	}

	// Render skill warnings
	for _, warning := range skillWarnings {
		_ = templates.ErrorToast(warning).Render(r.Context(), w)
	}

	// StartRun spawns agent loop goroutine; it handles status + session persistence internally.
	s.config.SessionManager.UpdateStatus(id, session.StatusRunning)

	// Broadcast run-started status to browser subscribers for real-time sidebar update
	if s.config.RunService != nil {
		sess := s.config.SessionManager.Get(id)
		if sess != nil && sess.BrowserID != "" {
			s.config.RunService.BroadcastToBrowser(sess.BrowserID, runner.BrowserEvent{
				Type: "session_status",
				Data: map[string]any{
					"session_id": id,
					"status":     string(session.StatusRunning),
				},
			})
		}
	}

	// Render user bubble + session tab refresh + send JS events for SSE connect and run state
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("HX-Trigger", `{"eitri:connectRunStream":"`+id+`","eitri:runStarted":"`+id+`"}`)

	sessions := s.config.SessionManager.ListByBrowser(browserID)
	_ = templates.UserBubble(renderMarkdownToHTML(message)).Render(r.Context(), w)
	_ = templates.SessionTabs(sessions, id, true).Render(r.Context(), w)

	// Render OOB-active-skill-chips swap for newly activated skills
	if len(justActivatedSkills) > 0 {
		_ = templates.ActiveSkillChips(sess.ActiveSkills, true).Render(r.Context(), w)
	}
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	if s.config.RunService == nil {
		s.notifyNoActiveRun(w, r, id)
		return
	}

	subscriberID, sseCh, ok := s.config.RunService.Subscribe(id)
	if !ok {
		s.notifyNoActiveRun(w, r, id)
		return
	}
	defer s.config.RunService.Unsubscribe(id, subscriberID)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send initial connecting event
	if initData := mustJSON(runstate.SSEEvent{Type: "connecting"}); initData != nil {
		fmt.Fprintf(w, "data: %s\n\n", string(initData))
		flusher.Flush()
	}

	ctx := r.Context()
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case evt, ok := <-sseCh:
			if !ok {
				return
			}
			data := mustJSON(evt)
			if data == nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", string(data))
			flusher.Flush()
			if evt.Type == "done" || evt.Type == "error" || evt.Type == "closed" {
				return
			}

		case <-keepAlive.C:
			// SSE keep-alive comment
			fmt.Fprintf(w, ":keepalive\n\n")
			flusher.Flush()

		case <-ctx.Done():
			return
		}
	}
}

// notifyNoActiveRun sends a valid SSE response indicating no active run exists.
// This lets the client gracefully handle the case without retry storms.
func (s *Server) notifyNoActiveRun(w http.ResponseWriter, r *http.Request, sessionID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if data := mustJSON(runstate.SSEEvent{Type: "no_active_run", Message: "No active run for session " + sessionID}); data != nil {
		fmt.Fprintf(w, "data: %s\n\n", string(data))
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	browserID := s.browserIDFromRequest(r)

	sess := s.config.SessionManager.Get(id)
	if sess == nil || sess.BrowserID != browserID {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	s.config.RunService.Cancel(id)
	s.config.SessionManager.UpdateStatus(id, session.StatusIdle)

	// Broadcast session status update for real-time sidebar refresh
	if s.config.RunService != nil {
		if sess.BrowserID != "" {
			s.config.RunService.BroadcastToBrowser(sess.BrowserID, runner.BrowserEvent{
				Type: "session_status",
				Data: map[string]any{
					"session_id": id,
					"status":     string(session.StatusIdle),
				},
			})
		}
	}

	// Re-enabling is now client-side via CSS class toggle (issue #103).
	// Return empty 200 so HTMX does not swap out the composer.
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleBrowserEvents(w http.ResponseWriter, r *http.Request) {
	browserID := s.browserIDFromRequest(r)
	if browserID == "" {
		http.Error(w, "No browser ID", http.StatusUnauthorized)
		return
	}

	if s.config.RunService == nil {
		http.Error(w, "Service not available", http.StatusInternalServerError)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	subscriberID, ch := s.config.RunService.SubscribeBrowser(browserID)
	defer s.config.RunService.UnsubscribeBrowser(browserID, subscriberID)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send initial connected event
	fmt.Fprintf(w, "data: %s\n\n", string(mustJSON(runner.BrowserEvent{Type: "connected"})))
	flusher.Flush()

	ctx := r.Context()
	keepAlive := time.NewTicker(30 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return
			}
			data := mustJSON(evt)
			if data == nil {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", string(data))
			flusher.Flush()

		case <-keepAlive.C:
			fmt.Fprintf(w, ":keepalive\n\n")
			flusher.Flush()

		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) handleSessionTabs(w http.ResponseWriter, r *http.Request) {
	browserID := s.browserIDFromRequest(r)
	if browserID == "" {
		http.Error(w, "No browser ID", http.StatusUnauthorized)
		return
	}

	sessions := s.config.SessionManager.ListByBrowser(browserID)

	// Determine active session from query param or last active
	activeID := r.URL.Query().Get("active")
	if activeID == "" {
		if last := s.config.SessionManager.LastActive(browserID); last != nil {
			activeID = last.ID
		}
	}

	w.Header().Set("Content-Type", "text/html")
	component := templates.SessionTabs(sessions, activeID, true)
	component.Render(r.Context(), w)
}

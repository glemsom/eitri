// Package api provides the HTTP server, SSE infrastructure, HTMX/Templ rendering,
// and route registration for the Eitri browser UI.
//
// It is the outermost layer of the application — it wires together the service
// interfaces from config, session, skills, runner, and provider into a cohesive
// HTTP+SSE surface that a browser (or any HTTP client) can interact with.
//
// # Key types
//
//   - Server — wraps the Go 1.22+ http.ServeMux and injected dependencies.
//     Created via NewServer(ServerConfig).
//   - ServerConfig — holds all service dependencies (SessionManager, RunService,
//     SkillsService, DebugRecorder, etc.) plus bootstrap parameters (ConfigPath,
//     Workspace, Version, StartTime).
//   - GitHubCopilotOAuthConfig — OAuth configuration for GitHub Copilot
//     device-code flow authentication (defined in copilot_device_flow.go).
//
// # Route contract summary
//
// All routes are registered in Server.registerRoutes() using Go 1.22+ ServeMux
// pattern syntax ("METHOD /path"). The route table is grouped as follows:
//
//	Health:          GET  /health
//	Static assets:   GET  /static/*       (embedded in assets.Files via embed.FS)
//	Root page:       GET  /{$}            (serves the base HTML shell)
//	Sessions:        POST /api/sessions   (create)
//	                 GET  /sessions/{id}  (view)
//	                 DELETE /api/sessions/{id}
//	Settings:        GET  /settings
//	                 GET  /api/config
//	                 PUT  /api/config
//	                 GET  /api/models
//	Agent run+SSE:   POST /api/sessions/{id}/chat    (send message, start run)
//	                 GET  /api/sessions/{id}/stream   (SSE event stream)
//	                 POST /api/sessions/{id}/cancel   (cancel running agent)
//	                 POST /api/sessions/{id}/render   (render UI component)
//	Confirm:         POST /api/sessions/{id}/confirm
//	Skills:          GET  /skills
//	                 GET  /api/skills
//	                 POST /api/skills/refresh
//	                 GET  /api/sessions/{id}/complete/skills
//	                 GET  /api/sessions/{id}/complete/files
//	                 POST /api/sessions/{id}/skills/{name}/activate
//	                 GET  /api/sessions/{id}/skills/chips
//	                 POST /api/skills/{name}/disable|enable
//	                 POST /api/skills/disable-all|enable-all
//	Browser events:  GET  /api/events               (browser-level SSE for real-time UI)
//	Session tabs:    GET  /api/session-tabs          (sidebar tabs fragment)
//	Debug:           GET  /api/debug                 (umbrella page)
//	                 GET  /api/debug/sessions
//	                 GET  /api/debug/sessions/{id}
//	                 GET  /api/debug/runtime
//	                 GET  /api/debug/config
//	                 GET  /api/debug/health
//	                 GET  /api/debug/http
//	                 GET  /api/debug/http/{trace_id}
//	Copilot OAuth:   POST /api/providers/github_copilot/device-flow/start
//	                 GET  /api/providers/github_copilot/device-flow/{id}
//	                 DELETE /api/providers/github_copilot/device-flow/{id}
//
// # SSE event format
//
// The agent SSE stream (GET /api/sessions/{id}/stream) emits data-only
// newline-delimited events:
//
//	data: {"type":"token","content":"Hello"}\n\n
//	data: {"type":"tool_call","tool":"bash","args":{...}}\n\n
//	data: {"type":"tool_result","tool":"bash","output":{...}}\n\n
//	data: {"type":"component","kind":"tool_card","content":"..."}\n\n
//	data: {"type":"done","usage":{"total_tokens":123}}\n\n
//	data: {"type":"error","message":"..."}\n\n
//	data: {"type":"closed","message":"..."}\n\n
//
// The browser-level event stream (GET /api/events) emits BrowserEvent JSON:
//
//	data: {"type":"connected"}\n\n
//	data: {"type":"session_status","data":{"session_id":"...","status":"running"}}\n\n
//
// Keep-alive comments (:keepalive\n\n) are sent every 30 seconds on both streams.
//
// # Render endpoint dispatch
//
// POST /api/sessions/{id}/render is called by the Stream browser island
// when an SSE event with kind="tool_card" or kind="component" arrives.
// The render handler dispatches by RenderKind:
//
//   - "tool_card" — renders a <details> element showing tool progress/result
//   - "component" — renders a Templ component fragment (DiffCard, MermaidDiagram,
//     QuickReplies, FileEditCard, ErrorToast, etc.)
//
// # Middleware
//
// Every request passes through two middleware layers (applied in
// Server.withMiddleware):
//
//   - requestLoggingMiddleware — logs method, path, status, duration, session_id
//   - requestBodyLimitMiddleware — rejects POST/PUT bodies larger than 1 MiB
//
// # Templates and assets
//
//   - internal/api/templates/ — Templ source files (.templ) and generated Go
//     code (_templ.go). Defines the page shell (base.templ), chat UI, settings,
//     skills page, and all render components.
//   - internal/api/assets/ — frontend assets embedded via embed.FS at compile
//     time. Includes HTMX, Prism.js, KaTeX, Mermaid, custom CSS, and JS islands
//     (eitri-stream.js, eitri-composer.js, eitri-context.js, etc.).
//
// # Dependencies
//
// This package imports from the following internal packages:
//
//   - config    — Config load/save lifecycle (for auth persistence callback)
//   - session   — UISession model and Manager
//   - skills    — Skill discovery, activation, slash-command parsing
//   - runner    — RunService (agent loop lifecycle, SSE broadcast, cancel)
//   - provider  — Provider auth, model discovery, GitHub Copilot OAuth config
//   - debug     — HTTP trace recorder
//   - runstate  — SSE event types (SSEEvent, RenderKind)
//   - fileutil  — (transitive through markdown rendering)
//
// # Extension points
//
//  1. Adding a new API route:
//     Add a handler method on *Server, then register it in registerRoutes()
//     using Go 1.22+ "METHOD /path" syntax.
//
//  2. Adding a new generative UI component:
//     a) Define a RenderKind constant in internal/runstate/runstate.go.
//     b) Create a Templ component in internal/api/templates/.
//     c) Add a dispatch case in the render handler (handlers_chat.go handleRender).
//     d) Optionally add a browser island JS file in internal/api/assets/.
//
//  3. Adding a new browser island:
//     a) Place a .js file in internal/api/assets/ (referenced from the
//     embedded embed.FS).
//     b) Load it in the appropriate Templ template via <script src="...">.
//     c) Use hx-trigger or EventSource to attach to server events.
package api

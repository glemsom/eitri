// Package main (cmd/eitri) is the Eitri server entry point.
//
// It owns HTTP server startup, graceful shutdown, signal handling, and the
// "batch mode" headless runner (invoked with -b). Service wiring (session
// managers, run service, skills service, debug recorder) is done here.
//
// Key responsibilities:
//   - Parse flags (--version, -b)
//   - Load config from ~/.eitri/config.json or EITRI_CONFIG
//   - Wire up and start the HTTP+SSE server
//   - Handle graceful shutdown via SIGINT/SIGTERM
//   - Run batch mode when -b is set (headless, no UI)
//   - Capture crash dumps on fatal errors
//
// Dependencies (all internal/ packages):
//   - api (HTTP server, SSE, rendering)
//   - config (config load/save/validate)
//   - history (conversation history manager)
//   - debug (crash dump writer, HTTP trace recorder)
//   - runner (run lifecycle, agent loop)
//   - session (browser UI session manager)
//   - skills (Agent Skills service)
package main

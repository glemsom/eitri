package tools

import (
	"context"
	"fmt"
	"path/filepath"
)

// Deps carries the per-session wiring the registry (and hence every tool)
// needs: the workspace, the session temp host root, the GUID that namespaces
// /tmp, configured extra writable paths, the sandbox runner, and the network
// and browser seams. One registry is built per run and shared across TUI and
// batch.
type Deps struct {
	Workspace     string
	TempHost      string
	GUID          GUID
	ExtraWritable []string
	Runner        Runner
	Fetcher       Fetcher
	Browser       BrowserLauncher

	// Skills is the pre-discovered, filtered skill catalog for this run (T8).
	// When non-nil and non-empty, the dedicated `skill` tool is registered and
	// its `name` enum is the catalog's valid names. When empty, the skill tool
	// is omitted entirely.
	Skills *Catalog
}

// Tool is one agent-callable function. Name must match the registry key; Run
// receives a JSON-decoded argument map and returns a deterministic, reversible
// string result suitable for future compression/compaction. Compressed reports
// whether the result is the line-compressor's truncated form: bash
// compresses at the tool boundary and sets it true when the
// never-inflate gate actually shortened the output; every other tool returns
// raw bytes and sets it false, so the engine's byte-cap never mistakes raw
// content that LOOKS like a "+N more" tail for the compressor's marker.
// Schema returns the tool's strict-shaped JSON-Schema argument object
// (additionalProperties:false, required fields) so a caller can build the
// per-dialect tool definition.
type ToolResult struct {
	// Text is the tool's result string.
	Text string
	// Compressed is true when Text is the line-compressor's compressed form
	// (carries an explicit "+N more" tail marker).
	Compressed bool
}

type Tool interface {
	Name() string
	Description() string
	Schema() map[string]any
	Run(ctx context.Context, args map[string]any) (ToolResult, error)
}

// Definition is a tool's provider-facing definition: name, description, and a
// minimal JSON-Schema parameters object (strict-shaped).
type Definition struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// Definitions returns the registry's fixed tool surface as provider-facing
// definitions (name/description/JSON-Schema). The stable ordering and content
// keep the request head byte-stable so a later prompt cache can hit.
func (r *Registry) Definitions() []Definition {
	names := r.Names()
	out := make([]Definition, 0, len(names))
	for _, n := range names {
		t := r.tools[n]
		out = append(out, Definition{Name: n, Description: t.Description(), Parameters: t.Schema()})
	}
	return out
}

// Browser exposes the registry's host-side browser launch seam (the launcher
// backing the open_in_browser tool).
func (r *Registry) Browser() BrowserLauncher { return r.browser }

// Registry is the shared tool registry: it wires the single PathTranslator,
// the write-side Validator once, plus the network and browser seams, then
// exposes the fixed tool surface.
type Registry struct {
	tr        *PathTranslator
	val       *Validator
	sandbox   *Sandbox
	browser   BrowserLauncher
	workspace string
	tools     map[string]Tool
}

// NewRegistry builds the registry for one session from Deps.
func NewRegistry(d Deps) *Registry {
	if d.Fetcher == nil {
		d.Fetcher = httpFetcher{}
	}
	if d.Browser == nil {
		d.Browser = xdgBrowser{}
	}
	r := &Registry{
		tr:        NewPathTranslator(d.GUID),
		browser:   d.Browser,
		workspace: filepath.Clean(d.Workspace),
		tools:     map[string]Tool{},
	}
	r.val = NewValidator(d.Workspace, d.ExtraWritable, r.tr)
	r.sandbox = NewSandbox(d.Workspace, d.TempHost, d.Runner)
	r.tools["bash"] = &bashTool{sb: r.sandbox}
	r.tools["read"] = &readTool{tr: r.tr, workspace: r.workspace}
	r.tools["write"] = &writeTool{val: r.val}
	r.tools["edit"] = &editTool{val: r.val}
	r.tools["web_fetch"] = &webFetchTool{f: d.Fetcher}
	r.tools["open_in_browser"] = &openInBrowserTool{br: d.Browser, tr: r.tr}
	if d.Skills != nil && len(d.Skills.Names()) > 0 {
		r.tools["skill"] = &skillTool{c: d.Skills}
	}
	return r
}

// Names returns the registered tool names in stable order. The skill tool
// appears only when skills were discovered and passed through Deps.Skills.
func (r *Registry) Names() []string {
	base := []string{"bash", "read", "write", "edit", "web_fetch", "open_in_browser"}
	if _, ok := r.tools["skill"]; ok {
		return append(base, "skill")
	}
	return base
}

// PathTranslator returns the shared translation seam (exposed for host-side
// launch points like open_in_browser and for tests).
func (r *Registry) PathTranslator() *PathTranslator { return r.tr }

// Workspace returns the workspace root (host-absolute, cleaned): the writable
// root write/edit validate against and the resolve base for read's
// workspace-relative paths.
// It is exposed for host-side telemetry (e.g. the TUI file line-delta seam).
func (r *Registry) Workspace() string { return r.workspace }

// Run executes the named tool with the given decoded args, returning its
// result string plus whether the result is the line-compressor's compressed
// form. Unknown tools are a hard error.
func (r *Registry) Run(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
	tool, ok := r.tools[name]
	if !ok {
		return ToolResult{}, fmt.Errorf("unknown tool %q", name)
	}
	return tool.Run(ctx, args)
}

// helper: strArg extracts a required string argument, enforcing presence.
func strArg(args map[string]any, key string) (string, error) {
	v, ok := args[key]
	if !ok {
		return "", fmt.Errorf("missing required argument %q", key)
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", fmt.Errorf("argument %q must be a non-empty string", key)
	}
	return s, nil
}

// strictSchema builds a strict-shaped JSON-Schema object:
// additionalProperties:false and every field required, emulating optionals with
// unions when needed later. This is the canonical per-tool schema target T5
// re-expresses per dialect.
func strictSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

// optStr extracts an optional string argument. Unlike strArg it permits an
// empty value (e.g. an empty new_string in an edit that deletes text); the
// key must still be present and a string.
func optStr(args map[string]any, key string) (string, error) {
	v, ok := args[key]
	if !ok {
		return "", fmt.Errorf("missing required argument %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("argument %q must be a string", key)
	}
	return s, nil
}

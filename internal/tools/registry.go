package tools

import (
	"context"
	"fmt"
	"path/filepath"
)

// Deps carries the per-session wiring the registry (and hence every tool) needs: the workspace, the session temp host root, configured extra writable paths, the sandbox runner, the browser seam, and the skill catalog backing the human /skillname slash surface.
type Deps struct {
	Workspace     string
	TempHost      string
	GUID          GUID
	ExtraWritable []string
	Runner        Runner
	Browser       BrowserLauncher

	Skills *Catalog
}

// Tool is one agent-callable function.
type ToolResult struct {
	Text       string
	Compressed bool
}

type Tool interface {
	Name() string
	Description() string
	Schema() map[string]any
	Run(ctx context.Context, args map[string]any) (ToolResult, error)
}

// Definition is a tool's provider-facing definition: name, description, and a minimal JSON-Schema parameters object (strict-shaped).
type Definition struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// Definitions returns the registry's fixed tool surface as provider-facing definitions (name/description/JSON-Schema).
func (r *Registry) Definitions() []Definition {
	names := r.Names()
	out := make([]Definition, 0, len(names))
	for _, n := range names {
		t := r.tools[n]
		out = append(out, Definition{Name: n, Description: t.Description(), Parameters: t.Schema()})
	}
	return out
}

// Registry is the shared tool registry: it wires the single PathTranslator plus the network and browser seams, then exposes the fixed tool surface. It also holds the skill catalog that backs only the human /skillname slash surface.
type Registry struct {
	tr        *PathTranslator
	sandbox   *Sandbox
	browser   BrowserLauncher
	workspace string
	tools     map[string]Tool
	catalog   *Catalog
}

// NewRegistry builds the registry for one session from Deps.
func NewRegistry(d Deps) *Registry {
	if d.Browser == nil {
		d.Browser = xdgBrowser{}
	}
	r := &Registry{
		tr:        NewPathTranslator(),
		browser:   d.Browser,
		workspace: filepath.Clean(d.Workspace),
		tools:     map[string]Tool{},
	}
	r.sandbox = NewSandbox(d.Workspace, d.TempHost, d.Runner, d.ExtraWritable...)
	r.tools["bash"] = &bashTool{sb: r.sandbox}
	r.tools["open_in_browser"] = &openInBrowserTool{br: d.Browser, tr: r.tr}

	// Skills back the human /skillname slash surface and, via RenderIndex, feed
	// the model a name/path/description index; the model has no `skill` tool and
	// loads pack bodies itself via `bash cat` (see the system prompt).
	r.catalog = d.Skills
	return r
}

func (r *Registry) Names() []string {
	return []string{"bash", "open_in_browser"}
}

func (r *Registry) Workspace() string { return r.workspace }

// Run executes the named tool with the given decoded args, returning its result string plus whether the result is the line-compressor's compressed form.
func (r *Registry) Run(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
	tool, ok := r.tools[name]
	if !ok {
		return ToolResult{}, fmt.Errorf("unknown tool %q", name)
	}
	return tool.Run(ctx, args)
}

// ActivateSkill renders the named skill's payload for the TUI's human
// `/skillname` slash surface. Every activation re-applies the full payload: a
// human re-invoke is an explicit command, never short-circuited. The model has
// no `skill` tool; it sees a name/path/description index and loads pack bodies
// itself via `bash cat` (see the system prompt).
func (r *Registry) ActivateSkill(_ context.Context, name string) (ToolResult, error) {
	if r.catalog == nil || len(r.catalog.Names()) == 0 {
		return ToolResult{}, fmt.Errorf("no skills configured")
	}
	sk := r.catalog.Skill(name)
	if sk == nil {
		return ToolResult{}, fmt.Errorf("unknown skill %q", name)
	}
	return ToolResult{Text: renderSkillPayload(name, sk)}, nil
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

// strictSchema builds a strict-shaped JSON-Schema object: additionalProperties:false and a required list carrying only the genuinely-mandatory fields; optional fields are declared as ordinary properties and may be omitted by a caller (null values are still tolerated at run time by the argument readers).
func strictSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

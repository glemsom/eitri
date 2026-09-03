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
	ExtraWritable []string
	Runner        Runner
	Browser       BrowserLauncher

	Skills *Catalog
}

type ToolResult struct {
	Text       string
	Compressed bool
	Dropped    int
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

// Registry owns the fixed tool surface and the skill catalog used by human activation and the model-visible index.
type Registry struct {
	sandbox   *Sandbox
	workspace string
	tools     map[string]Tool
	catalog   *Catalog
}

// NewRegistry builds the registry for one session from Deps.
func NewRegistry(d Deps) (*Registry, error) {
	if d.Browser == nil {
		d.Browser = xdgBrowser{}
	}
	r := &Registry{
		workspace: filepath.Clean(d.Workspace),
		tools:     map[string]Tool{},
	}
	var err error
	r.sandbox, err = NewSandbox(d.Workspace, d.TempHost, d.Runner, d.ExtraWritable...)
	if err != nil {
		return nil, fmt.Errorf("build tool registry: %w", err)
	}
	r.tools["bash"] = &bashTool{sb: r.sandbox}
	r.tools["open_in_browser"] = &openInBrowserTool{br: d.Browser}

	// Skills back the human /skillname slash surface and, via RenderIndex, feed
	// the model a name/path/description index; the model has no `skill` tool and
	// loads pack bodies itself via `bash cat` (see the system prompt).
	r.catalog = d.Skills
	return r, nil
}

func (r *Registry) Names() []string {
	return []string{"bash", "open_in_browser"}
}

func (r *Registry) Workspace() string { return r.workspace }

// SetTempHost rewires the per-session temp directory used by sandboxed tools.
func (r *Registry) SetTempHost(tempHost string) error {
	if tempHost == "" {
		return fmt.Errorf("session temp path is empty")
	}
	if !filepath.IsAbs(tempHost) {
		return fmt.Errorf("session temp path must be absolute: %q", tempHost)
	}
	r.sandbox.tempHost = filepath.Clean(tempHost)
	return nil
}

// TempHost returns the per-session temp directory used by sandboxed tools.
func (r *Registry) TempHost() string { return r.sandbox.tempHost }

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

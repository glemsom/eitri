package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/voocel/litellm"
)

// Registry is a dispatch map of tool handlers by name.
type Registry struct {
	handlers map[string]ToolHandler
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]ToolHandler)}
}

// Register adds a tool handler to the registry. Panics if name is empty or
// a handler with the same name is already registered.
func (r *Registry) Register(h ToolHandler) {
	if h.Name() == "" {
		panic("tool: cannot register handler with empty name")
	}
	if _, exists := r.handlers[h.Name()]; exists {
		panic(fmt.Sprintf("tool: handler %q already registered", h.Name()))
	}
	r.handlers[h.Name()] = h
}

// Lookup returns the handler for the given name, or nil if not found.
func (r *Registry) Lookup(name string) ToolHandler {
	return r.handlers[name]
}

// Dispatch looks up the tool by name and calls it.
//
// If the tool is not found, it returns a Go error (which terminates the loop).
// If the tool executes but returns an error, the result is wrapped as a
// ToolResultBlock with IsError=true and returned as blocks (so the LLM sees it).
//
// On success, the returned blocks are wrapped in a ToolResultBlock.
func (r *Registry) Dispatch(ctx context.Context, toolUseID, name string, args json.RawMessage) ([]litellm.Block, error) {
	h := r.Lookup(name)
	if h == nil {
		return nil, fmt.Errorf("unknown tool: %q", name)
	}

	blocks, err, isError := h.Call(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("tool %q: %w", name, err)
	}

	// Always wrap in ToolResultBlock for the agent loop
	result := []litellm.Block{
		litellm.ToolResultBlock{
			ToolUseID: toolUseID,
			Content:   blocks,
			IsError:   isError,
		},
	}
	return result, nil
}

// Names returns all registered tool names, sorted alphabetically.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.handlers))
	for name := range r.handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// All returns all registered handlers.
func (r *Registry) All() []ToolHandler {
	all := make([]ToolHandler, 0, len(r.handlers))
	for _, h := range r.handlers {
		all = append(all, h)
	}
	return all
}

// LitellmTools converts all registered handlers to litellm.Tool definitions
// for use in LLM requests.
func (r *Registry) LitellmTools() []litellm.Tool {
	tools := make([]litellm.Tool, 0, len(r.handlers))
	for _, h := range r.handlers {
		tools = append(tools, litellm.Tool{
			Name:        h.Name(),
			Description: h.Description(),
			Parameters:  h.JSONSchema(),
		})
	}
	return tools
}

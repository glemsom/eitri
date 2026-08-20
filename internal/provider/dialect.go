package provider

// Dialect names the Chat-Completions-style tool dialects Eitri re-expresses a canonical schema into.
type Dialect string

const (
	DialectChat      Dialect = "chat"
	DialectAnthropic Dialect = "anthropic"
)

// DialectDefinition is one tool's canonical, provider-agnostic form.
type DialectDefinition struct {
	Name        string
	Description string
	Schema      map[string]any
}

// AnthropicTool is the Anthropic Messages tool wrapper (name, description, input_schema + strict).
type AnthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	Strict      bool           `json:"strict,omitempty"`
}

// ReExpress turns the canonical definitions into a per-dialect tool manifest.
func ReExpress(defs []DialectDefinition, d Dialect) any {
	switch d {
	case DialectChat:
		out := make([]Tool, 0, len(defs))
		for _, def := range defs {
			out = append(out, Tool{
				Type: "function",
				Function: ToolFunction{
					Name:        def.Name,
					Description: def.Description,
					Parameters:  def.Schema,
				},
			})
		}
		return out
	case DialectAnthropic:
		out := make([]AnthropicTool, 0, len(defs))
		for _, def := range defs {
			out = append(out, AnthropicTool{
				Name:        def.Name,
				Description: def.Description,
				InputSchema: def.Schema,
			})
		}
		return out
	default:
		return nil
	}
}

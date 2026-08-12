package provider

// Dialect re-expression: one canonical JSON-Schema per tool is authored once
// (in the tool registry) and serialized here into each wire dialect's tool
// wrapper (docs/spec.md §2, ticket #10). Never author per-dialect copies — the
// same schema map feeds every wrapper, so the strict-shape guarantee holds on
// every transport. T11 routes real requests through this same layer; today the
// tests assert the emitted wrappers.

// Dialect names the Chat-Completions-style tool dialects Eitri re-expresses a
// canonical schema into. Anthropic (`/v1/messages`) is the reference
// alternative to Chat Completions; primary routing in T11 adds Responses.
type Dialect string

const (
	// DialectChat is OpenAI Chat Completions: schema lives under
	// function.parameters (the primary deepseek-v4-flash path).
	DialectChat Dialect = "chat"
	// DialectAnthropic is Anthropic Messages: schema lives under
	// input_schema beside a strict flag.
	DialectAnthropic Dialect = "anthropic"
)

// DialectDefinition is one tool's canonical, provider-agnostic form. Schema is
// the strict-shaped JSON-Schema (additionalProperties:false, all-required,
// nullable unions for optionals) and is the single source for every dialect.
type DialectDefinition struct {
	Name        string
	Description string
	Schema      map[string]any
}

// AnthropicTool is the Anthropic Messages tool wrapper (name, description,
// input_schema + strict). Schema is the same canonical map given to Chat.
type AnthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	Strict      bool           `json:"strict,omitempty"`
}

// ReExpress turns the canonical definitions into a per-dialect tool manifest.
// The canonical Schema map is referenced, not copied, so all dialects share
// the identical strict-shape surface. Unsupported dialects return nil.
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

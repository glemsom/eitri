package provider

// DialectDefinition is one tool's canonical, provider-agnostic form.
type DialectDefinition struct {
	Name        string
	Description string
	Schema      map[string]any
}

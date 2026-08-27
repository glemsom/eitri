package provider

import (
	"encoding/json"
	"io"
)

// compile-time proof that the Copilot chat dialect implements the Dialect seam.
var _ Dialect = (*CopilotChatDialect)(nil)

// CopilotChatDialect is the Dialect implementation for the Copilot chat
// wire: a Chat-Completions shape that, unlike the shared chat dialect, carries
// the DeepSeek thinking toggle explicitly in both directions — enabled while
// the caller keeps thinking on and disabled when thinking is off.
type CopilotChatDialect struct{}

// NewCopilotChatDialect returns the Copilot chat dialect.
func NewCopilotChatDialect() *CopilotChatDialect {
	return &CopilotChatDialect{}
}

// Build implements Dialect.
func (d *CopilotChatDialect) Build(req Request) ([]byte, error) {
	return json.Marshal(chatCompletionBody{
		Model:           req.Model,
		Messages:        req.Messages,
		Tools:           toolsForWire(req),
		ToolChoice:      req.ToolChoice,
		Stream:          true,
		StreamOptions:   &streamOptions{IncludeUsage: true},
		Thinking:        copilotThinkingControl(req),
		ReasoningEffort: reasoningEffortControl(req),
		MaxOutputTokens: maxOutputTokens(req),
	})
}

// Capabilities implements Dialect, reporting the generation controls the
// Copilot chat wire honors.
func (d *CopilotChatDialect) Capabilities() []GenerationControl {
	return []GenerationControl{
		GenerationControlGenerationBudget,
		GenerationControlThinkingSuppression,
	}
}

// Manifest implements Dialect, re-expressing canonical tool definitions into
// the Chat-Completions function manifest the Copilot wire shares.
func (d *CopilotChatDialect) Manifest(defs []DialectDefinition) any {
	return chatToolManifest(defs)
}

// Stream implements Dialect, parsing the Copilot chat SSE stream into
// provider chunks.
func (d *CopilotChatDialect) Stream(r io.Reader) Stream {
	return &openAIStream{ev: newSSE(r), acc: newToolAccumulator()}
}

// copilotThinkingControl returns the DeepSeek thinking-mode toggle in its
// explicit form for the Copilot wire: enabled when the caller keeps thinking
// on, and disabled when thinking is off.
func copilotThinkingControl(req Request) *thinkingEnabler {
	t := "enabled"
	if !req.ThinkingEnabled {
		t = "disabled"
	}
	return &thinkingEnabler{Type: t}
}

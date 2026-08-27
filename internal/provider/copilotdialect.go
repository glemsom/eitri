package provider

import (
	"encoding/json"
	"io"
)

// compile-time proof that the Copilot chat dialect implements the WireDialect seam.
var _ WireDialect = (*CopilotChatDialect)(nil)

// CopilotChatDialect is the WireDialect implementation for the Copilot chat
// wire: a Chat-Completions shape that, unlike the shared chat dialect, carries
// the DeepSeek thinking toggle explicitly in both directions — enabled while
// the caller keeps thinking on and disabled when thinking is off.
type CopilotChatDialect struct{}

// NewCopilotChatDialect returns the Copilot chat dialect.
func NewCopilotChatDialect() *CopilotChatDialect {
	return &CopilotChatDialect{}
}

// Build implements WireDialect.
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

// Capabilities implements WireDialect, reporting the generation controls the
// Copilot chat wire honors.
func (d *CopilotChatDialect) Capabilities() []GenerationControl {
	return []GenerationControl{
		GenerationControlGenerationBudget,
		GenerationControlThinkingSuppression,
	}
}

// Stream implements WireDialect, parsing the Copilot chat SSE stream into
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

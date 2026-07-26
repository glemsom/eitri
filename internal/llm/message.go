package llm

import "time"

// Message is the canonical message type used throughout the application.
// It combines all fields from the previous llm.Message (wire-format) and
// session.Message (UI) types into a single representation.
//
// All data paths (LLM API → persistence → UI) use this type directly,
// eliminating the need for conversion between parallel message types.
type Message struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	Components       []ComponentData `json:"components,omitempty"`
	QuickReplies     []string        `json:"quick_replies,omitempty"`
}

// ComponentData holds a rendered UI component attached to an assistant message.
type ComponentData struct {
	Name string         `json:"name"`
	Data map[string]any `json:"data"`
}



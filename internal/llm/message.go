package llm

import (
	"time"

	"github.com/glemsom/eitri/internal/message"
)

// ComponentData holds a rendered UI component attached to a message.
// Defined in the internal/message package; this type alias preserves
// compatibility for existing consumers that reference llm.ComponentData.
type ComponentData = message.ComponentData

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



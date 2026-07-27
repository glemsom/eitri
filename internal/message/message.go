// Package message defines the EitriMessage and Message types and conversion
// helpers. EitriMessage wraps litellm.Message with UI-only metadata. Message
// is a flat struct used for serialization and by consumers that need direct
// field access to role, content, and tool call fields.
//
// Migrated from internal/llm (issue #899).
package message

import (
	"encoding/json"
	"time"

	"github.com/voocel/litellm"
)

// ── ComponentData ──────────────────────────────────────────────────────────

// ComponentData holds a rendered UI component attached to a message.
type ComponentData struct {
	Name string         `json:"name"`
	Data map[string]any `json:"data"`
}

// ── ToolCall & FunctionCall ────────────────────────────────────────────────

// ToolCall represents a function call made by the LLM.
type ToolCall struct {
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function FunctionCall `json:"function"`
}

// FunctionCall holds the name and JSON-encoded arguments of a function call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ── Message (flat) ─────────────────────────────────────────────────────────

// Message is the flat conversation message type used for serialization and by
// consumers that need direct field access to role, content, tool calls, and UI
// metadata. It is the historical canonical type; newer code should prefer
// EitriMessage which wraps litellm.Message.
//
// All data paths (LLM API → persistence → UI) can use this type or convert
// from EitriMessage as needed.
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

// ── EitriMessage ───────────────────────────────────────────────────────────

// EitriMessage wraps litellm.Message with UI-only metadata.
type EitriMessage struct {
	litellm.Message
	CreatedAt    time.Time       `json:"created_at"`
	Components   []ComponentData `json:"components,omitempty"`
	QuickReplies []string        `json:"quick_replies,omitempty"`
}

// ToLitellm returns the embedded litellm.Message for transport.
func (m EitriMessage) ToLitellm() litellm.Message {
	return m.Message
}

// Content returns the concatenated text content from all TextBlocks.
func (m EitriMessage) Content() string {
	var content string
	for _, block := range m.Blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			content += b.Text
		case litellm.ToolResultBlock:
			for _, c := range b.Content {
				if text, ok := c.(litellm.TextBlock); ok {
					content += text.Text
				}
			}
		}
	}
	return content
}

// ReasoningContent returns the concatenated reasoning content from all ReasoningBlocks.
func (m EitriMessage) ReasoningContent() string {
	var content string
	for _, block := range m.Blocks {
		if r, ok := block.(litellm.ReasoningBlock); ok {
			content += r.Text
		}
	}
	return content
}

// ToolCallID returns the tool use ID from the first ToolResultBlock, if any.
func (m EitriMessage) ToolCallID() string {
	for _, block := range m.Blocks {
		if tr, ok := block.(litellm.ToolResultBlock); ok {
			return tr.ToolUseID
		}
	}
	return ""
}

// ToolCalls returns all ToolUseBlock entries as []ToolCall (flat format).
func (m EitriMessage) ToolCalls() []ToolCall {
	var out []ToolCall
	for _, block := range m.Blocks {
		if tu, ok := block.(litellm.ToolUseBlock); ok {
			args := ""
			if len(tu.Arguments) > 0 {
				args = string(tu.Arguments)
			}
			out = append(out, ToolCall{
				ID:   tu.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      tu.Name,
					Arguments: args,
				},
			})
		}
	}
	return out
}

// ToMessage converts EitriMessage to the flat Message type.
func (m EitriMessage) ToMessage() Message {
	return Message{
		Role:             string(m.Role),
		Content:          m.Content(),
		ReasoningContent: m.ReasoningContent(),
		ToolCallID:       m.ToolCallID(),
		ToolCalls:        m.ToolCalls(),
		CreatedAt:        m.CreatedAt,
		Components:       m.Components,
		QuickReplies:     m.QuickReplies,
	}
}

// FromLitellm wraps a litellm.Message with zero-value UI fields.
func FromLitellm(msg litellm.Message) EitriMessage {
	return EitriMessage{Message: msg}
}

// ── Conversion: Message ↔ litellm.Message ────────────────────────────────

// ToLitellmMessage converts a flat Message to a litellm.Message.
func ToLitellmMessage(m Message) litellm.Message {
	var blocks []litellm.Block

	// Assistant messages with tool calls get structured blocks
	if m.Role == "assistant" && len(m.ToolCalls) > 0 {
		if m.Content != "" {
			blocks = append(blocks, litellm.TextBlock{Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			args := json.RawMessage(tc.Function.Arguments)
			if !json.Valid(args) {
				args = json.RawMessage("{}")
			}
			blocks = append(blocks, litellm.ToolUseBlock{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: args,
			})
		}
	} else if m.Role == "tool" {
		blocks = append(blocks, litellm.ToolResultBlock{
			ToolUseID: m.ToolCallID,
			Content:   []litellm.Block{litellm.TextBlock{Text: m.Content}},
		})
	} else {
		// System, user, or simple assistant messages
		content := m.Content
		if m.ReasoningContent != "" {
			content = m.ReasoningContent + "\n" + content
		}
		blocks = append(blocks, litellm.TextBlock{Text: content})
	}

	return litellm.Message{
		Role:   litellm.Role(m.Role),
		Blocks: blocks,
	}
}

// FromLitellmMessage converts a litellm.Message to a flat Message.
// UI-only fields (CreatedAt, Components, QuickReplies) are zero-valued
// and should be populated by the caller if needed.
func FromLitellmMessage(msg litellm.Message) Message {
	var content, reasoningContent string
	var toolCalls []ToolCall
	var toolCallID string

	for _, block := range msg.Blocks {
		switch b := block.(type) {
		case litellm.TextBlock:
			content += b.Text
		case litellm.ReasoningBlock:
			reasoningContent += b.Text
		case litellm.ToolUseBlock:
			args := ""
			if len(b.Arguments) > 0 {
				args = string(b.Arguments)
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   b.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      b.Name,
					Arguments: args,
				},
			})
		case litellm.ToolResultBlock:
			toolCallID = b.ToolUseID
			for _, contentBlock := range b.Content {
				if text, ok := contentBlock.(litellm.TextBlock); ok {
					content += text.Text
				}
			}
		}
	}

	return Message{
		Role:             string(msg.Role),
		Content:          content,
		ReasoningContent: reasoningContent,
		ToolCallID:       toolCallID,
		ToolCalls:        toolCalls,
	}
}

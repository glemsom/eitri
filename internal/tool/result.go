package tool

import "github.com/voocel/litellm"

// ToolResult replaces the 3-value return from ToolHandler.Call().
//
// The error return from Call() is now only for Go-level failures (cancel,
// JSON parse, unknown tool). The IsError and NeedsConfirm fields on
// ToolResult signal LLM-facing error vs confirmation-needed vs success.
type ToolResult struct {
	Blocks         []litellm.Block
	IsError        bool   // when true, Blocks are wrapped as ToolResultBlock with IsError=true
	NeedsConfirm   bool   // when true, tool needs user approval before proceeding
	ConfirmPath    string // path requiring confirmation (set when NeedsConfirm=true)
	ConfirmMessage string // message to show to the user (set when NeedsConfirm=true)
}

// Success returns a ToolResult with no error or confirmation flags.
func Success(blocks []litellm.Block) ToolResult {
	return ToolResult{Blocks: blocks}
}

// ToolError returns a ToolResult with IsError=true, signalling an LLM-facing error.
func ToolError(blocks []litellm.Block) ToolResult {
	return ToolResult{Blocks: blocks, IsError: true}
}

// NeedsConfirm returns a ToolResult with NeedsConfirm=true, signalling the tool
// needs user approval before the result can be returned.
func NeedsConfirm(blocks []litellm.Block) ToolResult {
	return ToolResult{Blocks: blocks, NeedsConfirm: true}
}

// NeedsConfirmPath returns a ToolResult with NeedsConfirm=true and the given
// path and message for the confirmation prompt.
func NeedsConfirmPath(blocks []litellm.Block, path, message string) ToolResult {
	return ToolResult{Blocks: blocks, NeedsConfirm: true, ConfirmPath: path, ConfirmMessage: message}
}

// TextResult is a convenience constructor that creates a Success ToolResult
// containing a single TextBlock. If text is empty, Blocks is nil.
func TextResult(text string) ToolResult {
	if text == "" {
		return Success(nil)
	}
	return Success([]litellm.Block{litellm.TextBlock{Text: text}})
}

// TextBlocks creates a []litellm.Block containing a single TextBlock.
// These are plain content blocks; dispatch wraps them in ToolResultBlock.
func TextBlocks(text string) []litellm.Block {
	if text == "" {
		return nil
	}
	return []litellm.Block{litellm.TextBlock{Text: text}}
}

package tool

import (
	"testing"

	"github.com/voocel/litellm"
)

// ── Result constructor tests ───────────────────────────────────────────────

func TestSuccess(t *testing.T) {
	blocks := []litellm.Block{litellm.TextBlock{Text: "ok"}}
	r := Success(blocks)
	if r.IsError {
		t.Error("IsError = true, want false")
	}
	if r.NeedsConfirm {
		t.Error("NeedsConfirm = true, want false")
	}
	if len(r.Blocks) != 1 {
		t.Fatalf("len(Blocks) = %d, want 1", len(r.Blocks))
	}
	tb, ok := r.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", r.Blocks[0])
	}
	if tb.Text != "ok" {
		t.Errorf("Text = %q, want %q", tb.Text, "ok")
	}
}

func TestToolError(t *testing.T) {
	blocks := []litellm.Block{litellm.TextBlock{Text: "error occurred"}}
	r := ToolError(blocks)
	if !r.IsError {
		t.Error("IsError = false, want true")
	}
	if r.NeedsConfirm {
		t.Error("NeedsConfirm = true, want false")
	}
	if len(r.Blocks) != 1 {
		t.Fatalf("len(Blocks) = %d, want 1", len(r.Blocks))
	}
}

func TestNeedsConfirm(t *testing.T) {
	blocks := []litellm.Block{litellm.TextBlock{Text: "confirm this"}}
	r := NeedsConfirm(blocks)
	if r.IsError {
		t.Error("IsError = true, want false")
	}
	if !r.NeedsConfirm {
		t.Error("NeedsConfirm = false, want true")
	}
	if r.ConfirmPath != "" {
		t.Errorf("ConfirmPath = %q, want empty", r.ConfirmPath)
	}
	if r.ConfirmMessage != "" {
		t.Errorf("ConfirmMessage = %q, want empty", r.ConfirmMessage)
	}
	if len(r.Blocks) != 1 {
		t.Fatalf("len(Blocks) = %d, want 1", len(r.Blocks))
	}
}

func TestNeedsConfirmPath(t *testing.T) {
	blocks := []litellm.Block{litellm.TextBlock{Text: "write to path"}}
	r := NeedsConfirmPath(blocks, "/path/to/file", "Allow writing to this path?")
	if r.IsError {
		t.Error("IsError = true, want false")
	}
	if !r.NeedsConfirm {
		t.Error("NeedsConfirm = false, want true")
	}
	if r.ConfirmPath != "/path/to/file" {
		t.Errorf("ConfirmPath = %q, want %q", r.ConfirmPath, "/path/to/file")
	}
	if r.ConfirmMessage != "Allow writing to this path?" {
		t.Errorf("ConfirmMessage = %q, want %q", r.ConfirmMessage, "Allow writing to this path?")
	}
}

func TestTextResult_NonEmpty(t *testing.T) {
	r := TextResult("hello world")
	if r.IsError {
		t.Error("IsError = true, want false")
	}
	if r.NeedsConfirm {
		t.Error("NeedsConfirm = true, want false")
	}
	if len(r.Blocks) != 1 {
		t.Fatalf("len(Blocks) = %d, want 1", len(r.Blocks))
	}
	tb, ok := r.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", r.Blocks[0])
	}
	if tb.Text != "hello world" {
		t.Errorf("Text = %q, want %q", tb.Text, "hello world")
	}
}

func TestTextResult_Empty(t *testing.T) {
	r := TextResult("")
	if r.IsError {
		t.Error("IsError = true, want false")
	}
	if r.NeedsConfirm {
		t.Error("NeedsConfirm = true, want false")
	}
	if r.Blocks != nil {
		t.Errorf("Blocks = %v, want nil for empty string", r.Blocks)
	}
}

func TestTextBlocks_NonEmpty(t *testing.T) {
	blocks := TextBlocks("hello")
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	tb, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	if tb.Text != "hello" {
		t.Errorf("Text = %q, want %q", tb.Text, "hello")
	}
}

func TestTextBlocks_Empty(t *testing.T) {
	blocks := TextBlocks("")
	if blocks != nil {
		t.Errorf("blocks = %v, want nil for empty string", blocks)
	}
}

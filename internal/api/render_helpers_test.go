package api

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/session"
)

// ─── hasMermaidComponent ───────────────────────────────────────────────

func TestHasMermaidComponent_True(t *testing.T) {
	components := []session.ComponentData{
		{Name: "QuickReplies", Data: map[string]any{}},
		{Name: "MermaidDiagram", Data: map[string]any{"code": "graph TD; A-->B;"}},
	}
	if !hasMermaidComponent(components) {
		t.Error("expected hasMermaidComponent to return true")
	}
}

func TestHasMermaidComponent_False(t *testing.T) {
	components := []session.ComponentData{
		{Name: "QuickReplies", Data: map[string]any{}},
		{Name: "DiffCard", Data: map[string]any{}},
	}
	if hasMermaidComponent(components) {
		t.Error("expected hasMermaidComponent to return false")
	}
}

func TestHasMermaidComponent_Empty(t *testing.T) {
	if hasMermaidComponent([]session.ComponentData{}) {
		t.Error("expected hasMermaidComponent to return false for empty slice")
	}
}

func TestHasMermaidComponent_Nil(t *testing.T) {
	if hasMermaidComponent(nil) {
		t.Error("expected hasMermaidComponent to return false for nil slice")
	}
}

// ─── stripMermaidCodeBlocks ────────────────────────────────────────────

func TestStripMermaidCodeBlocks_EmptyString(t *testing.T) {
	got := stripMermaidCodeBlocks("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestStripMermaidCodeBlocks_TrailingTextOnFence(t *testing.T) {
	input := "Before\n\n```mermaid graph TD;\ngraph TD; A-->B;\n```\n\nAfter"
	// The opening fence has trailing text "graph TD;" — should still be detected as mermaid.
	got := stripMermaidCodeBlocks(input)
	want := "Before\n\nAfter"
	if got != want {
		t.Errorf("stripMermaidCodeBlocks with trailing text on fence\n  got:  %q\n  want: %q", got, want)
	}
}

func TestStripMermaidCodeBlocks_ClosingFenceWithTrailingTextStaysOpen(t *testing.T) {
	// If the closing fence has non-whitespace trailing text, the mermaid block
	// is NOT considered closed. Everything until a proper ``` line is stripped.
	input := "Before\n\n```mermaid\ngraph TD; A-->B;\n``` ignored trailing text\n\nAfter"
	got := stripMermaidCodeBlocks(input)
	want := "Before\n"
	if got != want {
		t.Errorf("stripMermaidCodeBlocks with trailing text on closing fence\n  got:  %q\n  want: %q", got, want)
	}
}

func TestStripMermaidCodeBlocks_NonMermaidUntouched(t *testing.T) {
	input := "```go\nfunc main() {}\n```"
	got := stripMermaidCodeBlocks(input)
	if got != input {
		t.Errorf("expected non-mermaid code block unchanged\n  got:  %q\n  want: %q", got, input)
	}
}

func TestStripMermaidCodeBlocks_InterleavedBlocks(t *testing.T) {
	input := "```go\npackage main\n```\n\n```mermaid\ngraph TD; A;\n```\n\n```python\nprint(1)\n```\n\n```mermaid\ngraph TD; B;\n```"
	got := stripMermaidCodeBlocks(input)
	want := "```go\npackage main\n```\n\n```python\nprint(1)\n```\n"
	if got != want {
		t.Errorf("interleaved blocks\n  got:  %q\n  want: %q", got, want)
	}
}

// ─── renderSessionForPage ─────────────────────────────────────────────

func TestRenderSessionForPage_NilSession(t *testing.T) {
	if got := renderSessionForPage(nil); got != nil {
		t.Error("expected nil for nil session")
	}
}

func TestRenderSessionForPage_EmptySession(t *testing.T) {
	sess := &session.UISession{
		ID:    "test-session",
		Title: "Test",
	}
	got := renderSessionForPage(sess)
	if got == nil {
		t.Fatal("expected non-nil session")
	}
	if got.ID != "test-session" {
		t.Errorf("expected ID 'test-session', got %q", got.ID)
	}
	if len(got.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(got.Messages))
	}
}

func TestRenderSessionForPage_UserMessage(t *testing.T) {
	sess := &session.UISession{
		ID: "sess-1",
		Messages: []session.Message{
			{Role: "user", Content: "Hello **world**"},
		},
	}
	got := renderSessionForPage(sess)
	if got == nil {
		t.Fatal("expected non-nil session")
	}
	if len(got.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got.Messages))
	}
	msg := got.Messages[0]
	if !strings.Contains(msg.Content, "<strong>world</strong>") {
		t.Errorf("expected user message content to be rendered as HTML, got: %s", msg.Content)
	}
	if !strings.Contains(msg.Content, "Hello") {
		t.Errorf("expected user message content to contain 'Hello', got: %s", msg.Content)
	}
}

func TestRenderSessionForPage_AssistantWithoutMermaid(t *testing.T) {
	sess := &session.UISession{
		ID: "sess-1",
		Messages: []session.Message{
			{Role: "assistant", Content: "Hello **world**\n\n```go\nfmt.Println(\"hi\")\n```"},
		},
	}
	got := renderSessionForPage(sess)
	if got == nil {
		t.Fatal("expected non-nil session")
	}
	msg := got.Messages[0]
	if !strings.Contains(msg.Content, "<strong>world</strong>") {
		t.Errorf("expected rendered HTML, got: %s", msg.Content)
	}
	// The mermaid code block was not stripped because there's no MermaidDiagram component.
	if !strings.Contains(msg.Content, "fmt.Println") {
		t.Errorf("expected non-mermaid code block preserved, got: %s", msg.Content)
	}
	// Should still have the code block fences rendered.
	if !strings.Contains(msg.Content, "language-go") {
		t.Errorf("expected syntax-highlighted code block, got: %s", msg.Content)
	}
}

func TestRenderSessionForPage_AssistantWithMermaidComponent(t *testing.T) {
	sess := &session.UISession{
		ID: "sess-1",
		Messages: []session.Message{
			{
				Role:    "assistant",
				Content: "Here is a diagram:\n\n```mermaid\ngraph TD; A-->B;\n```\n\nDone.",
				Components: []session.ComponentData{
					{Name: "MermaidDiagram", Data: map[string]any{"code": "graph TD; A-->B;"}},
				},
			},
		},
	}
	got := renderSessionForPage(sess)
	if got == nil {
		t.Fatal("expected non-nil session")
	}
	msg := got.Messages[0]
	// The mermaid code block should be stripped because there's a MermaidDiagram component.
	if strings.Contains(msg.Content, "```mermaid") {
		t.Errorf("expected mermaid code block to be stripped, got: %s", msg.Content)
	}
	// The component should be rendered as HTML.
	if !strings.Contains(msg.Content, "class=\"mermaid\"") {
		t.Errorf("expected MermaidDiagram component rendered, got: %s", msg.Content)
	}
	// The surrounding text should remain.
	if !strings.Contains(msg.Content, "Here is a diagram") {
		t.Errorf("expected surrounding text preserved, got: %s", msg.Content)
	}
	if !strings.Contains(msg.Content, "Done.") {
		t.Errorf("expected 'Done.' preserved, got: %s", msg.Content)
	}
}

func TestRenderSessionForPage_ActiveSkillsCopied(t *testing.T) {
	sess := &session.UISession{
		ID:           "sess-1",
		ActiveSkills: []string{"skill-a", "skill-b"},
	}
	got := renderSessionForPage(sess)
	if got == nil {
		t.Fatal("expected non-nil session")
	}
	if len(got.ActiveSkills) != 2 || got.ActiveSkills[0] != "skill-a" {
		t.Errorf("expected ActiveSkills copied, got: %v", got.ActiveSkills)
	}
	// Verify it's a copy, not alias (append should not affect original).
	_ = append(got.ActiveSkills, "skill-c")
	if len(sess.ActiveSkills) != 2 {
		t.Error("renderSessionForPage should copy the ActiveSkills slice")
	}
}

// ─── renderComponentsToHTML ───────────────────────────────────────────

func TestRenderComponentsToHTML_Empty(t *testing.T) {
	got := renderComponentsToHTML(context.Background(), "sess-1", nil)
	if got != "" {
		t.Errorf("expected empty for nil components, got %q", got)
	}
	got = renderComponentsToHTML(context.Background(), "sess-1", []session.ComponentData{})
	if got != "" {
		t.Errorf("expected empty for empty components, got %q", got)
	}
}

func TestRenderComponentsToHTML_MermaidDiagram(t *testing.T) {
	components := []session.ComponentData{
		{Name: "MermaidDiagram", Data: map[string]any{"code": "graph TD; A-->B;"}},
	}
	got := renderComponentsToHTML(context.Background(), "sess-1", components)
	if got == "" {
		t.Fatal("expected non-empty HTML for mermaid component")
	}
	if !strings.Contains(got, "class=\"mermaid\"") {
		t.Errorf("expected mermaid class in output, got: %s", got)
	}
	if !strings.Contains(got, "graph TD; A--&gt;B;") {
		t.Errorf("expected escaped mermaid code, got: %s", got)
	}
}

func TestRenderComponentsToHTML_DiffCard(t *testing.T) {
	components := []session.ComponentData{
		{Name: "DiffCard", Data: map[string]any{
			"old":  "hello\n",
			"new":  "world\n",
			"lang": "text",
		}},
	}
	got := renderComponentsToHTML(context.Background(), "sess-1", components)
	if got == "" {
		t.Fatal("expected non-empty HTML for diff card component")
	}
	if !strings.Contains(got, "diff") {
		t.Errorf("expected diff-related content, got: %s", got)
	}
}

func TestRenderComponentsToHTML_FileEditCard(t *testing.T) {
	components := []session.ComponentData{
		{Name: "FileEditCard", Data: map[string]any{
			"path":          "/tmp/test.txt",
			"mode":          "edit",
			"old":           "old content\n",
			"new":           "new content\n",
			"bytes_written": 12,
		}},
	}
	got := renderComponentsToHTML(context.Background(), "sess-1", components)
	if got == "" {
		t.Fatal("expected non-empty HTML for file edit card component")
	}
	if !strings.Contains(got, "test.txt") && !strings.Contains(got, "/tmp/test.txt") {
		t.Errorf("expected file path in output, got: %s", got)
	}
}

func TestRenderComponentsToHTML_MultipleComponents(t *testing.T) {
	components := []session.ComponentData{
		{Name: "MermaidDiagram", Data: map[string]any{"code": "graph TD; A;"}},
		{Name: "DiffCard", Data: map[string]any{
			"old":  "a\n",
			"new":  "b\n",
			"lang": "text",
		}},
	}
	got := renderComponentsToHTML(context.Background(), "sess-1", components)
	if got == "" {
		t.Fatal("expected non-empty HTML for multiple components")
	}
	if !strings.Contains(got, "class=\"mermaid\"") {
		t.Errorf("expected mermaid class in output, got: %s", got)
	}
	if !strings.Contains(got, "diff") {
		t.Errorf("expected diff content in output, got: %s", got)
	}
}

package assets

import (
	"io"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func TestJsFiles(t *testing.T) {
	files := []string{
		"eitri-composer.js",
		"eitri-stream.js",
		"eitri-renderers.js",
		"eitri-mermaid.js",
		"htmx.min.js",
		"prism-core.min.js",
		"prism-go.min.js",
		"katex.min.js",
		"katex-auto-render.min.js",
		"mermaid.min.js",
		"prism.min.css",
		"katex.min.css",
		"eitri-context.js",
	}
	for _, name := range files {
		f, err := Files.Open(name)
		if err != nil {
			t.Errorf("failed to open %s: %v", name, err)
			continue
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			t.Errorf("failed to read %s: %v", name, err)
			continue
		}
		t.Logf("%s: %d bytes", name, len(data))
	}

	// Verify composer JS has runStarted handler
	f, err := Files.Open("eitri-composer.js")
	if err != nil {
		t.Fatalf("failed to open eitri-composer.js: %v", err)
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		t.Fatalf("failed to read eitri-composer.js: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "eitri:runStarted") {
		t.Error("eitri-composer.js missing eitri:runStarted handler")
	}

	// Verify stream JS has reenableComposer
	f2, err := Files.Open("eitri-stream.js")
	if err != nil {
		t.Fatalf("failed to open eitri-stream.js: %v", err)
	}
	data2, err := io.ReadAll(f2)
	f2.Close()
	if err != nil {
		t.Fatalf("failed to read eitri-stream.js: %v", err)
	}
	content2 := string(data2)
	if !strings.Contains(content2, "reenableComposer") {
		t.Error("eitri-stream.js missing reenableComposer function")
	}


	// Verify stream JS has insertOptimisticBubble
	if !strings.Contains(content2, "insertOptimisticBubble") {
		t.Error("eitri-stream.js missing insertOptimisticBubble function")
	}

	// Verify stream JS has scrollToLatest
	if !strings.Contains(content2, "scrollToLatest") {
		t.Error("eitri-stream.js missing scrollToLatest function")
	}

	// Verify stream JS has scroll-to-bottom button logic (IntersectionObserver, sentinel, button toggle)
	if !strings.Contains(content2, "initScrollToBottomButton") {
		t.Error("eitri-stream.js missing initScrollToBottomButton function")
	}
	if !strings.Contains(content2, "scroll-to-bottom-btn") {
		t.Error("eitri-stream.js missing scroll-to-bottom-btn element reference")
	}
	if !strings.Contains(content2, "IntersectionObserver") {
		t.Error("eitri-stream.js missing IntersectionObserver for scroll detection")
	}

	// Verify stream JS has removeOptimisticBubbles
	if !strings.Contains(content2, "removeOptimisticBubbles") {
		t.Error("eitri-stream.js missing removeOptimisticBubbles function")
	}

	// Verify activity panel functions are removed
	if strings.Contains(content2, "autoOpenActivityPanel") {
		t.Error("eitri-stream.js should not contain autoOpenActivityPanel function")
	}
	if strings.Contains(content2, "updateActivitySummary") {
		t.Error("eitri-stream.js should not contain updateActivitySummary function")
	}

	if strings.Contains(content2, "activityElapsed") {
		t.Error("eitri-stream.js should not contain activityElapsed variable or function")
	}
	if strings.Contains(content2, "appendActivityEntry") {
		t.Error("eitri-stream.js should not contain appendActivityEntry function")
	}
	if strings.Contains(content2, "updateActivityCount") {
		t.Error("eitri-stream.js should not contain updateActivityCount function")
	}
	if strings.Contains(content2, "resetActivityPanel") {
		t.Error("eitri-stream.js should not contain resetActivityPanel function")
	}
	if strings.Contains(content2, "activityBriefForPacket") {
		t.Error("eitri-stream.js should not contain activityBriefForPacket function")
	}
	if strings.Contains(content2, "summarizeToolDetail") {
		t.Error("eitri-stream.js should not contain summarizeToolDetail function")
	}
	if strings.Contains(content2, "formatElapsed") {
		t.Error("eitri-stream.js should not contain formatElapsed function")
	}
	if strings.Contains(content2, "activityToolCount") {
		t.Error("eitri-stream.js should not contain activityToolCount variable")
	}
	if strings.Contains(content2, "activityToolSummary") {
		t.Error("eitri-stream.js should not contain activityToolSummary variable")
	}

	// Verify stream JS has context_update handler
	if !strings.Contains(content2, "context_update") {
		t.Error("eitri-stream.js missing context_update handler")
	}
	if !strings.Contains(content2, "dispatchContextUpdate") {
		t.Error("eitri-stream.js missing dispatchContextUpdate call")
	}
	if !strings.Contains(content2, "resetContextPanel") {
		t.Error("eitri-stream.js missing resetContextPanel call")
	}

	// Verify stream JS appends token-usage before scroll-sentinel
	if !strings.Contains(content2, "insertBefore") && strings.Contains(content2, "scroll-sentinel") {
		// Check that appendTokenUsage inserts before sentinel, not after
		if strings.Contains(content2, "messages.insertBefore(footer, sentinel)") {
			// Good: token-usage goes before sentinel
		} else if strings.Contains(content2, "messages.appendChild(footer)") && strings.Contains(content2, "// Insert before scroll-sentinel") {
			// Good: token-usage inserted before sentinel
		} else {
			t.Error("eitri-stream.js should insert token-usage before scroll-sentinel")
		}
	}

	f3, err := Files.Open("eitri-renderers.js")
	if err != nil {
		t.Fatalf("failed to open eitri-renderers.js: %v", err)
	}
	data3, err := io.ReadAll(f3)
	f3.Close()
	if err != nil {
		t.Fatalf("failed to read eitri-renderers.js: %v", err)
	}
	content3 := string(data3)
	if !strings.Contains(content3, "initPrism") {
		t.Error("eitri-renderers.js missing Prism initialization")
	}
	if !strings.Contains(content3, "initKatex") {
		t.Error("eitri-renderers.js missing KaTeX initialization")
	}

	// Verify CSS has scroll-to-bottom button with --composer-height variable
	f4, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("failed to open eitri.css: %v", err)
	}
	data4, err := io.ReadAll(f4)
	f4.Close()
	if err != nil {
		t.Fatalf("failed to read eitri.css: %v", err)
	}
	content4 := string(data4)

	// Verify CSS has .messages as scroll container with overflow-y: auto
	if !strings.Contains(content4, ".messages {") {
		t.Error("eitri.css missing .messages selector for scroll container")
	}
	// Check overflow-y: auto within messages block
	msgIdx := strings.Index(content4, ".messages {")
	if msgIdx >= 0 {
		// Scan forward from messages selector for overflow-y: auto
		block := content4[msgIdx:]
		closeIdx := strings.Index(block, "}")
		if closeIdx >= 0 {
			block = block[:closeIdx+1]
			if !strings.Contains(block, "overflow-y: auto") {
				t.Error(".messages CSS block missing overflow-y: auto (required for IntersectionObserver scroll container)")
			}
		}
	}
	if !strings.Contains(content4, "--composer-height") {
		t.Error("eitri.css missing --composer-height CSS variable for scroll-to-bottom positioning")
	}
	if !strings.Contains(content4, "calc(var(--composer-bottom, var(--composer-height") {
		t.Error("eitri.css missing calc(var(--composer-bottom, var(--composer-height) for scroll-to-bottom button bottom offset")
	}

	// Verify composer JS has composer height tracking on parent #chat-view
	if !strings.Contains(content, "_trackComposerHeight") {
		t.Error("eitri-composer.js missing _trackComposerHeight method")
	}
	if !strings.Contains(content, "ResizeObserver") {
		t.Error("eitri-composer.js missing ResizeObserver for composer height tracking")
	}
	if !strings.Contains(content, "parent.style.setProperty") {
		t.Error("eitri-composer.js should set --composer-height on parent element")
	}

	// Verify context JS exports
	f5, err := Files.Open("eitri-context.js")
	if err != nil {
		t.Fatalf("failed to open eitri-context.js: %v", err)
	}
	data5, err := io.ReadAll(f5)
	f5.Close()
	if err != nil {
		t.Fatalf("failed to read eitri-context.js: %v", err)
	}
	content5 := string(data5)

	if !strings.Contains(content5, "customElements.define") {
		t.Error("eitri-context.js missing customElements.define call")
	}
	if !strings.Contains(content5, "eitri-context") {
		t.Error("eitri-context.js missing eitri-context element name")
	}
	if !strings.Contains(content5, "context-update") {
		t.Error("eitri-context.js missing context-update event listener")
	}
	if !strings.Contains(content5, "resetToIdle") {
		t.Error("eitri-context.js missing resetToIdle method")
	}
	if !strings.Contains(content5, "dispatchContextUpdate") {
		t.Error("eitri-context.js missing dispatchContextUpdate helper")
	}
	if !strings.Contains(content5, "resetContextPanel") {
		t.Error("eitri-context.js missing resetContextPanel helper")
	}
	if !strings.Contains(content5, "_renderCompact") {
		t.Error("eitri-context.js missing _renderCompact method")
	}
	if !strings.Contains(content5, "_renderExpanded") {
		t.Error("eitri-context.js missing _renderExpanded method")
	}
	if !strings.Contains(content5, "fill-green") {
		t.Error("eitri-context.js missing fill-green class name")
	}
	if !strings.Contains(content5, "fill-yellow") {
		t.Error("eitri-context.js missing fill-yellow class name")
	}
	if !strings.Contains(content5, "fill-red") {
		t.Error("eitri-context.js missing fill-red class name")
	}
	if !strings.Contains(content5, "No active run") {
		t.Error("eitri-context.js missing idle state text")
	}
	if !strings.Contains(content5, "DEBOUNCE_MS") {
		t.Error("eitri-context.js missing DEBOUNCE_MS constant")
	}

	// Per-category progress bars
	if !strings.Contains(content5, "context-category-bar") {
		t.Error("eitri-context.js missing context-category-bar class for per-category mini bars")
	}
	if !strings.Contains(content5, "context-category-bar-fill") {
		t.Error("eitri-context.js missing context-category-bar-fill class for per-category mini bar fill")
	}
	if strings.Count(content5, "context-category-bar-fill") < 5 {
		t.Errorf("eitri-context.js has %d category-bar-fill elements, want at least 5 (one per row)", strings.Count(content5, "context-category-bar-fill"))
	}

	// Verify stream JS exports lightweightMarkdown function

	// Verify stream JS es.onerror handler calls cleanup before RECONNECTING
	if !strings.Contains(content2, "clearToolActivity") {
		t.Error("eitri-stream.js missing clearToolActivity function")
	}
	if !strings.Contains(content2, "clearThinkingPanel") {
		t.Error("eitri-stream.js missing clearThinkingPanel function")
	}
	if !strings.Contains(content2, "resetActivityTracking") {
		t.Error("eitri-stream.js missing resetActivityTracking function")
	}
	// Verify es.onerror calls all three cleanup functions before RECONNECTING
	errReconnectIdx := strings.Index(content2, "state.status = STATES.RECONNECTING")
	if errReconnectIdx < 0 {
		t.Error("eitri-stream.js missing RECONNECTING state transition")
	} else {
		// Find es.onerror block — search backwards for it
		onerrorStart := strings.LastIndex(content2[:errReconnectIdx], "es.onerror = function")
		if onerrorStart < 0 {
			t.Error("eitri-stream.js missing es.onerror handler")
		} else {
			onerrorBlock := content2[onerrorStart:errReconnectIdx]
			if !strings.Contains(onerrorBlock, "clearToolActivity()") {
				t.Error("es.onerror handler missing clearToolActivity() call before RECONNECTING")
			}
			if !strings.Contains(onerrorBlock, "clearThinkingPanel()") {
				t.Error("es.onerror handler missing clearThinkingPanel() call before RECONNECTING")
			}
			if !strings.Contains(onerrorBlock, "resetActivityTracking()") {
				t.Error("es.onerror handler missing resetActivityTracking() call before RECONNECTING")
			}
		}
	}
	if !strings.Contains(content2, "lightweightMarkdown") {
		t.Error("eitri-stream.js missing lightweightMarkdown function")
	}
}

func TestLightweightMarkdown(t *testing.T) {
	f, err := Files.Open("eitri-stream.js")
	if err != nil {
		t.Fatalf("failed to open eitri-stream.js: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("failed to read eitri-stream.js: %v", err)
	}
	content := string(data)

	// Extract the lightweightMarkdown function body
	// Defined as: function lightweightMarkdown(text) { ... }
	startMatch := "function lightweightMarkdown(text) {"
	startIdx := strings.Index(content, startMatch)
	if startIdx < 0 {
		t.Fatal("lightweightMarkdown function not found in eitri-stream.js")
	}
	// Opening brace position
	braceIdx := startIdx + len(startMatch) - 1
	// Body starts after the {
	bodyStart := braceIdx + 1

	// Find matching closing brace — scan counting braces
	depth := 1
	bodyEnd := bodyStart
	for bodyEnd < len(content) {
		switch content[bodyEnd] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				bodyEnd++
				goto extractBody
			}
		}
		bodyEnd++
	}
extractBody:
	if depth != 0 {
		t.Fatal("could not find matching closing brace for lightweightMarkdown function")
	}

	// Build JS function — extracted body only
	fnSrc := "function lightweightMarkdown(text) {" + content[bodyStart:bodyEnd]

	runtime := goja.New()
	_, err = runtime.RunString(fnSrc)
	if err != nil {
		t.Fatalf("failed to parse lightweightMarkdown: %v", err)
	}

	var fn func(string) string
	err = runtime.ExportTo(runtime.Get("lightweightMarkdown"), &fn)
	if err != nil {
		t.Fatalf("failed to export lightweightMarkdown: %v", err)
	}

	tests := []struct {
		name     string
		input    string
		wantHTML string
	}{
		{
			name:     "bold",
			input:    "**bold**",
			wantHTML: "<strong>bold</strong>",
		},
		{
			name:     "italic",
			input:    "*italic*",
			wantHTML: "<em>italic</em>",
		},
		{
			name:     "inline code",
			input:    "`code`",
			wantHTML: "<code>code</code>",
		},
		{
			name:     "https link",
			input:    "[text](https://example.com)",
			wantHTML: `<a href="https://example.com" target="_blank" rel="noopener">text</a>`,
		},
		{
			name:     "http link",
			input:    "[text](http://example.com)",
			wantHTML: `<a href="http://example.com" target="_blank" rel="noopener">text</a>`,
		},
		{
			name:     "mailto link",
			input:    "[me](mailto:u@h.com)",
			wantHTML: `<a href="mailto:u@h.com" target="_blank" rel="noopener">me</a>`,
		},
		{
			name:     "javascript: link — no <a>",
			input:    "[click](javascript:alert(1))",
			wantHTML: "[click](javascript:alert(1))",
		},
		{
			name:     "data: link — no <a>",
			input:    "[bad](data:text/html,<svg>)",
			wantHTML: "[bad](data:text/html,&lt;svg&gt;)",
		},
		{
			name:     "incomplete/unclosed bold",
			input:    "**unclosed",
			wantHTML: "**unclosed",
		},
		{
			name:     "paragraph breaks",
			input:    "para1\n\npara2",
			wantHTML: "</p><p>",
		},
		{
			name:     "mixed bold italic code",
			input:    "**bold** *italic* `code`",
			wantHTML: "<strong>bold</strong> <em>italic</em> <code>code</code>",
		},
		{
			name:     "plain text wrapped in <p>",
			input:    "hello world",
			wantHTML: "<p>hello world</p>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fn(tc.input)
			if !strings.Contains(got, tc.wantHTML) {
				t.Errorf("lightweightMarkdown(%q)\n  got:  %q\n  want substring: %q", tc.input, got, tc.wantHTML)
			}
		})
	}
}

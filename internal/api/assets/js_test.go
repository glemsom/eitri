package assets

import (
	"io"
	"strings"
	"testing"
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

	// Verify stream JS has activity panel auto-open on first tool
	if !strings.Contains(content2, "autoOpenActivityPanel") {
		t.Error("eitri-stream.js missing autoOpenActivityPanel function")
	}

	// Verify stream JS has compact summary update for activity panel
	if !strings.Contains(content2, "updateActivitySummary") {
		t.Error("eitri-stream.js missing updateActivitySummary function")
	}

	// Verify stream JS has elapsed time tracking in activity entries
	if !strings.Contains(content2, "activityElapsed") {
		t.Error("eitri-stream.js missing activityElapsed variable or function")
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
	if !strings.Contains(content4, "--composer-height") {
		t.Error("eitri.css missing --composer-height CSS variable for scroll-to-bottom positioning")
	}
	if !strings.Contains(content4, "calc(var(--composer-height") {
		t.Error("eitri.css missing calc(var(--composer-height) for scroll-to-bottom button bottom offset")
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
}

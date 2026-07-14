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

	// Verify stream JS has removeOptimisticBubbles
	if !strings.Contains(content2, "removeOptimisticBubbles") {
		t.Error("eitri-stream.js missing removeOptimisticBubbles function")
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
}

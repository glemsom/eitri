package assets

import (
	"io"
	"strings"
	"testing"
)

func TestJsFiles(t *testing.T) {
	files := []string{"eitri-composer.js", "eitri-stream.js", "htmx.min.js"}
	for _, name := range files {
		f, err := Files.Open(name)
		if err != nil {
			t.Errorf("failed to open %s: %v", name, err)
			continue
		}
		data, _ := io.ReadAll(f)
		f.Close()
		t.Logf("%s: %d bytes", name, len(data))
	}
	
	// Verify composer JS has runStarted handler
	f, _ := Files.Open("eitri-composer.js")
	data, _ := io.ReadAll(f)
	f.Close()
	content := string(data)
	if !strings.Contains(content, "eitri:runStarted") {
		t.Error("eitri-composer.js missing eitri:runStarted handler")
	}
	
	// Verify stream JS has reenableComposer
	f2, _ := Files.Open("eitri-stream.js")
	data2, _ := io.ReadAll(f2)
	f2.Close()
	content2 := string(data2)
	if !strings.Contains(content2, "reenableComposer") {
		t.Error("eitri-stream.js missing reenableComposer function")
	}
}

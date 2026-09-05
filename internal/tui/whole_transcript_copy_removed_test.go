package tui

import (
	"strings"
	"testing"
)

func TestWholeTranscriptCopyCommandsAreAbsent(t *testing.T) {
	m := resize(t, NewModelCfg(Dependencies{}))
	for _, command := range []string{"/copy", "ctrl+o"} {
		if strings.Contains(helpView(), command) {
			t.Errorf("help contains removed command %q", command)
		}
		if strings.Contains(view(m), command) {
			t.Errorf("main view contains removed command %q", command)
		}
	}
	for _, candidate := range slashCandidates("/", nil) {
		if candidate == "/copy" {
			t.Fatal("slash completion contains removed /copy command")
		}
	}
}

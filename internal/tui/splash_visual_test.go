package tui

import (
	"os"
	"testing"
)

func TestSplashVisualDump(t *testing.T) {
	if os.Getenv("SPLASH_DUMP") == "" {
		t.Skip("visual dump; run with SPLASH_DUMP=1")
	}
	s := &splashState{}
	for f := 0; f <= 50; f += 5 {
		s.frame = f
		t.Logf("=== frame %d ===\n%s", f, renderSplash(s, 80, 24))
	}
}

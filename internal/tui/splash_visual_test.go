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
	// One dump frame per splash phase plus each transition boundary (issue #510).
	for _, f := range []int{0, 5, 10, 15, 18, 20, 22, 23, 27, 28, 40, 50} {
		s.frame = f
		t.Logf("=== frame %d ===\n%s", f, renderSplash(s, 80, 24))
	}
}

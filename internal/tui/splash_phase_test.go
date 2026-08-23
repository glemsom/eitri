package tui

import (
	"strings"
	"testing"
)

// splashEyeFlashSGR is the exact true-color green the eyes flash with at
// frame 18, as specified by issue #510.
const splashEyeFlashSGR = "\x1b[38;2;0;255;136m"

func TestSplash_eyesFlashGreenAtFrame18(t *testing.T) {
	s := &splashState{kitty: true, frame: 18}
	if out := renderSplash(s, 120, 40); !strings.Contains(out, splashEyeFlashSGR) {
		t.Fatalf("kitty frame 18 must contain the eye-flash green %q", splashEyeFlashSGR)
	}
	for _, f := range []int{17, 19} {
		s.frame = f
		if out := renderSplash(s, 120, 40); strings.Contains(out, splashEyeFlashSGR) {
			t.Errorf("eye flash leaked into frame %d", f)
		}
	}
}

func TestSplash_nonKittyRainIntensifiesDuringEmergence(t *testing.T) {
	count := func(frame int) int {
		s := &splashState{}
		s.frame = frame
		return strings.Count(stripANSI(renderSplash(s, 80, 24)), "ᚠ")
	}
	before := count(8)
	during := count(15)
	if during <= before {
		t.Fatalf("emergence rain (frame 15: %d glyphs) must be denser than the storm baseline (frame 8: %d)", during, before)
	}
}

func TestSplash_allPhasesRenderAtSupportedSizes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {200, 60}} {
		for _, f := range []int{0, 10, 15, 18, 20, 22, 23, 27, 28, 40, 50} {
			s := &splashState{kitty: true, frame: f}
			if out := renderSplash(s, size[0], size[1]); strings.Count(out, "\n") < size[1]-3 {
				t.Errorf("kitty frame %d at %dx%d rendered %d lines", f, size[0], size[1], strings.Count(out, "\n"))
			}
		}
	}
}

func TestSplash_visualDumpCoversAllPhases(t *testing.T) {
	for _, f := range []int{0, 5, 10, 15, 18, 20, 22, 23, 27, 28, 40, 50} {
		s := &splashState{}
		s.frame = f
		if out := renderSplash(s, 80, 24); len(out) == 0 {
			t.Errorf("frame %d rendered empty", f)
		}
	}
}

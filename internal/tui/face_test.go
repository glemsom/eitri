package tui

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"regexp"
	"strings"
	"testing"
)

// TestFaceWebPEmbedded verifies the binary carries the face asset and that it
// decodes as the WebP the splash displays.
func TestFaceWebPEmbedded(t *testing.T) {
	if len(faceWebP) == 0 {
		t.Fatal("faceWebP is empty: face.webp not embedded")
	}
	if !bytes.HasPrefix(faceWebP, []byte("RIFF")) || !bytes.Contains(faceWebP[:16], []byte("WEBP")) {
		t.Fatal("embedded face is not a WebP container")
	}
}

// TestKittyFaceEscapeWellFormed checks the emitted sequence is a valid Kitty
// graphics transmission followed by a cursor-anchored placement.
func TestKittyFaceEscapeWellFormed(t *testing.T) {
	esc := kittyFaceEscape(12, 80, 24)
	if esc == "" {
		t.Fatal("kittyFaceEscape(frame 12) = empty, want a graphics escape")
	}
	if !strings.HasPrefix(esc, "\x1b[") || !strings.HasSuffix(esc, "\x1b\\") {
		t.Fatalf("escape not cursor-prefixed and APC-terminated in Kitty form: %q", esc[:min(len(esc), 40)])
	}
	// The transmission command must declare PNG payload format (f=100).
	if !strings.Contains(esc, "f=100") {
		t.Error("transmission missing f=100 (PNG) format key")
	}
	// A placement action must anchor the image to cells so it can be centered.
	if !strings.Contains(esc, "a=p") {
		t.Error("escape missing placement action a=p")
	}
	// Every base64 payload chunk must decode; reassembly must be a PNG image.
	re := regexp.MustCompile(`\x1b_G[^;]*;([A-Za-z0-9+/=]*)\x1b\\`)
	var b64 string
	for _, m := range re.FindAllStringSubmatch(esc, -1) {
		b64 += m[1]
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("payload is not valid base64: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("reassembled payload does not decode as PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() > maxFacePixelSize*2 || b.Dy() > maxFacePixelSize*2 {
		t.Errorf("payload image %v too large for a splash overlay", b)
	}
}

// TestKittyFaceOnlyWhenKitty gates emission on the splash's Kitty capability:
// a non-Kitty terminal must never see a single graphics escape.
func TestKittyFaceOnlyWhenKitty(t *testing.T) {
	for frame := 0; frame <= splashTotalFrames; frame++ {
		out := renderSplash(&splashState{frame: frame, kitty: false}, 120, 40)
		if strings.Contains(out, "\x1b_G") {
			t.Fatalf("non-Kitty render at frame %d emitted a Kitty graphics escape", frame)
		}
	}
	sawEscape := false
	for frame := 10; frame <= 22; frame++ {
		if strings.Contains(renderSplash(&splashState{frame: frame, kitty: true}, 120, 40), "\x1b_G") {
			sawEscape = true
		}
	}
	if !sawEscape {
		t.Error("Kitty render never emitted a graphics escape during frames 10–22")
	}
	for _, frame := range []int{0, 9, 23, 30, 50} {
		out := renderSplash(&splashState{frame: frame, kitty: true}, 120, 40)
		if strings.Contains(out, "\x1b_G") {
			t.Errorf("frame %d outside the emergence window still emitted a graphics escape", frame)
		}
	}
}

// TestFaceOpacityRamp pins the fade choreography: silent before 10, ramping
// up to full by 18, fading out across the shatter (20–22), gone after 22.
func TestFaceOpacityRamp(t *testing.T) {
	if faceOpacity(0) != 0 || faceOpacity(9) != 0 {
		t.Error("face should be invisible before frame 10")
	}
	prev := 0.0
	for f := 10; f <= 18; f++ {
		o := faceOpacity(f)
		if o <= prev || o > 1 {
			t.Fatalf("opacity ramp not monotonically increasing toward frame 18: frame %d → %v", f, o)
		}
		prev = o
	}
	if faceOpacity(18) < 1 {
		t.Errorf("faceOpacity(18) = %v, want fully visible at end of ramp", faceOpacity(18))
	}
	if faceOpacity(19) < 1 {
		t.Errorf("faceOpacity(19) = %v, want full during hold", faceOpacity(19))
	}
	prev = 1.0
	for f := 20; f <= 22; f++ {
		o := faceOpacity(f)
		if o >= prev || o < 0 {
			t.Fatalf("shatter fade-out not monotonically decreasing: frame %d → %v", f, o)
		}
		prev = o
	}
	if faceOpacity(23) != 0 || faceOpacity(50) != 0 {
		t.Error("face should be invisible after frame 22")
	}
}

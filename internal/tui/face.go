package tui

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"strings"
	"sync"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

// The dwarf face the splash reveals behind the rain on Kitty-capable
// terminals. The WebP is embedded so the binary stays self-contained; the
// Kitty graphics protocol only accepts PNG payloads in-band, so it is decoded
// once and re-encoded as PNG at startup.

//go:embed face.webp
var faceWebP []byte

// maxFacePixelSize caps the downscaled face: a splash overlay must stay small
// enough that re-transmitting it every fade frame costs kilobytes, not
// megabytes.
const maxFacePixelSize = 200

// kittyFaceImageID is the graphics ID under which the face is transmitted and
// placed; reusing one ID lets each frame's transmission overwrite the last
// instead of accumulating images in the terminal.
const kittyFaceImageID = 1

var (
	facePNGOnce sync.Once
	facePNGData []byte
	facePNGB64  string // pre-encoded so per-frame work is only alpha scaling
)

// facePNG decodes the embedded WebP and downscales it to at most
// maxFacePixelSize on its long edge. The result is cached; a decode failure
// yields nil and the splash silently skips the face rather than corrupting
// output with garbage payloads.
func facePNG() []byte {
	facePNGOnce.Do(func() {
		src, err := webp.Decode(bytes.NewReader(faceWebP))
		if err != nil {
			return
		}
		b := src.Bounds()
		scale := float64(maxFacePixelSize) / float64(max(b.Dx(), b.Dy()))
		w := max(1, int(float64(b.Dx())*scale))
		h := max(1, int(float64(b.Dy())*scale))
		dst := image.NewNRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
		var buf bytes.Buffer
		if png.Encode(&buf, dst) != nil {
			return
		}
		facePNGData = buf.Bytes()
	})
	return facePNGData
}

// faceOpacity returns the face's opacity at a frame: zero before the
// emergence phase, an even ramp from frame 10 to full visibility by frame 18,
// held through frame 19, then fading out across the shatter (frames 20–22).
func faceOpacity(frame int) float64 {
	switch {
	case frame < 10 || frame > 22:
		return 0
	case frame <= 18:
		return float64(frame-9) / 9 // frames 10–18: 1/9 … 9/9
	case frame == 19:
		return 1
	default:
		return float64(23-frame) / 4 // frames 20–22: 3/4 … 1/4, dissolving toward the wordmark convergence
	}
}

// faceCells computes the cell-grid footprint that keeps the image's aspect
// ratio under the terminal's ~2:1 cell aspect, capped at half the screen so
// the rain stays visible around it.
func faceCells(imgW, imgH, termW, termH int) (cols, rows int) {
	maxRows := max(4, termH/2)
	rows = maxRows
	cols = rows * imgW / imgH / 2
	if cols > termW {
		cols = termW
		rows = cols * imgH / imgW * 2
	}
	if rows > termH {
		rows = termH
	}
	return cols, rows
}

// kittyFaceEscape renders the escape sequences that show the face at the
// given opacity for a terminal of w×h cells: the image is transmitted as
// chunked base64 PNG, then placed centered via cursor positioning (C=1). An
// opacity of zero or a missing payload emits nothing — the non-Kitty and
// out-of-window paths produce no graphics escapes at all.
//
// Note: the issue text describes the wire format loosely ("CSI … BEL"); this
// uses the protocol-correct APC framing (`ESC _ G … ESC \`) that Kitty,
// Ghostty and WezTerm actually parse.
func kittyFaceEscape(frame, w, h int) string {
	op := faceOpacity(frame)
	if op <= 0 || w <= 0 || h <= 0 {
		return ""
	}
	src := facePNG()
	if src == nil {
		return ""
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return ""
	}
	cols, rows := faceCells(cfg.Width, cfg.Height, w, h)
	row := max(1, (h-rows)/2+1)
	col := max(1, (w-cols)/2+1)

	payload := scaleFaceAlpha(src, op)
	const chunkLimit = 4096
	b64 := base64.StdEncoding.EncodeToString(payload)

	var b strings.Builder
	// Move the hardware cursor to the placement anchor before placing; C=1
	// makes the placement relative to the cursor instead of the screen origin.
	fmt.Fprintf(&b, "\x1b[%d;%dH", row, col)
	for {
		chunk := b64
		more := false
		if len(chunk) > chunkLimit {
			chunk, b64 = chunk[:chunkLimit], chunk[chunkLimit:]
			more = true
		}
		keys := fmt.Sprintf("i=%d,f=100,a=T,q=1,m=%d", kittyFaceImageID, boolToInt(more))
		fmt.Fprintf(&b, "\x1b_G%s;%s\x1b\\", keys, chunk)
		if !more {
			break
		}
	}
	fmt.Fprintf(&b, "\x1b_Ga=p,i=%d,C=1,q=1,c=%d,r=%d\x1b\\", kittyFaceImageID, cols, rows)
	return b.String()
}

// scaleFaceAlpha multiplies every opaque pixel's alpha by op so the same PNG
// encoder produces the fade ramp: Kitty has no per-image opacity key, so the
// transparency is baked into each frame's payload.
func scaleFaceAlpha(pngData []byte, op float64) []byte {
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return pngData
	}
	nrgba, ok := img.(*image.NRGBA)
	if !ok {
		return pngData
	}
	scaled := image.NewNRGBA(nrgba.Bounds())
	copy(scaled.Pix, nrgba.Pix)
	for i := 3; i < len(scaled.Pix); i += 4 {
		scaled.Pix[i] = uint8(float64(scaled.Pix[i]) * op)
	}
	var buf bytes.Buffer
	if png.Encode(&buf, scaled) != nil {
		return pngData
	}
	return buf.Bytes()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

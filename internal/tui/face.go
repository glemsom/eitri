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

// Face choreography frames: the fade-in ramp, the hold, and the shatter
// dissolve — one source of truth shared by faceOpacity and the tests.
const (
	faceStartFrame = 10 // first frame the face appears (ramp begins)
	faceFullFrame  = 18 // ramp reaches full opacity
	holdEndFrame   = 19 // last fully-visible frame
	faceGoneFrame  = 22 // shatter dissolve completes; invisible after
)

var (
	facePNGOnce sync.Once
	facePNGData []byte
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
// held through frame 19, then fading out across the shatter until it is gone
// at frame 22.
func faceOpacity(frame int) float64 {
	switch {
	case frame < faceStartFrame || frame > faceGoneFrame:
		return 0
	case frame <= faceFullFrame:
		return float64(frame-faceStartFrame+1) / (faceFullFrame - faceStartFrame + 1)
	case frame <= holdEndFrame:
		return 1
	default:
		return float64(faceGoneFrame-frame) / (faceGoneFrame - holdEndFrame) // 2/3 … 0 across the shatter
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

// facePlacement is where the face sits on the terminal grid: centered,
// aspect-corrected, capped at half the screen. Shared by the graphics escape
// and the eye-flash overlay so both agree on the face's cell footprint.
type facePlacement struct {
	col, row, cols, rows int
}

// faceGeometry computes the face's cell-grid placement for a w×h terminal;
// ok is false when there is no decodable face payload.
func faceGeometry(w, h int) (p facePlacement, ok bool) {
	src := facePNG()
	if src == nil || w <= 0 || h <= 0 {
		return p, false
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return p, false
	}
	p.cols, p.rows = faceCells(cfg.Width, cfg.Height, w, h)
	p.row = max(1, (h-p.rows)/2+1)
	p.col = max(1, (w-p.cols)/2+1)
	return p, true
}

// cellPos is one terminal-grid cell.
type cellPos struct {
	row, col int
}

// eyeFlashOffsets locate the dwarf's eyes within the face's cell footprint,
// as fractions of that footprint measured off the source image: the eyes sit
// roughly 42% down and a third/two-thirds across.
const (
	eyeRowFrac      = 0.42
	eyeLeftColFrac  = 0.37
	eyeRightColFrac = 0.63
)

// eyeFlashCells returns the two terminal cells covering the eyes for the
// given placement.
func eyeFlashCells(p facePlacement) [2]cellPos {
	mk := func(colFrac float64) cellPos {
		return cellPos{
			row: p.row + int(eyeRowFrac*float64(p.rows)),
			col: p.col + int(colFrac*float64(p.cols)),
		}
	}
	return [2]cellPos{mk(eyeLeftColFrac), mk(eyeRightColFrac)}
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
	place, ok := faceGeometry(w, h)
	if !ok {
		return ""
	}
	cols, rows, row, col := place.cols, place.rows, place.row, place.col

	payload := scaleFaceAlpha(facePNG(), op)
	const chunkLimit = 4096
	b64 := base64.StdEncoding.EncodeToString(payload)

	var b strings.Builder
	// The placement anchor needs the hardware cursor, but this string rides
	// inside a bubbletea-rendered frame — so save and restore the cursor
	// (DECSC/DECRC) around the move, leaving the renderer's cursor untouched.
	b.WriteString("\x1b7")
	fmt.Fprintf(&b, "\x1b[%d;%dH", row, col)
	for {
		chunk := b64
		more := false
		if len(chunk) > chunkLimit {
			chunk, b64 = chunk[:chunkLimit], chunk[chunkLimit:]
			more = true
		}
		// Continuation chunks carry only the m key per the graphics spec.
		keys := fmt.Sprintf("i=%d,f=100,a=T,q=1", kittyFaceImageID)
		if more {
			keys = "m=1"
		}
		fmt.Fprintf(&b, "\x1b_G%s;%s\x1b\\", keys, chunk)
		if !more {
			break
		}
	}
	fmt.Fprintf(&b, "\x1b_Ga=p,i=%d,C=1,q=1,c=%d,r=%d\x1b\\", kittyFaceImageID, cols, rows)
	b.WriteString("\x1b8")
	return b.String()
}

// scaleFaceAlpha multiplies every opaque pixel's alpha by op so the same PNG
// encoder produces the fade ramp: Kitty has no per-image opacity key, so the
// transparency is baked into each frame's payload. Results are memoized per
// opacity level — the splash revisits each only once.
var faceAlphaCache sync.Map

func scaleFaceAlpha(pngData []byte, op float64) []byte {
	if cached, ok := faceAlphaCache.Load(op); ok {
		return cached.([]byte)
	}
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
	out := buf.Bytes()
	faceAlphaCache.Store(op, out)
	return out
}

package tui

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"image/png"
	"os"
	"strconv"
	"strings"
	"sync"
)

//go:embed face-removebg-preview.png
var eitriFacePNG []byte

var (
	kittyFacePathOnce sync.Once
	kittyFacePath     string
)

func kittyFaceFile() string {
	kittyFacePathOnce.Do(func() {
		img, err := png.Decode(bytes.NewReader(eitriFacePNG))
		if err != nil {
			return
		}
		f, err := os.CreateTemp("", "tty-graphics-protocol-eitri-face-*.png")
		if err != nil {
			return
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			_ = os.Remove(f.Name())
			return
		}
		kittyFacePath = f.Name()
	})
	return kittyFacePath
}

func kittyImageFile(path string, cols, rows int) string {
	if path == "" || cols <= 0 || rows <= 0 || !kittyGraphicsLikelySupported() {
		return ""
	}
	return "\x1b_Ga=T,f=100,t=f,c=" + strconv.Itoa(cols) + ",r=" + strconv.Itoa(rows) + ",z=1;" + base64.StdEncoding.EncodeToString([]byte(path)) + "\x1b\\"
}

func kittyGraphicsLikelySupported() bool {
	if os.Getenv("EITRI_KITTY_IMAGES") == "1" {
		return true
	}
	if strings.HasSuffix(os.Args[0], ".test") {
		return false
	}
	termProgram := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	return strings.Contains(termProgram, "ghostty") || strings.Contains(termProgram, "kitty")
}

func railFaceRows(railWidth int) (cols, rows int) {
	cols = railWidth - 6
	if cols < 1 {
		return 0, 0
	}
	rows = (cols + 1) / 2
	if rows < 1 {
		rows = 1
	}
	return cols, rows
}

func kittyFacePlacement(x, y, railWidth int) string {
	cols, rows := railFaceRows(railWidth)
	face := kittyImageFile(kittyFaceFile(), cols, rows)
	if face == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\x1b7")
	b.WriteString("\x1b[")
	b.WriteString(strconv.Itoa(y))
	b.WriteString(";")
	b.WriteString(strconv.Itoa(x))
	b.WriteString("H")
	b.WriteString(face)
	borderX := x - 5
	if borderX < 1 {
		borderX = 1
	}
	for row := 0; row < rows; row++ {
		b.WriteString("\x1b[")
		b.WriteString(strconv.Itoa(y + row))
		b.WriteString(";")
		b.WriteString(strconv.Itoa(borderX))
		b.WriteString("H")
		b.WriteString(g("│", "|"))
	}
	b.WriteString("\x1b8")
	return b.String()
}

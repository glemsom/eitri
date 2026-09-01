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

	"github.com/charmbracelet/x/ansi/kitty"
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

const kittyFaceImageID = 1162433618 // "EITR" as a stable Kitty image ID.

func kittyFaceDelete() string {
	return "\x1b_Ga=d,d=I,i=" + strconv.Itoa(kittyFaceImageID) + ";\x1b\\"
}

func kittyImageFile(path string, cols, rows int) string {
	if path == "" || cols <= 0 || rows <= 0 || !kittyGraphicsLikelySupported() {
		return ""
	}
	return "\x1b_Ga=T,f=100,t=f,i=" + strconv.Itoa(kittyFaceImageID) + ",c=" + strconv.Itoa(cols) + ",r=" + strconv.Itoa(rows) + ",U=1,z=-1,q=2;" + base64.StdEncoding.EncodeToString([]byte(path)) + "\x1b\\"
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

func kittyFaceUpload(railWidth int) string {
	cols, rows := railFaceRows(railWidth)
	face := kittyImageFile(kittyFaceFile(), cols, rows)
	if face == "" {
		return ""
	}
	return kittyFaceDelete() + face
}

func kittyFacePlaceholderRow(row, cols int) string {
	if cols <= 0 {
		return ""
	}
	id := uint32(kittyFaceImageID)
	color := "\x1b[38;2;" + strconv.Itoa(int(id>>16&0xff)) + ";" + strconv.Itoa(int(id>>8&0xff)) + ";" + strconv.Itoa(int(id&0xff)) + "m"
	var b strings.Builder
	b.WriteString(color)
	b.WriteRune(kitty.Placeholder)
	b.WriteRune(kitty.Diacritic(row))
	b.WriteRune(kitty.Diacritic(0))
	b.WriteRune(kitty.Diacritic(int(id >> 24)))
	for col := 1; col < cols; col++ {
		b.WriteRune(kitty.Placeholder)
	}
	b.WriteString("\x1b[39m")
	return b.String()
}

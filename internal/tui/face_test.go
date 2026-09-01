package tui

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestKittyImageEncodesEmbeddedFaceAtRailWidth(t *testing.T) {
	t.Setenv("EITRI_KITTY_IMAGES", "1")
	cols, rows := railFaceRows(30)
	if cols != 24 || rows != 12 {
		t.Fatalf("railFaceRows(30) = %dx%d, want 24x12", cols, rows)
	}

	path := kittyFaceFile()
	if path == "" {
		t.Fatalf("kittyFaceFile returned empty path")
	}
	img := kittyImageFile(path, cols, rows)
	if !strings.HasPrefix(img, "\x1b_Ga=T,f=100,t=f,c=24,r=12,z=1;") {
		t.Fatalf("kitty image header missing file-transfer constraints: %q", img[:min(len(img), 100)])
	}
	if !strings.HasSuffix(img, "\x1b\\") {
		t.Fatalf("kitty image must end with ST terminator")
	}
	if !strings.Contains(img, base64.StdEncoding.EncodeToString([]byte(path))) {
		t.Fatalf("kitty image payload does not include encoded face path")
	}
}

func TestStyledRailBottomReservesFaceRailWithoutInlineImage(t *testing.T) {
	t.Setenv("EITRI_KITTY_IMAGES", "1")
	rail := styledRailWithFace("STATS\nCONTEXT\nMODEL", 24, 30)
	if strings.Contains(rail, "\x1b_G") {
		t.Fatalf("styled rail must reserve space without inline Kitty graphics: %q", rail)
	}
	if got := strings.Count(rail, "\n│"); got < 14 {
		t.Fatalf("styled rail reserved %d face-border rows, want at least 12: %q", got, rail)
	}
}

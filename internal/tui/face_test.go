package tui

import (
	"encoding/base64"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi/kitty"
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
	if !strings.HasPrefix(img, "\x1b_Ga=T,f=100,t=f,i=1162433618,c=24,r=12,U=1,z=-1,q=2;") {
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

func TestStyledRailWithFaceHasContinuousLeftBorder(t *testing.T) {
	t.Setenv("EITRI_KITTY_IMAGES", "1")
	rail := styledRailWithFace("STATS\nCONTEXT\nMODEL", 24, 30)
	lines := strings.Split(rail, "\n")
	for row, line := range lines {
		if row == 0 || row == len(lines)-1 {
			continue
		}
		if !strings.HasPrefix(line, "│") {
			t.Fatalf("rail row %d has no left border: %q", row, line)
		}
	}
}

func TestClockTickDoesNotRedrawFace(t *testing.T) {
	t.Setenv("EITRI_KITTY_IMAGES", "1")
	m := NewModelCfg(Dependencies{Rail: NewRail("provider", "model", "low", true, "session", "/tmp/session")})
	m = resizeTo(t, m, 120, 30)

	nm, cmd := m.Update(clockTickMsg{})
	m = asModel(t, nm)
	if cmd == nil {
		t.Fatalf("clock tick must schedule the next clock tick")
	}
	if batch, ok := cmd().(tea.BatchMsg); ok && len(batch) > 1 {
		t.Fatalf("clock tick scheduled %d commands; want only the next clock tick, not a face redraw", len(batch))
	}
}

func TestStreamingFollowReanchorsFaceAfterRendererScroll(t *testing.T) {
	t.Setenv("EITRI_KITTY_IMAGES", "1")
	m := NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Events: NewEventFeed(),
		Rail:   NewRail("provider", "model", "low", true, "session", "/tmp/session"),
	})
	m = resizeTo(t, m, 120, 31)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	feed := m.runtime.events
	_, cmd := m.Update(eventMsg{update: Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: strings.Repeat("word ", 200)}}})
	close(feed.updates)
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("streaming event command = %T, want redraw and event wait batch", cmd())
	}
	for _, batched := range batch {
		if _, ok := batched().(faceDrawMsg); ok {
			return
		}
	}
	t.Fatal("streaming follow must re-anchor the face after the renderer may scroll")
}

func TestMouseWheelDoesNotRedrawProtectedFace(t *testing.T) {
	t.Setenv("EITRI_KITTY_IMAGES", "1")
	m := NewModelCfg(Dependencies{Rail: NewRail("provider", "model", "low", true, "session", "/tmp/session")})
	m = resizeTo(t, m, 120, 30)

	_, cmd := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 2, Y: 2})
	if cmd != nil {
		t.Fatalf("mouse-wheel command = %T, want no corrective face redraw", cmd())
	}
}

func TestKittyFaceUsesVirtualUploadAndInBandPlaceholders(t *testing.T) {
	t.Setenv("EITRI_KITTY_IMAGES", "1")
	upload := kittyFaceUpload(30)
	if !strings.Contains(upload, ",U=1,z=-1,") {
		t.Fatalf("face upload is not virtual and behind text: %q", upload)
	}
	if strings.Contains(upload, "\x1b[") {
		t.Fatalf("face upload uses absolute cursor positioning: %q", upload)
	}

	rail := styledRailWithFace("STATS\nCONTEXT\nMODEL", 24, 30)
	if !strings.ContainsRune(rail, kitty.Placeholder) {
		t.Fatal("rail does not contain in-band Kitty placeholders")
	}
	if strings.Contains(rail, "\x1b_G") {
		t.Fatal("rail frame contains out-of-band Kitty graphics commands")
	}
}

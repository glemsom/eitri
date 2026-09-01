package tui

import (
	"encoding/base64"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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

func TestStreamingFollowRedrawsFaceAfterAutoScroll(t *testing.T) {
	t.Setenv("EITRI_KITTY_IMAGES", "1")
	m := NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Events: NewEventFeed(),
		Rail:   NewRail("provider", "model", "low", true, "session", "/tmp/session"),
	})
	m = resizeTo(t, m, 120, 30)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	if !m.tx.histFollow {
		t.Fatalf("precondition: streaming turn should start in follow mode")
	}

	nm, cmd := m.Update(eventMsg{update: Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: strings.Repeat("word ", 200)}}})
	m = asModel(t, nm)
	if cmd == nil {
		t.Fatalf("streaming auto-follow must schedule a face redraw after content moves")
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, batched := range batch {
			if _, ok := batched().(faceDrawMsg); ok {
				return
			}
		}
	}
	t.Fatalf("streaming auto-follow did not schedule a face redraw")
}

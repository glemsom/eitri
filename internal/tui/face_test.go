package tui

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi/kitty"

	"github.com/glemsom/eitri/internal/config"
)

func TestKittyImageEncodesEmbeddedFaceAtRailWidth(t *testing.T) {
	t.Cleanup(CleanupKittyFace) // the scratch PNG leaks nothing on the test host
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
	t.Cleanup(CleanupKittyFace) // the scratch PNG leaks nothing on the test host
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
	t.Cleanup(CleanupKittyFace) // the scratch PNG leaks nothing on the test host
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

// faceUpload runs one faceDrawMsg against m and requires it to issue a kitty
// raw upload, returning the model with the upload's dirty state applied.
func faceUpload(t *testing.T, m Model) Model {
	t.Helper()
	nm, cmd := m.Update(faceDrawMsg{})
	m = asModel(t, nm)
	if cmd == nil {
		t.Fatal("face draw issued no command")
	}
	if _, ok := cmd().(tea.RawMsg); !ok {
		t.Fatalf("face draw command = %T, want a kitty raw upload", cmd())
	}
	return m
}

func TestFaceUploadsOnceAtBootThenIdles(t *testing.T) {
	t.Cleanup(CleanupKittyFace) // the scratch PNG leaks nothing on the test host
	t.Setenv("EITRI_KITTY_IMAGES", "1")
	m := NewModelCfg(Dependencies{Rail: NewRail("provider", "model", "low", true, "session", "/tmp/session")})
	m = resizeTo(t, m, 120, 31) // bubbletea delivers one WindowSizeMsg at boot

	m = faceUpload(t, m) // the boot upload
	if m.faceDirty {
		t.Fatal("successful upload must clear the face-dirty flag")
	}

	// A stray face draw (what the old 50 ms polling loop delivered) must be a
	// no-op while idle: no upload and no re-arm.
	nm, cmd := m.Update(faceDrawMsg{})
	m = asModel(t, nm)
	if cmd != nil {
		t.Fatalf("idle face draw scheduled work: %T", cmd())
	}
	if m.faceDirty {
		t.Fatal("an idle no-op face draw must not re-dirty the face")
	}
}

func TestResizeReuploadsFace(t *testing.T) {
	t.Cleanup(CleanupKittyFace) // the scratch PNG leaks nothing on the test host
	t.Setenv("EITRI_KITTY_IMAGES", "1")
	m := NewModelCfg(Dependencies{Rail: NewRail("provider", "model", "low", true, "session", "/tmp/session")})
	m = resizeTo(t, m, 120, 31)
	m = faceUpload(t, m)

	// A terminal resize moves the face: the next face draw must re-upload.
	m = resizeTo(t, m, 130, 35)
	m = faceUpload(t, m)

	// ...and the new geometry is clean again until the next damage.
	nm, cmd := m.Update(faceDrawMsg{})
	m = asModel(t, nm)
	if cmd != nil {
		t.Fatalf("post-resize idle face draw scheduled work: %T", cmd())
	}
}

func TestRailWidthChangeReuploadsFace(t *testing.T) {
	t.Cleanup(CleanupKittyFace) // the scratch PNG leaks nothing on the test host
	t.Setenv("EITRI_KITTY_IMAGES", "1")
	m := NewModelCfg(Dependencies{
		Rail:   NewRail("provider", "model", "low", true, "session", "/tmp/session"),
		Config: testConfig(40),
	})
	m = resizeTo(t, m, 120, 38)
	m = faceUpload(t, m)

	// Ctrl+z widens the rail, changing the face's column count: the next face
	// draw must re-upload rather than stay idle.
	nm, cmd := m.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	m = asModel(t, nm)
	if cmd == nil {
		t.Fatal("rail-width change must arm a face draw")
	}
	if _, ok := cmd().(faceDrawMsg); !ok {
		t.Fatalf("rail-width change armed %T, want a face draw tick", cmd())
	}
	m = faceUpload(t, m)
}

func TestThemeChangeReuploadsFace(t *testing.T) {
	t.Cleanup(CleanupKittyFace) // the scratch PNG leaks nothing on the test host
	t.Setenv("EITRI_KITTY_IMAGES", "1")
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, _ string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(),
		Save:   func(config.Config) error { return nil },
		Rail:   NewRail("provider", "model", "low", true, "session", "/tmp/session"),
	})
	m = resizeTo(t, m, 120, 31)
	m = faceUpload(t, m)

	// Change the appearance in settings, save, and close the overlay: the
	// theme swap is face damage and the next face draw must re-upload.
	m = keypress(t, m, "ctrl+,")
	for i := fieldProvider; i < fieldTheme; i++ {
		m = keypress(t, m, "enter")
	}
	m = keypress(t, m, "right") // cycle the theme
	for i := fieldTheme; i < fieldSave; i++ {
		m = keypress(t, m, "enter")
	}
	m = keypress(t, m, "enter") // save the draft
	if m.settings == nil {
		t.Fatal("settings overlay must stay open after Save")
	}
	nm, cmd := m.Update(namedKey("esc")) // close the overlay
	m = asModel(t, nm)
	if cmd == nil {
		t.Fatal("closing settings after a theme change must arm a face draw")
	}
	m = faceUpload(t, m)
}

func TestNonFaceSettingsSaveDoesNotReuploadFace(t *testing.T) {
	t.Cleanup(CleanupKittyFace) // the scratch PNG leaks nothing on the test host
	t.Setenv("EITRI_KITTY_IMAGES", "1")
	m := NewModelCfg(Dependencies{
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(),
		Save:   func(config.Config) error { return nil },
		Rail:   NewRail("provider", "model", "low", true, "session", "/tmp/session"),
	})
	m = resizeTo(t, m, 120, 31)
	m = faceUpload(t, m)

	// A save that touches no face input (max turns, not theme/rail width) must
	// not re-upload: the face stays clean, so closing the overlay arms nothing.
	m = keypress(t, m, "ctrl+,")
	for i := fieldProvider; i < fieldMaxTurns; i++ {
		m = keypress(t, m, "enter")
	}
	m = keypress(t, m, "right") // bump MaxTurns by one step
	for i := fieldMaxTurns; i < fieldSave; i++ {
		m = keypress(t, m, "enter")
	}
	m = keypress(t, m, "enter")          // save the draft
	nm, cmd := m.Update(namedKey("esc")) // close the overlay
	m = asModel(t, nm)
	if cmd != nil {
		t.Fatalf("non-face settings close armed a face draw: %T", cmd())
	}
}

func TestClockTickDoesNotRedrawFace(t *testing.T) {
	t.Cleanup(CleanupKittyFace) // the scratch PNG leaks nothing on the test host
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
	t.Cleanup(CleanupKittyFace) // the scratch PNG leaks nothing on the test host
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
	t.Cleanup(CleanupKittyFace) // the scratch PNG leaks nothing on the test host
	t.Setenv("EITRI_KITTY_IMAGES", "1")
	m := NewModelCfg(Dependencies{Rail: NewRail("provider", "model", "low", true, "session", "/tmp/session")})
	m = resizeTo(t, m, 120, 30)

	_, cmd := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: 2, Y: 2})
	if cmd != nil {
		t.Fatalf("mouse-wheel command = %T, want no corrective face redraw", cmd())
	}
}

func TestKittyFaceUsesVirtualUploadAndInBandPlaceholders(t *testing.T) {
	t.Cleanup(CleanupKittyFace) // the scratch PNG leaks nothing on the test host
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

func TestCleanupKittyFaceRemovesScratchFile(t *testing.T) {
	t.Setenv("EITRI_KITTY_IMAGES", "1")
	path := kittyFaceFile()
	if path == "" {
		t.Fatal("kittyFaceFile returned an empty path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("scratch file %s missing right after creation: %v", path, err)
	}
	CleanupKittyFace()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch file %s still present after CleanupKittyFace (stat err = %v)", path, err)
	}
	CleanupKittyFace() // must be idempotent: a second call is a no-op
}

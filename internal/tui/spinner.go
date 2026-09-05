package tui

import (
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Busy spinner: the animated braille indicator that runs while a turn works,
// plus the motion gate that decides when animation is allowed. The default is an
// OpenCode-style braille frame set advanced every busySpinnerTick; the static
// "… thinking" line (render.go's busyLine) is the reduced-motion fallback.
// The gate disables all animation when the user opts out (EITRI_NO_MOTION) or
// the locale cannot render braille (non-UTF-8) — the same gate keeps the
// spinner's animated frames off for those environments too.
const busySpinnerTick = 80 * time.Millisecond

// busySpinnerFrames is the OpenCode-style braille frame set.
var busySpinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

type spinnerTickMsg struct{}

func spinnerTick() tea.Cmd {
	return tea.Tick(busySpinnerTick, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// motion gate: animated indicators run unless the user opts out (EITRI_NO_MOTION set) or the locale cannot render braille (non-UTF-8), mirroring the benchmark's reduced-motion + ASCII-fallback requirements (§4.3).
var (
	localeOnce sync.Once
	localeUTF8 bool
)

// localeSupportsUTF8 sniffs the process locale once: an explicit non-UTF-8 locale (LC_ALL/LC_CTYPE/LANG without UTF-8/UTF8) means non-ASCII glyphs and braille would render as tofu, so the surface degrades to ASCII (see glyphs.go and motionEnabled).
func localeSupportsUTF8() bool {
	localeOnce.Do(func() {
		localeUTF8 = true
		for _, v := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
			if l := os.Getenv(v); l != "" && !strings.Contains(strings.ToUpper(l), "UTF-8") && !strings.Contains(strings.ToUpper(l), "UTF8") {
				localeUTF8 = false // explicit non-UTF-8 locale: braille would render as tofu
				return
			}
		}
	})
	return localeUTF8
}

func motionEnabled() bool {
	if os.Getenv("EITRI_NO_MOTION") != "" {
		return false
	}
	return localeSupportsUTF8()
}

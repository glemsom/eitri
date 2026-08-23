package tui

import (
	"context"
	"strings"
	"testing"
)

func TestKittyGraphicsFromEnv(t *testing.T) {
	for _, tp := range []string{"kitty", "ghostty", "wezterm", "KITTY", "Ghostty"} {
		if !kittyGraphicsFromEnv(tp) {
			t.Errorf("kittyGraphicsFromEnv(%q) = false, want true", tp)
		}
	}
	for _, tp := range []string{"", "xterm-256color", "alacritty", "tmux", "vscode", "kittyx"} {
		if kittyGraphicsFromEnv(tp) {
			t.Errorf("kittyGraphicsFromEnv(%q) = true, want false", tp)
		}
	}
}

func TestKittyGraphicsFromDA1(t *testing.T) {
	// Per the Kitty graphics spec, support is probed by sending the graphics
	// query action followed by a DA1 query; a supporting terminal answers both,
	// an unsupported one only the DA1.
	in := strings.NewReader("\x1b_Gi=31;OK\x1b\\")
	out := &strings.Builder{}
	if !kittyGraphicsFromDA1(out, in) {
		t.Fatal("kittyGraphicsFromDA1 with graphics reply = false, want true")
	}
	if got, want := out.String(), "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[c"; got != want {
		t.Errorf("probe written = %q, want %q", got, want)
	}

	in = strings.NewReader("\x1b[?62;c") // DA1 answer only: no graphics support
	if kittyGraphicsFromDA1(&strings.Builder{}, in) {
		t.Error("kittyGraphicsFromDA1 without graphics reply = true, want false")
	}

	in = strings.NewReader("")
	if kittyGraphicsFromDA1(&strings.Builder{}, in) {
		t.Error("kittyGraphicsFromDA1 with no response = true, want false")
	}
}

func TestDetectKittyGraphics(t *testing.T) {
	env := func(k string) string {
		if k == "TERM_PROGRAM" {
			return "kitty"
		}
		return ""
	}
	da1Called := false
	if !detectKittyGraphics(env, func() bool { da1Called = true; return true }) {
		t.Fatal("detectKittyGraphics(kitty env) = false, want true")
	}
	if da1Called {
		t.Error("DA1 fallback queried despite positive TERM_PROGRAM match")
	}

	env = func(string) string { return "" }
	if detectKittyGraphics(env, func() bool { return false }) {
		t.Error("detectKittyGraphics(unknown terminal, no DA1) = true, want false")
	}
	if !detectKittyGraphics(env, func() bool { return true }) {
		t.Error("detectKittyGraphics(unknown terminal, DA1 support) = false, want true")
	}
}

func TestModelKittyGraphicsFlag(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	m := NewModelCfg(Dependencies{Turn: func(context.Context, string, string) (TurnResult, error) { return TurnResult{}, nil }})
	m.splash = &Splash{state: &splashState{}} // splashFor is environment-gated; seed the module directly
	if m.kittyGraphics() {
		t.Error("model reports Kitty graphics for an unknown terminal")
	}

	t.Setenv("TERM_PROGRAM", "kitty")
	k := NewModelCfg(Dependencies{Turn: func(context.Context, string, string) (TurnResult, error) { return TurnResult{}, nil }, KittyDA1: func() bool { return false }, Splash: true})
	if !k.kittyGraphics() {
		t.Error("model should detect Kitty graphics from TERM_PROGRAM=kitty")
	}
	if k.splash == nil || k.splash.state == nil {
		t.Fatal("splash-enabled model must start with an active splash")
	}
	if !k.splash.state.kitty {
		t.Error("splashState should mirror the model's Kitty capability")
	}
	if !k.splash.state.kitty || !k.kittyGraphics() {
		t.Error("flag should agree across splashState and model")
	}
}

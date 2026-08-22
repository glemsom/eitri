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
	// A terminal answering CSI ? u with the Kitty graphics attribute (0x1000)
	// among its feature flags reports support; one without it does not.
	in := strings.NewReader("\x1b[?62;4100;4096u")
	out := &strings.Builder{}
	if !kittyGraphicsFromDA1(out, in) {
		t.Fatal("kittyGraphicsFromDA1 with 0x1000 flag = false, want true")
	}
	if got := out.String(); got != "\x1b[?u" {
		t.Errorf("query written = %q, want %q", got, "\x1b[?u")
	}

	in = strings.NewReader("\x1b[?62;22u")
	if kittyGraphicsFromDA1(&strings.Builder{}, in) {
		t.Error("kittyGraphicsFromDA1 without 0x1000 flag = true, want false")
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
	m.splash = &splashState{} // splashFor is environment-gated; seed the state directly
	if m.kittyGraphics() {
		t.Error("model reports Kitty graphics for an unknown terminal")
	}

	t.Setenv("TERM_PROGRAM", "kitty")
	k := NewModelCfg(Dependencies{Turn: func(context.Context, string, string) (TurnResult, error) { return TurnResult{}, nil }, KittyDA1: func() bool { return false }})
	if !k.kittyGraphics() {
		t.Error("model should detect Kitty graphics from TERM_PROGRAM=kitty")
	}
	if k.splash != nil {
		if !k.splash.kitty {
			t.Error("splashState should mirror the model's Kitty capability")
		}
		if !k.splash.kitty || !k.kittyGraphics() {
			t.Error("flag should agree across splashState and model")
		}
	}
}

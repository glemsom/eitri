package assets

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Tests for the desaturated dark palette and tinted shadows (issue #1178).
//
// These assert spec-derived outcomes at the embedded-asset seam: the dark
// theme moves from blue-navy to neutral charcoal, the accent is desaturated
// and warmed, all box-shadow tokens are tinted to the charcoal base (not pure
// black), and the surface ladder elevates off --bg. The light theme must
// remain unchanged.

// cssTokenValue returns the value of the named custom property in a token-root body.
func cssTokenValue(body, name string) (string, bool) {
	key := strings.TrimPrefix(name, "--")
	token := "--" + key + ":"
	for _, raw := range strings.Split(body, ";") {
		decl := strings.TrimSpace(raw)
		if !strings.HasPrefix(decl, token) {
			continue
		}
		return strings.TrimSpace(decl[len(token):]), true
	}
	return "", false
}

// hexChannels parses "#rgb" or "#rrggbb" into 8-bit channels.
func hexChannels(s string) (r, g, b int, ok bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "#") {
		return 0, 0, 0, false
	}
	hex := s[1:]
	switch len(hex) {
	case 3:
		expanded := make([]byte, 0, 6)
		for i := 0; i < len(hex); i++ {
			c := hex[i]
			expanded = append(expanded, c, c)
		}
		hex = string(expanded)
	case 6:
	default:
		return 0, 0, 0, false
	}
	vals := [3]int{}
	for i := range vals {
		n, err := strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
		if err != nil {
			return 0, 0, 0, false
		}
		vals[i] = int(n)
	}
	return vals[0], vals[1], vals[2], true
}

// saturationFraction returns saturation as a fraction of the max channel
// range (0..1), matching cssutils.py's definition.
func saturationFraction(r, g, b int) float64 {
	max, min := r, g
	if b > max {
		max = b
	}
	if b < min {
		min = b
	}
	if max == 0 {
		return 0
	}
	return float64(max-min) / float64(max)
}

// isLightRoot reports whether a parsed :root rule lives inside the light
// (prefers-color-scheme: light) media block.
func isLightRoot(css string, r cssRule) bool {
	return strings.Contains(css[:r.start], "prefers-color-scheme: light")
}

func TestEmbeddedCSSDarkPaletteCharcoalBase(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	var darkBody string
	for _, r := range tokenRoots(css) {
		if !isLightRoot(css, r) {
			darkBody = r.body
		}
	}
	if darkBody == "" {
		t.Fatalf("could not locate dark :root token body")
	}

	bg, _ := cssTokenValue(darkBody, "--bg")
	if bg != "#0d0d12" && bg != "#111118" {
		t.Errorf("dark --bg = %s, want neutral charcoal (#0d0d12 or #111118)", bg)
	}

	// Surface ladder must elevate off --bg toward a lighter charcoal, and
	// must not read blue-navy (a blue channel far above red/green).
	for _, name := range []string{"--surface", "--surface-raised"} {
		v, ok := cssTokenValue(darkBody, name)
		if !ok {
			t.Errorf("dark :root missing %s", name)
			continue
		}
		if v == bg {
			t.Errorf("dark %s == %s matches --bg; wants a slightly-lifted surface", name, v)
		}
		if r, g, b, ok := hexChannels(v); ok {
			if b > r+20 && b > g+20 {
				t.Errorf("dark %s = %s reads blue-navy; wants a neutral charcoal (< 20 blue lean)", name, v)
			}
		}
	}
}

func TestEmbeddedCSSDarkAccentDesaturatedWarm(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	var darkBody string
	for _, r := range tokenRoots(css) {
		if !isLightRoot(css, r) {
			darkBody = r.body
		}
	}

	accent, ok := cssTokenValue(darkBody, "--accent")
	if !ok {
		t.Fatalf("dark :root missing --accent")
	}
	r, g, b, ok := hexChannels(accent)
	if !ok {
		t.Fatalf("dark --accent = %s is not a hex color", accent)
	}
	if sat := saturationFraction(r, g, b); sat >= 0.8 {
		t.Errorf("dark --accent = %s saturation %.2f, want below 0.80 (desaturated)", accent, sat)
	}
	// Warm: red channel must dominate green and blue (red/orange lean, not
	// pink/magenta where blue would approach red).
	if r <= g || r <= b {
		t.Errorf("dark --accent = %s not warm (red %d must dominate green %d and blue %d)", accent, r, g, b)
	}
	hover, _ := cssTokenValue(darkBody, "--accent-hover")
	if hover == "" || hover == accent {
		t.Errorf("dark --accent-hover = %q, want a distinct lighter hover shade", hover)
	}
}

func TestEmbeddedCSSDarkShadowsTintedNotBlack(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	var darkBody string
	for _, r := range tokenRoots(css) {
		if !isLightRoot(css, r) {
			darkBody = r.body
		}
	}

	// Charcoal base channels of #0d0d12: r=g=13, b=18.
	charcoalTriple := regexp.MustCompile(`rgba\(\s*1[0-3],\s*1[0-3],\s*1[0-8]`)
	for _, name := range []string{"--shadow-sm", "--shadow-md", "--shadow-lg", "--shadow-btn", "--shadow-float"} {
		v, ok := cssTokenValue(darkBody, name)
		if !ok {
			t.Errorf("dark :root missing %s", name)
			continue
		}
		if strings.Contains(v, "rgba(0, 0, 0,") {
			t.Errorf("dark %s = %s uses pure black shadow; wants a charcoal-tinted shadow", name, v)
		}
		if !charcoalTriple.MatchString(v) {
			t.Errorf("dark %s = %s not tinted to the charcoal base (#0d0d12)", name, v)
		}
	}
}

func TestEmbeddedCSSLightThemeUnchanged(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	var lightBody string
	for _, r := range tokenRoots(css) {
		if isLightRoot(css, r) {
			lightBody = r.body
		}
	}
	if lightBody == "" {
		t.Fatalf("could not locate light :root token body")
	}
	bg, _ := cssTokenValue(lightBody, "--bg")
	if bg != "#f5f5f7" {
		t.Errorf("light --bg = %s changed; light theme must remain unchanged (#f5f5f7)", bg)
	}
}

// cssPropertyValue returns the value of a plain (non custom-prop) CSS
// property declared in a rule body, e.g. `background`.
func cssPropertyValue(body, name string) (string, bool) {
	for _, raw := range strings.Split(body, ";") {
		decl := strings.TrimSpace(raw)
		prefix := name + ":"
		if !strings.HasPrefix(decl, prefix) && !strings.HasPrefix(decl, name+" ") {
			continue
		}
		if i := strings.IndexByte(decl, ':'); i >= 0 {
			return strings.TrimSpace(decl[i+1:]), true
		}
	}
	return "", false
}

// TestEmbeddedCSSSidebarPanelsDistinct checks that the sessions, tools and
// thinking sidebar panels carry three distinct background tints so the
// sections are visually differentiated by subtle shade differences.
func TestEmbeddedCSSSidebarPanelsDistinct(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	seen := map[string]string{}
	for _, r := range parseCSSRules(css) {
		name := ""
		switch {
		case strings.Contains(r.selector, "#session-panel"):
			name = "sessions"
		case strings.Contains(r.selector, "#tool-activity"):
			name = "tools"
		case strings.Contains(r.selector, "#thinking-panel"):
			name = "thinking"
		}
		if name == "" {
			continue
		}
		if v, ok := cssPropertyValue(r.body, "background"); ok {
			seen[name] = v
		}
	}
	if len(seen) != 3 {
		t.Fatalf("expected tint for each of sessions/tools/thinking panels, got %d backgrounds: %v",
			len(seen), seen)
	}
	vals := make([]string, 0, 3)
	for _, v := range seen {
		vals = append(vals, v)
	}
	for i := 0; i < len(vals); i++ {
		for j := i + 1; j < len(vals); j++ {
			if vals[i] == vals[j] {
				t.Errorf("sidebar panels not visually differentiated: %s reused for two panels", vals[i])
			}
		}
	}
}

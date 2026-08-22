package tui

import (
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Kitty graphics capability detection: Eitri gates every Kitty-graphics
// feature (image embedding in the splash, etc.) on this flag so non-Kitty
// terminals never receive a single Kitty escape sequence.

// kittyGraphicsQuery is the primary device attributes (DA1) query that a
// Kitty-graphics-capable terminal answers with its feature flags.
const kittyGraphicsQuery = "\x1b[?u"

// kittyGraphicsAttr is the feature-flag bit terminals set in the DA1
// response when they implement the Kitty graphics protocol (Kitty ≥ 0.40,
// Ghosty, WezTerm).
const kittyGraphicsAttr = 0x1000

// kittyGraphicsFromEnv reports whether TERM_PROGRAM names a terminal family
// known to support the Kitty graphics protocol.
func kittyGraphicsFromEnv(termProgram string) bool {
	switch strings.ToLower(strings.TrimSpace(termProgram)) {
	case "kitty", "ghostty", "wezterm":
		return true
	default:
		return false
	}
}

// kittyGraphicsFromDA1 writes the CSI ? u query to w and parses one response
// from r, reporting whether the answer carries the Kitty graphics attribute.
// It is only consulted when TERM_PROGRAM names no known Kitty terminal; a
// terminal that never answers yields false.
func kittyGraphicsFromDA1(w io.Writer, r io.Reader) bool {
	if _, err := io.WriteString(w, kittyGraphicsQuery); err != nil {
		return false
	}
	buf := make([]byte, 64)
	n, err := readWithTimeout(r, buf, time.Second)
	if err != nil && n == 0 {
		return false
	}
	return da1ReportsKittyGraphics(string(buf[:n]))
}

// da1ReportsKittyGraphics parses a `CSI ? <flags> u` DA1 response and reports
// whether any flag includes the Kitty graphics bit. Malformed or empty input
// reports false.
func da1ReportsKittyGraphics(resp string) bool {
	resp = strings.TrimSpace(resp)
	if !strings.HasPrefix(resp, "\x1b[?") || !strings.HasSuffix(resp, "u") {
		return false
	}
	flags := strings.Split(strings.TrimSuffix(strings.TrimPrefix(resp, "\x1b[?"), "u"), ";")
	for _, f := range flags {
		v, err := strconv.ParseUint(strings.TrimSpace(f), 10, 32)
		if err != nil {
			continue
		}
		if v&kittyGraphicsAttr != 0 {
			return true
		}
	}
	return false
}

// detectKittyGraphics resolves the capability once at startup: TERM_PROGRAM
// decides first; only an unrecognized terminal falls back to querying the
// device attributes through the injected probe.
func detectKittyGraphics(env func(string) string, da1 func() bool) bool {
	if kittyGraphicsFromEnv(env("TERM_PROGRAM")) {
		return true
	}
	return da1 != nil && da1()
}

// readWithTimeout reads up to len(buf) bytes, giving up after d. The
// deadline guards against terminals that ignore the DA1 query entirely —
// without it Eitri would hang on startup waiting for a reply that never
// comes.
func readWithTimeout(r io.Reader, buf []byte, d time.Duration) (int, error) {
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := r.Read(buf)
		ch <- result{n, err}
	}()
	select {
	case res := <-ch:
		return res.n, res.err
	case <-time.After(d):
		return 0, errDA1Timeout
	}
}

var errDA1Timeout = &timeoutError{}

type timeoutError struct{}

func (*timeoutError) Error() string { return "timed out waiting for DA1 response" }

// liveKittyEnv reads from the process environment; the seam detectKittyGraphics tests against.
func liveKittyEnv(k string) string { return os.Getenv(k) }

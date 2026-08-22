package tui

import (
	"io"
	"os"
	"strings"
	"time"
)

// Kitty graphics capability detection: Eitri gates every Kitty-graphics
// feature (image embedding in the splash, etc.) on this flag so non-Kitty
// terminals never receive a single Kitty escape sequence.

// kittyGraphicsProbe is the protocol's own query action (a=q, a 1×1 pixel
// dummy) per the Kitty graphics spec's "Querying support" section; it is sent
// together with the primary device attributes (DA1) query so a supporting
// terminal answers both and an unsupported one only the DA1.
const kittyGraphicsProbe = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"

// kittyDA1Query is the primary device attributes query that every VT-style
// terminal answers, giving the probe pair its discriminator.
const kittyDA1Query = "\x1b[c"

// kittyGraphicsFromDA1 writes the graphics query action followed by the DA1
// query to w and reads the responses from r, reporting whether a graphics
// reply arrived. It is only consulted when TERM_PROGRAM names no known Kitty
// terminal; a terminal that never answers yields false.
func kittyGraphicsFromDA1(w io.Writer, r io.Reader) bool {
	if _, err := io.WriteString(w, kittyGraphicsProbe+kittyDA1Query); err != nil {
		return false
	}
	buf := make([]byte, 256)
	n, err := readWithTimeout(r, buf, time.Second)
	if err != nil && n == 0 {
		return false
	}
	return apcGraphicsReply(string(buf[:n]))
}

// apcReportsKittyGraphics reports whether a raw response stream contains an
// APC-wrapped reply to the graphics query action (`ESC _ G …`); a terminal
// without graphics support answers only the DA1 query.
func apcGraphicsReply(resp string) bool {
	return strings.Contains(resp, "\x1b_G")
}

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

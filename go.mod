module github.com/glemsom/eitri

go 1.27

toolchain go1.27.1

require (
	charm.land/bubbles/v2 v2.2.1
	charm.land/bubbletea/v2 v2.0.9
	charm.land/glamour/v2 v2.0.1
	charm.land/lipgloss/v2 v2.0.6
	github.com/charmbracelet/colorprofile v0.4.3
	github.com/charmbracelet/ultraviolet v0.0.0-20260903151058-ae99b731b8c5
	github.com/charmbracelet/x/ansi v0.11.8
	golang.org/x/term v0.46.0
)

require (
	github.com/alecthomas/chroma/v2 v2.27.0 // indirect
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/charmbracelet/x/exp/slice v0.0.0-20260902165432-6f6ad8b37b0a // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/dlclark/regexp2/v2 v2.7.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.29 // indirect
	github.com/microcosm-cc/bluemonday v1.0.27 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/xo/terminfo v1.0.0 // indirect
	github.com/yuin/goldmark v1.8.6 // indirect
	github.com/yuin/goldmark-emoji v1.0.6 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

// EITRI PATCH: the upstream ultraviolet scroll optimization corrupts the
// alternate screen when a live region scrolls while a line's content changes
// (it leaves the old line painted), which Eitri's streaming chain-of-thought
// reliably triggers — CoT tokens showed in two screen positions. The local fork
// disables that optimization in SetScrollOptim; see
// third_party/ultraviolet/terminal_renderer.go. Drop this replace when upstream
// fixes scrollOptimize (still broken as of
// ultraviolet@v0.0.0-20260910203606-6c9e17dc7a16).
replace github.com/charmbracelet/ultraviolet => ./third_party/ultraviolet

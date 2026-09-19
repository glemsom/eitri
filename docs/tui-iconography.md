# TUI iconography

The visual mark system for the TUI surface: what a "mark" is, the two tiers it splits into, and the rules that keep emoji from eroding the theme. This supersedes the ASCII-fallback charter; see [adr/0001](adr/0001-assume-rich-terminal.md) and [adr/0002](adr/0002-emoji-identity-tier.md).

## Vocabulary

**Mark**:
Any rendered decorative marker on the surface — the umbrella term for both tiers below.

**Glyph**:
A monochrome mark whose colour comes from the theme. Width 1, inline-use friendly, safe in repetition.

**Icon**:
An emoji-presentation mark carrying its own colour, used only as a once-per-block identity marker. Width 2, no fallback.

The interactive surface assumes a UTF-8, truecolor terminal; there is no ASCII fallback path.

## The two tiers

| | Glyph | Icon |
|---|---|---|
| colour | theme role | intrinsic (font) |
| width | 1 | 2 (VS16-normalized) |
| density | inline, repeated | once per block |
| fallback | none | none |
| where | outcomes, panes, rules, list bullets, inline reasoning | role chips, tool categories, section headers, phase badges, brand |

**The rule:** an icon may appear only where the mark carries identity, never where colour encodes state. Success/failure (`✓`/`✗`), the error/stopped pane borders, and the muted reasoning hue stay glyphs so the theme's semantics survive.

## Icon set (v1)

| Surface | Icon |
|---|---|
| user role chip | 🧑 |
| assistant role chip | ⚒️ |
| bash | 🐚 |
| open_in_browser | 🌐 |
| skill | ✨ |
| generic tool | 🧰 |
| reasoning pane header (expanded only) | 🧠 |
| rail: stats / context / model | 📊 🧭 ⚙️ |
| help: composer / navigation / panes / actions | ✍️ 🧭 🪟 ⚡ |
| settings: model / credentials / reasoning / appearance / workspace | ⚙️ 🔑 🧠 🎨 📁 |
| phase: reasoning / working / answering | 🧠 / ⚒️ / ✍️ |
| idle brand | ⚒️ Eitri |

Brand reuse is allowed (⚒️ serves the assistant chip and the working phase). Semantic collision is not: MODEL is ⚙️, never 🧠.

## Presentation rules

- Every icon is VS16-normalized (`U+FE0F`) so its cell width is a stable 2.
- ZWJ sequences, skin-tone modifiers, and other compound emoji are banned: their width is font-dependent and would defeat `ansi.StringWidth`.
- Icons are declared in `glyphInventory` with a `tier` of `icon`; the retired `ascii` and per-entry `width` fields are gone. `lookup()` remains the single resolution seam.

## Copy and payload

Two directions, never mixed:

- **Injected** chrome icons never enter payload paths — no icon inside tool-result bodies, code blocks, the user-prompt echo, or the clipboard copy path.
- **Received** emoji the model or user writes are preserved verbatim through markdown rendering, drag-select, and OSC 52 copy. They are never stripped — only the chrome is iconography.

## Testing

- Registry test: every `icon` entry is VS16-normalized, width 2, and contains no ZWJ or modifier codepoint.
- Copy test: a model-emitted emoji (including a VS16 pair) survives `plainLines()` → drag-select → OSC 52.
- Snapshot frames across all themes are the visual regression gate.

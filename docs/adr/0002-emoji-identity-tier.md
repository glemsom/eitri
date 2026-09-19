# Emoji icons are confined to the identity tier and never carry semantic hue

The glyph charter held that "colour comes from theme roles, never from the glyph's own presentation", and the surface is skinned by ten theme palettes. Introducing emoji admits marks whose colour is intrinsic and un-themeable. We decided to relax the invariant narrowly: emoji are permitted only where a mark is neutral *identity* — role chips, tool categories, section headers, phase badges, the brand — and never where colour encodes state (✓/✗ outcomes, the error/stopped panes, the muted reasoning hue). Those stay typographic and theme-coloured, so "state is colour" is preserved.

## Consequences

- Icons are VS16-normalized to width 2, and ZWJ sequences, skin-tone modifiers, and other compound emoji are banned from the registry so `ansi.StringWidth` stays authoritative.
- Icons carry no fallback (see ADR 0001).
- Emoji that arrive *in payload* — written by the model or the user — are preserved verbatim through markdown rendering, drag-select, and OSC 52 copy; emoji *injected* as chrome never enter payload or copy paths.

## Considered options

- Force text presentation (VS15) so emoji inherit the theme foreground (rejected: monochrome "emoji" defeat the purpose).
- Abandon intrinsic-colour emoji entirely (rejected: warmth is the goal).

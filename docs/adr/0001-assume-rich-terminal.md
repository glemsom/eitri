# Assume a UTF-8, truecolor terminal and retire the ASCII fallback subsystem

The TUI carried an ASCII-fallback subsystem — `g()`'s locale branch, the `ascii` field on every `glyphInventory` entry, `localeSupportsUTF8()`, the ASCII composer caret, and the non-UTF-8 braille spinner fallback — sourced from benchmark §3.6/§4.3. We decided the interactive surface may assume a UTF-8, truecolor terminal: the fallback is deleted and every mark renders its UTF-8 form unconditionally. This supersedes benchmark §3.6/§4.3, which must be updated outside this repo to match.

## Consequences

- `EITRI_ASCII_GLYPHS` and `localeSupportsUTF8()` are removed. Reduced motion is driven by `EITRI_NO_MOTION` alone.
- The composer caret is always `┃`; the busy indicator is always the braille spinner.
- The `notty` markdown theme is retired from `supportedThemes`; a stored `theme: "notty"` falls back to `dark`.
- The `glyphInventory` `ascii` field and the `g(utf8, ascii)` helper are deleted, inlining UTF-8 literals at the ~28 call sites.
- 27 test files lose their `EITRI_ASCII_GLYPHS` fallback assertions.

## Considered options

- Keep the fallback (rejected: it was a test-only seam, undocumented in README/docs, guarding a terminal shape we no longer support).
- Stage the removal behind the icon-tier work (rejected: a single simple end state was preferred over two migrations).

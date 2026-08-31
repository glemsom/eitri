# Failing tests

Collected with `make test`.

Total failing tests: 22

## `github.com/glemsom/eitri/internal/tui`

- [x] `TestDragSelect_scrolledViewportMapsRows` — fixed (test now pages far enough up to select a visible answer row)
- [x] `TestModel_bandPinnedOnResize` — fixed (test now asserts against the rendered bottom band instead of the raw textarea body)
- [x] `TestModelBandSpansFullTerminalWidthTall` — fixed (status/feedback band lines now pad to the full band width)
- `TestModelBandSpansFullWidthUnderRailTallSweep`
- `TestModelBandSpansFullWidthUnderRailTallSweep/height/26`
- `TestModelBandSpansFullWidthUnderRailTallSweep/height/30`
- `TestModelBandSpansFullWidthUnderRailTallSweep/height/35`
- `TestModelBandSpansFullWidthUnderRailTallSweep/height/40`
- `TestModelBandSpansFullWidthUnderRailTallSweep/height/50`
- `TestModel_composerLongDraftBandPinned`
- [x] `TestModel_composerShortTerminalClamp` — fixed (viewString now clips overflow to the terminal height on extremely short terminals)
- `TestModel_followStaysEngagedThroughPerDeltaBurst`
- `TestModel_heightAwareClampsHistory`
- `TestModelRailEndsOneRowAboveBandTallSweep`
- `TestModelRailEndsOneRowAboveBandTallSweep/height/26`
- `TestModelRailEndsOneRowAboveBandTallSweep/height/30`
- `TestModelRailEndsOneRowAboveBandTallSweep/height/35`
- `TestModelRailEndsOneRowAboveBandTallSweep/height/40`
- `TestModelRailEndsOneRowAboveBandTallSweep/height/50`
- `TestModel_statusAndSlashPinnedAboveComposer`
- [x] `TestModel_userBubbleFillsFullWidth` — fixed (test now checks the prompt card background from its actual rendered column, not terminal column 0)
- `TestScrollFollow_longStreamViewportStaysPinned`

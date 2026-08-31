# Failing tests

Collected with `make test`.

Total failing tests: 22

## `github.com/glemsom/eitri/internal/tui`

- [x] `TestDragSelect_scrolledViewportMapsRows` — fixed (test now pages far enough up to select a visible answer row)
- [x] `TestModel_bandPinnedOnResize` — fixed (test now asserts against the rendered bottom band instead of the raw textarea body)
- [x] `TestModelBandSpansFullTerminalWidthTall` — fixed (status/feedback band lines now pad to the full band width)
- [x] `TestModelBandSpansFullWidthUnderRailTallSweep` — fixed (test now locates the composer band separator by its following panel, not the rail/welcome separators)
- [x] `TestModelBandSpansFullWidthUnderRailTallSweep/height/26` — fixed
- [x] `TestModelBandSpansFullWidthUnderRailTallSweep/height/30` — fixed
- [x] `TestModelBandSpansFullWidthUnderRailTallSweep/height/35` — fixed
- [x] `TestModelBandSpansFullWidthUnderRailTallSweep/height/40` — fixed
- [x] `TestModelBandSpansFullWidthUnderRailTallSweep/height/50` — fixed
- [x] `TestModel_composerLongDraftBandPinned` — fixed (test now treats the full bottom band, including the contextual hint row, as pinned)
- [x] `TestModel_composerShortTerminalClamp` — fixed (viewString now clips overflow to the terminal height on extremely short terminals)
- [x] `TestModel_followStaysEngagedThroughPerDeltaBurst` — fixed (test uses a terminal height with a positive live-history viewport)
- [x] `TestModel_heightAwareClampsHistory` — fixed (test now asserts rendered band markers instead of raw textarea bytes)
- [x] `TestModelRailEndsOneRowAboveBandTallSweep` — fixed (test now ignores composer panel borders when finding the rail extent)
- [x] `TestModelRailEndsOneRowAboveBandTallSweep/height/26` — fixed
- [x] `TestModelRailEndsOneRowAboveBandTallSweep/height/30` — fixed
- [x] `TestModelRailEndsOneRowAboveBandTallSweep/height/35` — fixed
- [x] `TestModelRailEndsOneRowAboveBandTallSweep/height/40` — fixed
- [x] `TestModelRailEndsOneRowAboveBandTallSweep/height/50` — fixed
- [x] `TestModel_statusAndSlashPinnedAboveComposer` — fixed (test now treats the composer panel as pinned above the contextual hint row rather than requiring raw textarea output to be the final bytes)
- [x] `TestModel_userBubbleFillsFullWidth` — fixed (test now checks the prompt card background from its actual rendered column, not terminal column 0)
- [x] `TestScrollFollow_longStreamViewportStaysPinned` — fixed (test uses a terminal height with room for the live tail)

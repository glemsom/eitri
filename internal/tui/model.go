package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"

	"github.com/glemsom/eitri/internal/config"
)

// Busy-state motion (issue #211): the static "… thinking" line is the
// reduced-motion fallback; the default is an animated braille spinner advanced
// every busySpinnerTick while a turn runs. Braille spinners read calmer than
// bar spinners and are the modern agent-TUI default (benchmark §4.3).
const busySpinnerTick = 80 * time.Millisecond

// busySpinnerFrames is the OpenCode-style braille frame set. The muted style +
// glyph pairing keeps the working state glanceable without flashing.
var busySpinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// spinnerTickMsg advances the busy spinner by one frame. It is issued only
// while a turn runs and motion is enabled, so an idle TUI never ticks.
type spinnerTickMsg struct{}

// spinnerTick returns the command that delivers the next spinner frame after
// busySpinnerTick.
func spinnerTick() tea.Cmd {
	return tea.Tick(busySpinnerTick, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// motion gate: animated indicators run unless the user opts out (EITRI_NO_MOTION
// set) or the locale cannot render braille (non-UTF-8), mirroring the
// benchmark's reduced-motion + ASCII-fallback requirements (§4.3). The env
// opt-out is re-checked per call (tests flip it); the locale sniff is cached —
// the process locale cannot change mid-run.
var (
	localeOnce sync.Once
	localeUTF8 bool
)

// localeSupportsUTF8 sniffs the process locale once: an explicit non-UTF-8
// locale (LC_ALL/LC_CTYPE/LANG without UTF-8/UTF8) means non-ASCII glyphs and
// braille would render as tofu, so the surface degrades to ASCII (see glyphs.go
// and motionEnabled). An unset or UTF-8 locale keeps the full glyph set.
func localeSupportsUTF8() bool {
	localeOnce.Do(func() {
		localeUTF8 = true
		for _, v := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
			if l := os.Getenv(v); l != "" && !strings.Contains(strings.ToUpper(l), "UTF-8") && !strings.Contains(strings.ToUpper(l), "UTF8") {
				localeUTF8 = false // explicit non-UTF-8 locale: braille would render as tofu
				return
			}
		}
	})
	return localeUTF8
}

func motionEnabled() bool {
	if os.Getenv("EITRI_NO_MOTION") != "" {
		return false
	}
	return localeSupportsUTF8()
}

// busyLine renders the in-progress working indicator: the animated braille
// spinner with a "working" label when motion is enabled, the static "… thinking"
// line otherwise. The label stays plain so a monochrome terminal still reads it.
func busyLine(idx int) string {
	if !motionEnabled() || len(busySpinnerFrames) == 0 {
		return "… thinking"
	}
	return string(busySpinnerFrames[idx%len(busySpinnerFrames)]) + " working"
}

// Turn runs one agent conversation turn (user prompt -> assistant answer) over
// the shared engine seam. It is what the model depends on; both the real engine
// (internal/engine) and tests implement it, so conversation behavior is testable
// without a terminal or a live provider.
type Turn func(ctx context.Context, prompt string) (TurnResult, error)

// TurnResult is the outcome of one conversation turn: the final assistant
// answer plus any reasoning produced along the way. Reasoning is kept on a
// separate channel so the TUI can render it as a collapsible thinking block and
// never merge it into the answer (docs/spec.md §6, ticket #17).
type TurnResult struct {
	Answer    string
	Reasoning string
}

// message is one committed line of the conversation log.
type message struct {
	role      string // "you" or "eitri"
	content   string
	reasoning string // assistant chain-of-thought, rendered as a collapsible block
	streaming bool   // true while this assistant reply is still growing from the answer stream
	// thinkingExpanded is true while this turn's reasoning block is expanded
	// (issue #85): it defaults false (auto-collapsed) so reasoning never clogs
	// the final reply, and collapses back when the turn's answer lands.
	thinkingExpanded bool
}

// toolEntry is one rendered tool call in the transcript (issue #84): the tool
// name + args, plus the delivered result and its deterministic compression and
// file line-delta metadata. It renders as a compact one-line `⊕ tool  args`
// summary that collapses the result by default and expands on demand to the
// full inline output (never silently truncated). anchor is the index into
// messages of the "you" message whose turn this tool call belongs to, so View
// can interleave the entry chronologically after its triggering prompt.
type toolEntry struct {
	name       string
	args       string
	result     string
	lines      int
	dropped    int
	compressed bool
	added      int
	removed    int
	anchor     int // index of the triggering "you" message in messages
	complete   bool
	// startedAt/doneAt bound the tool's execution window for the elapsed-time
	// readout (benchmark §4.1: tool cards carry elapsed time). startedAt is set
	// when the tool begins, doneAt when its result lands; a running tool's live
	// elapsed re-renders while the busy spinner ticks.
	startedAt time.Time
	doneAt    time.Time
	// expanded is the per-entry expansion state toggled by a mouse click on the
	// entry's rows (click-to-expand, benchmark §4.4); alt+y remains the global
	// all-entries toggle.
	expanded bool
	// before/after/path carry the file content and host path a file-mutating
	// edit/write captured (issue #90): they back the review panel's inline diff
	// and open_in_browser escape hatch. Empty for non-edit tools and batch runs.
	before string
	after  string
	path   string
}

type turnDoneMsg struct {
	prompt    string
	answer    string
	reasoning string
	err       error
}

// telemetryUpdateMsg carries one queued live telemetry update from the engine
// seam into the UI loop. It is produced by a waiting command (telemetryWait) so
// the status strip refreshes live even with no keyboard input.
type telemetryUpdateMsg struct {
	update TelemetryUpdate
}

// skillDoneMsg reports a slash-command skill activation's result.
type skillDoneMsg struct {
	payload string
}

// discoverDoneMsg reports the outcome of an on-demand provider model discovery
// started when the Settings surface opened (issue #89 AC2). models is the
// discovered list on success; err carries the failure otherwise.
type discoverDoneMsg struct {
	models []string
	err    error
}

// SkillItem is one detected skill surfaced to the TUI's slash-command surface:
// its name is what `/skillname` matches and what the `/` completion list
// offers (eitri.md §2.3). The rail no longer carries a skills panel, so no
// scope or activation state is tracked TUI-side (issue #188).
type SkillItem struct {
	Name string
}

// SkillsSurface wires the TUI's slash-command activation to the run's tool
// layer (T8). Items lists the detected skill names; Activate runs one skill
// activation (the T8 `skill` tool via the engine/registry seam) and returns
// the activation payload. Nil means no skills were detected.
type SkillsSurface struct {
	Items    []SkillItem
	Activate func(ctx context.Context, name string) (string, error)
}

// Dependencies wires a Model to its environment: the conversation Turn, model
// discovery + loaded config for the Settings surface, and a persistence seam.
// Fields are optional; a zero Dependencies yields a plain chat-only model (the
// pre-settings default, kept for tests and lean embeds).
type Dependencies struct {
	// Turn drives one conversation turn (engine). Required for chat.
	Turn Turn
	// WorkspacePath is the project/read-only state surfaced above the
	// transcript (issue #82 AC1): the workspace directory the run operates in.
	// Empty means no workspace header is rendered (the plain chat default).
	WorkspacePath string
	// Models is the provider-discovered model list surfaced in Settings.
	Models []string
	// DiscoverModels, when non-nil, is an on-demand provider model-discovery
	// seam (issue #89 AC2): it is invoked when the Settings surface opens with
	// no pre-seeded Models list, and the panel reports loading/error states
	// rather than failing silently. Nil disables on-demand discovery (the
	// pre-seeded list, or the configured model alone, is shown).
	DiscoverModels func(ctx context.Context) ([]string, error)
	// Config is the loaded config seeded into the Settings draft.
	Config config.Config
	// Save persists a Settings edit to the config layer. When nil, Settings can
	// still be opened/dismissed but saving is a no-op (view-only).
	Save func(config.Config) error
	// SaveBack, when non-nil, is invoked with updated settings after Save so
	// the app can refresh its in-process view.
	SaveBack func(config.Config)
	// Skills, when non-nil, backs the slash-command surface: `/skillname`
	// activation and the `/` completion list. Nil disables `/skillname`
	// commands (no skills). The right rail never renders skills (issue #188).
	Skills *SkillsSurface
	// Telemetry, when non-nil, renders the live bottom status strip (issue #86):
	// model, effort, thinking, turns/max, cost, and the cache hit-ratio gauge,
	// fed live from the engine seam. Nil disables the strip.
	Telemetry *Telemetry
	// Stream, when non-nil, feeds the live assistant answer-text stream (issue
	// #83): the engine's AnswerStream deltas arrive here and the in-progress
	// assistant message re-renders in place, growing token by token instead of
	// one full-reply dump on completion. Nil falls back to the historical
	// blocking answer-on-completion behaviour.
	Stream *Streamer
	// Tools, when non-nil, feeds the live tool-call stream (issue #84): each
	// ToolCallEvent/ToolResultEvent arrives here and renders as a compact,
	// collapsed `⊕ tool  args` one-liner that expands to the full result.
	// Nil disables tool entries (the pre-seam default).
	Tools *ToolFeed
	// OpenInBrowser is the review panel's open_in_browser escape hatch (issue
	// #90): a host-side seam that launches a changed file's path in the host
	// browser/editor so a diff too rich for the terminal is reviewable off-
	// screen. Nil disables the escape hatch (the panel still shows the inline
	// diff; issue #90 AC2 independently).
	OpenInBrowser func(ctx context.Context, target string) error
	// Rail, when non-nil, enables the toggleable right context rail (issue
	// #88): a fixed-width pane alongside the transcript showing STATS / CONTEXT
	// / MODEL, fed from the telemetry surface. Nil hides the rail (the plain
	// chat default).
	Rail *Rail
	// Clipboard writes text to the system clipboard (issue #123): Ctrl+O and
	// /copy copy the full transcript through it. Nil falls back to the
	// atotto/clipboard package default.
	Clipboard func(text string) error
	// OSC52Out, when non-nil, is the terminal output the copy fallback writes
	// its OSC 52 sequence to (issue #201): when the Clipboard path fails (e.g.
	// no xclip/wl-clipboard), the copy emits the OSC 52 clipboard sequence to
	// this writer and the terminal puts the text on the system clipboard. Nil
	// defaults to os.Stdout, so the fallback works out of the box in the
	// interactive TUI.
	OSC52Out io.Writer
}

// Model is the Bubble Tea state backing the TUI. It owns a single textarea
// composer and the conversation log, and drives agent turns over the injected
// Turn seam. It renders through the alternate screen (T1 pivot, issue #119), so
// every frame is a clean full-surface repaint and the history scroll/clip lives
// in a native viewport rather than a primary-buffer compensation layer. It also
// hosts the Settings surface (ctrl+s) and the interactive max-turns continuation
// prompt.
type Model struct {
	composer textarea.Model
	turn     Turn
	deps     Dependencies

	// theme is the styling surface for the TUI chrome (issue #178): a palette
	// registry plus derived styles the whole surface draws from. It defaults
	// to the pre-seam dark palette; swapping it re-skins the chrome with no
	// consumer change.
	theme Theme

	messages []message
	// busy is true from the moment a turn is submitted until its final answer
	// lands (or the turn errors). While busy the composer is inert (ticket #57)
	// and the spinner advances.
	busy bool
	// spinner is the current busy-spinner frame index, advanced by
	// spinnerTickMsg while busy (issue #211). It is 0 when no turn runs.
	spinner int
	// vimNormal is the composer's vim normal mode (benchmark §4.4): esc toggles
	// it, and while active h/j/k/l navigate the draft instead of inserting
	// text (see vimKey).
	vimNormal bool

	// settings state: non-nil means the Settings surface is open.
	settings *settingsForm
	savedMsg string

	// interactive continuation: continueReq/continueResp carry the max-turns
	// prompt between the running engine goroutine and the main loop. prompting
	// is true while a decision is being awaited.
	continueReq  chan struct{}
	continueResp chan bool
	prompting    bool
	// reasoningEffort is the run's reasoning-effort tier, rendered in the
	// collapsed thinking hint (issue #85 AC2: "🤔 1.4k tok · medium"). Empty
	// when reasoning is disabled, so the hint drops the effort suffix.
	reasoningEffort string

	// skills is the live list backing the slash-command completion, refreshed
	// from the Dependencies snapshot at construction.
	skills []SkillItem

	// slashIdx is the combo completion's currently selected candidate index into
	// slashCandidates for the composer's current `/...` line (issue #87 AC1). It
	// is the highlighted row of the on-screen completion list; 0 (the built-in
	// /settings row) by default, advanced as the user tabs-through or edits.
	slashIdx int
	// slashPrefix is the raw `/...` prefix the user typed into the composer
	// before any tab-autocompletion filled the remainder. The completion list
	// and tab-cycling are driven off it so a bare `/` keeps its full candidate
	// list even as tab fills one candidate in (tab walks the whole list, then
	// wraps). It updates only while the user edits the line, never on tab-fill.
	slashPrefix string

	// telemetry is the live status strip state (issue #86); nil disables it.
	telemetry *Telemetry

	// stream is the live answer-text stream (issue #83); nil disables streaming.
	stream *Streamer
	// curStream is the index into messages of the in-progress, incrementally
	// rendering assistant message being grown by AnswerStream deltas. It is -1
	// when no assistant reply is currently streaming.
	curStream int

	// toolFeed is the live tool-call stream (issue #84); nil disables tool
	// entries.
	toolFeed *ToolFeed
	// tools is the ordered list of tool entries rendered in the transcript,
	// in stream order. Entries stay after their turn completes so the user can
	// expand a result on demand.
	tools []toolEntry
	// curToolAnchor is the index into messages of the current turn's "you"
	// message, assigned on submit so new tool calls interleave after it.
	curToolAnchor int
	// showToolResult expands all tool entries to their full result (default
	// false: collapsed); it toggles on alt+y so the user can read a full
	// output on demand while keeping the transcript clean by default (issue #84
	// AC2/AC4).
	showToolResult bool

	// review is the open changed-file review panel (issue #90), built from the
	// accumulated file-mutating tool entries. Non-nil means the panel is open
	// (ctrl+d toggles); nil means the transcript is the active surface.
	review *reviewPanel

	// rail is the right context pane (issue #88); nil disables it.
	rail *Rail
	// width is the terminal width of the last WindowSizeMsg, used to decide the
	// rail's auto-hide and to size the transcript column (issue #88 AC3). It is
	// 0 until the first resize lands.
	width int
	// height is the terminal height of the last WindowSizeMsg: the history
	// viewport clamps to it so the fixed bottom band never trails off-screen on a
	// window shrink. It is 0 until the first resize lands, in which case the
	// history renders unclamped.
	height int
	// railAuto is true until the user first presses ctrl+b: the rail then follows
	// width (auto-show wide, auto-hide narrow). After a toggle it is false and
	// railShown owns the decision explicitly, so ctrl+b works on any width.
	railAuto bool
	// railShown is the explicit rail visibility after the user toggles (issue
	// #88 AC1). Current when !railAuto.
	railShown bool

	// histFollow is true while the history viewport re-anchors to the newest
	// output (the follow position, T1 alt-screen pivot issue #119). It is the
	// default; T2 navigation (issue #120) breaks it when the user scrolls up
	// (PgUp/Home/wheel up) so reading stays put instead of being yanked to the
	// newest, and a new submit re-engages it so the fresh turn is followed.
	histFollow bool

	// histViewport is the persisted history scroll component (T1 alt-screen
	// pivot, issue #119): a bubbletea/viewport that owns the scroll region's
	// position + follow behaviour. The TUI renders into the alternate screen, so
	// every frame is a clean repaint and the viewport natively owns the history
	// clip + scroll — no primary-buffer compensation layer is needed. It owns
	// user navigation (issue #120): PgUp/PgDn/Home/End and mouse-wheel scroll
	// move it, scrolling up breaks follow (histFollow) so reading stays put, and
	// a new submit re-engages follow. It is a pointer so scroll-state changes
	// made by View (which runs on a value copy) survive across render cycles.
	histViewport *viewport.Model

	// clipboard writes the plain-text transcript to the system clipboard (issue
	// #123): Ctrl+O and /copy route here. It is the injected Dependencies
	// seam, defaulting to the atotto/clipboard package's WriteAll wrapped in
	// the OSC 52 fallback (issue #201) so a failing system-clipboard path still
	// copies through an OSC 52-capable terminal.
	clipboard func(text string) error

	// dragSel tracks an in-progress click-drag selection over the history
	// viewport (issue #124, T6): a left press inside the history region starts
	// it, motion extends it, and release copies the selected plain-text range to
	// the clipboard. Coordinates live in content space (see selection.go); nil
	// means no drag is in progress.
	dragSel *dragSelect
}

// NewModel builds a bare chat-only model (no Settings surface), the historical
// default signature. The interactive app uses NewModelCfg for Settings.
func NewModel(t Turn) Model {
	return NewModelCfg(Dependencies{Turn: t})
}

// Composer caret style policy (issue #170): the composer's hardware caret is
// deliberately a steady (non-blinking) block rather than whatever the textarea
// or terminal defaults would draw.
//
// Shape is block: it keeps the visual identity of the software reverse-video
// block caret the hardware caret replaced (issue #168) and matches the
// near-universal terminal default, so a terminal that ignores the shape request
// degrades to the same visible block — the caret is never hidden by the policy.
//
// Blink is off and fixed (not the terminal default): the caret's presence
// already signals editability — it is attached only while the composer is the
// active editing surface and hidden otherwise (issue #169) — so blinking adds
// noise without carrying information.
const (
	composerCaretShape = tea.CursorBlock
	composerCaretBlink = false
)

// NewModelCfg builds a TUI model wired to the given dependencies.
func NewModelCfg(d Dependencies) Model {
	tx := textarea.New()
	tx.Placeholder = g("Ask Eitri something…", "Ask Eitri something...")
	if !localeSupportsUTF8() {
		tx.Prompt = "| " // ASCII composer rail
	} else {
		tx.Prompt = g("┃ ", "| ")
	}
	tx.Focus()
	tx.CharLimit = 0
	tx.ShowLineNumbers = false
	// Start compact: the composer grows with the draft up to maxComposerRows
	// (issue #121 AC5), so an empty composer sits at a single row.
	tx.SetHeight(1)
	// The composer's caret is the terminal's hardware cursor (issue #168): the
	// textarea's software reverse-video caret cell is disabled so the terminal
	// itself draws the caret at the edit position.
	tx.SetVirtualCursor(false)
	// Apply the explicit caret style policy (issue #170) instead of inheriting
	// the textarea default (block + blink), and give the composer rail the
	// agent accent so the input surface reads as accent-framed (benchmark
	// §4.4: mode-colored composer border).
	th := themeFor(d.Config.Theme)
	st := tx.Styles()
	st.Cursor.Shape = composerCaretShape
	st.Cursor.Blink = composerCaretBlink
	st.Focused.Prompt = lipgloss.NewStyle().Foreground(th.accent)
	tx.SetStyles(st)

	m := Model{
		composer:        tx,
		turn:            d.Turn,
		deps:            d,
		theme:           th,
		continueReq:     make(chan struct{}, 1),
		continueResp:    make(chan bool, 1),
		skills:          skillSnapshot(d),
		telemetry:       d.Telemetry,
		stream:          d.Stream,
		curStream:       -1,
		toolFeed:        d.Tools,
		rail:            d.Rail,
		railAuto:        true,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		reasoningEffort: d.Config.ReasoningEffort,
		clipboard:       newClipboard(d),
	}
	// An unknown hand-edited theme warns once on startup via the status strip,
	// naming the fallback, instead of failing silently: the renderer still
	// falls back to dark per issue #129 (issue #131 AC1). Valid themes never
	// warn. The warning is one-shot — savedMsg renders once, then clears.
	if !isSupportedTheme(d.Config.Theme) {
		m.savedMsg = fmt.Sprintf("unknown theme %q, using %s", d.Config.Theme, config.DefaultTheme)
	}
	return m
}

// newClipboard returns the clipboard write seam (issue #123): the injected
// Dependencies.Clipboard when set, else the atotto/clipboard package default so
// Ctrl+O and /copy work out of the box. The returned seam is wrapped in the
// OSC 52 fallback (issue #201): when the primary path fails, the copy re-routes
// through the OSC 52 terminal-clipboard sequence to Dependencies.OSC52Out
// (os.Stdout by default), so a machine without xclip/wl-clipboard still copies
// through any OSC 52-capable terminal. Every copy path — Ctrl+O, /copy, and
// drag-release — routes through this single seam.
func newClipboard(d Dependencies) func(text string) error {
	var primary func(text string) error
	if d.Clipboard != nil {
		primary = d.Clipboard
	} else {
		primary = clipboard.WriteAll
	}
	out := d.OSC52Out
	if out == nil {
		out = os.Stdout
	}
	return clipboardWithOSCFallback(primary, out)
}

// newHistoryViewport builds the persisted history scroll component (T1
// alt-screen pivot, issue #119) as a heap-allocated bubbletea/viewport. It is a
// pointer so scroll-state changes made by View (which runs on a value copy)
// survive across render cycles. It starts unsized (0x0) until the first
// WindowSizeMsg lands.
//
// Mouse-wheel handling is driven by the T2 seam (issue #120), not the
// component's own Update: navigateMouse calls ScrollUp/ScrollDown directly so
// the follow position (histFollow) can break on scroll-up and re-engage on
// reaching the bottom. The wheel delta of 3 matches the component's own default
// MouseWheelDelta.
func newHistoryViewport() *viewport.Model {
	v := viewport.New()
	v.MouseWheelEnabled = false
	return &v
}

// SetTurn swaps the conversation Turn (used at boot to wire the engine seam
// after the Model is constructed).
func (m *Model) SetTurn(t Turn) { m.turn = t }

// ContinueHook returns the interactive continuation hook wired to this Model's
// prompt channels. Pass it as the engine's CanContinue so a cap hit pauses and
// asks the user.
func (m Model) ContinueHook() func() bool {
	return func() bool { return internalContinue(m.continueReq, m.continueResp) }
}

// internalContinue signals a continuation prompt and blocks for the user's y/n
// decision from the main loop.
func internalContinue(req chan struct{}, resp chan bool) bool {
	req <- struct{}{}
	return <-resp
}

// clockTickMsg re-renders the surface once per second so the statusline's
// live session-elapsed timer advances even with no input or stream activity.
// It is a telemetry refresh, not decoration, so it runs regardless of the
// reduced-motion gate (the strip's numbers change, the layout does not).
type clockTickMsg struct{}

// clockTick returns the command that delivers the next one-second clock tick.
func clockTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return clockTickMsg{} })
}

// Init returns any startup commands. None are needed; input drives everything.
// Init returns any startup commands. It schedules the live telemetry waiter so
// the status strip starts refreshing from the engine seam immediately, even
// with no keyboard input (issue #86), plus the one-second clock that keeps the
// session-elapsed timer live.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.telemetry != nil {
		cmds = append(cmds, telemetryWait(m.telemetry))
	}
	if m.stream != nil {
		cmds = append(cmds, streamWait(m.stream))
	}
	if m.toolFeed != nil {
		cmds = append(cmds, toolWait(m.toolFeed))
	}
	cmds = append(cmds, clockTick())
	return tea.Batch(cmds...)
}

// Update handles a UI event and returns the next state plus any commands.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// The band note is one-shot: it renders on the frame after it was set (the
	// initial frame for the startup theme warning, one frame after Ctrl+O for
	// the copy note), then any later message drops it (issues #131 AC1, #123).
	// The startup warning survives here because the Bubble Tea runtime renders
	// the initial View before the first Update is processed.
	m.savedMsg = ""

	// Drain a pending continuation request from the running engine goroutine:
	// this flips the Model into the prompting state so the next keypress is
	// interpreted as the y/n answer.
	select {
	case <-m.continueReq:
		m.prompting = true
	default:
	}

	switch msgi := msg.(type) {
	case telemetryUpdateMsg:
		// A live telemetry update arrived through the waiting command: fold it
		// into the strip and immediately re-issue the waiter so the strip keeps
		// refreshing live (issue #86), with no keyboard input required.
		if m.telemetry == nil {
			return m, nil
		}
		m.telemetry.apply(msgi.update)
		return m, telemetryWait(m.telemetry)

	case toolUpdateMsg:
		// A tool-call observation arrived through the waiting command: fold it
		// into the transcript's tool entries and immediately re-issue the waiter
		// so further tool calls stream in (issue #84). Updates arriving when no
		// feed is wired are dropped so they never spawn spurious entries.
		if m.toolFeed == nil {
			return m, nil
		}
		m.applyToolUpdate(msgi.update)
		return m, toolWait(m.toolFeed)

	case streamDeltaMsg:
		// A streamed delta (reasoning or answer text) arrived through the waiting
		// command: grow the in-progress assistant message's thinking or answer
		// buffer in place and immediately re-issue the waiter so the streams keep
		// coming (issues #83, #85). Deltas arriving after the turn completed (a
		// race with the final delta) are dropped so they never spawn a spurious
		// assistant message.
		if m.stream == nil || !m.busy {
			return m, nil
		}
		m.appendStreamDelta(msgi.kind, msgi.delta)
		return m, streamWait(m.stream)

	case tea.WindowSizeMsg:
		m.width = msgi.Width
		m.height = msgi.Height
		m.syncWidths()
		return m, nil

	case tea.KeyPressMsg:
		// Settings surface open: route keys to it.
		if m.settings != nil {
			return m.updateSettings(msgi)
		}
		// Interactive continuation prompt active: only y/n/ctrl+c count.
		if m.prompting {
			return m.updatePrompt(msgi)
		}
		// Review panel open: route keys to it before the composer.
		if m.review != nil {
			return m.updateReview(msgi)
		}
		// Vim normal mode (benchmark §4.4 table stakes): while active, the
		// composer accepts no text — h/j/k/l and w/b/0/$ move the caret through
		// the textarea's own movement handlers, i/a/enter/esc return to insert
		// mode. Routed before the composer switch so submit/edit keys never
		// fire from normal mode.
		if m.vimNormal {
			return m.vimKey(msgi)
		}
		switch msgi.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			// No overlay open: esc toggles vim normal mode in the composer.
			// Overlays (settings/review/prompt) close on esc before this case.
			m.vimNormal = true
			m.syncComposerRail()
			return m, nil
		case "ctrl+s":
			return m.startSettings()
		case "pgup", "home":
			// History scroll navigation (issue #120 AC2/AC3): PgUp pages up and
			// Home jumps to the oldest output. Scrolling up breaks the follow
			// position so the transcript stays put instead of being yanked to
			// the newest; navigation never steals composer focus (the composer
			// keeps arrow-key editing).
			m.navigateHistory(msgi.String())
			return m, nil
		case "pgdown", "end":
			// PgDn pages down and End jumps to the newest output. Reaching the
			// bottom re-engages the follow position (issue #120 AC2/AC3).
			m.navigateHistory(msgi.String())
			return m, nil
		case "ctrl+b":
			// The right context rail (issue #88): ctrl+b toggles it between
			// visible and hidden on any width, without stealing composer focus.
			// No-op when the rail is not wired.
			if m.rail != nil {
				m.toggleRail()
			}
			return m, nil
		case "ctrl+d":
			// The review panel (issue #90): ctrl+d toggles the changed-file
			// review over the transcript, or closes it when already open.
			if m.review != nil {
				m.review = nil
				m.syncComposerRail()
				return m, nil
			}
			rp := m.buildReview()
			m.review = &rp
			m.syncComposerRail()
			return m, nil
		case "ctrl+o":
			// Copy the full transcript to the system clipboard (issue #123 AC1),
			// reporting success/failure as a band status note. Never mutates the
			// transcript or the agent loop.
			m.copyTranscript()
			return m, nil
		case "ctrl+j", "shift+enter":
			// Shift+Enter newline (issue #121 AC2): terminals deliver Shift+Enter
			// two ways. Legacy terminals send the line-feed byte, which Bubble Tea
			// surfaces as KeyCtrlJ; terminals with the enhanced (CSI u / kitty)
			// keyboard protocol send an explicit Shift+Enter key, decoded as
			// "shift+enter". Both insert a line break instead of submitting;
			// no-op while a turn is running (ticket #57).
			if m.busy {
				return m, nil
			}
			m.composer.InsertString("\n")
			m.syncComposerHeight()
			return m, nil
		case "enter":
			if m.busy {
				return m, nil
			}
			prompt := strings.TrimSpace(m.composer.Value())
			if prompt == "" {
				return m, nil
			}
			// A new submitted turn re-engages the follow position (issue #120
			// AC3): the user is now following new output, so the viewport stops
			// holding a stale reading offset and re-anchors to the newest.
			m.histFollow = true
			m.composer.Reset()
			m.syncComposerHeight()
			m.slashIdx = 0
			m.slashPrefix = ""
			// A slash command routes to its handler instead of the engine (issue
			// #87 AC1): `/settings` opens the Settings surface; `/skillname`
			// activates that skill via the T8 run (eitri.md §2.3), surfacing the
			// result rather than sending the raw command as a chat prompt. Any
			// other `/...` line is a normal prompt and is sent to the engine seam
			// unchanged (issue #87 AC4: slash handling never swallows input).
			if prompt == "/settings" {
				return m.startSettings()
			}
			// /copy copies the full transcript to the clipboard through the same
			// seam as Ctrl+O (issue #123 AC2); the command is never sent as a
			// prompt.
			if prompt == "/copy" {
				m.copyTranscript()
				return m, nil
			}
			if name, ok := slashCommand(prompt, m.skills); ok {
				return m.activateSkill(name)
			}
			m.messages = append(m.messages, message{role: "you", content: prompt})
			m.busy = true
			m.curStream = -1
			m.syncComposerRail()
			// Anchor new tool calls to this turn's prompt so entries interleave
			// after it (issue #84).
			m.curToolAnchor = len(m.messages) - 1
			// With a live answer stream, the composer turn and the stream waiter run
			// concurrently so the reply grows in place as deltas arrive (issue #83);
			// the spinner tick rides the same batch so the busy indicator animates
			// (issue #211). The non-streaming fallback keeps the single turn
			// command — tests drive it synchronously.
			if m.stream != nil {
				return m, tea.Batch(m.turnCmd(prompt), streamWait(m.stream), spinnerTick())
			}
			return m, m.turnCmd(prompt)
		case "tab":
			// Fresh `/` with tab walks the slash completion list: the built-in
			// `/settings` command plus matching detected skills (issue #87 AC1).
			// Otherwise tab toggles the thinking stream.
			if m.composer.Value() != "" && strings.HasPrefix(m.composer.Value(), "/") && len(slashCandidates(m.composer.Value(), m.skills)) > 0 {
				m.completeSlashCommand()
				return m, nil
			}
			// Toggle the current/last turn's collapsible thinking block (auto-collapsed
			// by default; per-turn expansion, issue #85 AC2). Toggling the newest
			// assistant block lets the user watch that turn's reasoning on demand.
			if m.curStream >= 0 && m.curStream < len(m.messages) {
				// During a stream, target the in-progress assistant message.
				m.messages[m.curStream].thinkingExpanded = !m.messages[m.curStream].thinkingExpanded
			} else {
				// Otherwise expand/collapse the most recent assistant (eitri) message.
				for i := len(m.messages) - 1; i >= 0; i-- {
					if m.messages[i].role != "you" {
						m.messages[i].thinkingExpanded = !m.messages[i].thinkingExpanded
						break
					}
				}
			}
			return m, nil
		}
		// alt+y toggles expanding tool-call entries to their full result (issue
		// #84): collapsed by default so the transcript stays clean, expanded on
		// demand so nothing is ever silently truncated.
		if msgi.Mod.Contains(tea.ModAlt) && msgi.Text == "y" {
			m.showToolResult = !m.showToolResult
			return m, nil
		}
		// Let the textarea handle editing (cursor, backspace, etc.).
		nm, cmd := m.composer.Update(msg)
		m.composer = nm
		// Editing a `/...` line re-syncs the completion: the raw typed prefix is
		// remembered (so tab can walk the full list even after filling one
		// candidate) and the selection resets to the first candidate (issue #87
		// AC1). Tab-selection survives because it returns before reaching here.
		val := m.composer.Value()
		if strings.HasPrefix(val, "/") {
			m.slashPrefix = val
			m.slashIdx = 0
		}
		// The draft changed: re-grow the composer within the band (issue #121
		// AC5), so the textarea's height tracks the new line count / wraps.
		m.syncComposerHeight()
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case tea.MouseMsg:
		// History mouse-wheel scroll (issue #120 AC1) plus click-drag selection
		// (issue #124, T6): wheel up/down over the transcript scrolls it (Up
		// breaks follow, Down reaching the bottom re-engages it); a left-button
		// drag over the history highlights a cell range and release copies it to
		// the clipboard. Requires the Bubble Tea program enabled mouse events.
		// bubbletea v2 delivers mouse events as an interface: updateMouse
		// type-switches on the concrete wheel/click/motion/release messages.
		m.updateMouse(msgi)
		return m, nil

	case turnDoneMsg:
		m.busy = false
		m.spinner = 0
		m.syncComposerRail()
		wasStreaming := m.curStream >= 0 && m.curStream < len(m.messages)
		if msgi.err != nil {
			// A streaming turn aborting with an error drops the partial reply and
			// renders the error in its place; the incremental buffer is advisory.
			m.curStream = -1
			m.messages = append(m.messages, message{role: "eitri", content: failurePrefix() + msgi.err.Error()})
			return m, nil
		}
		if wasStreaming {
			// Streaming turn: reconcile the incremental buffer with the full
			// answer. When every delta already arrived the contents match, so
			// this is a no-op visual diff (no flicker, no lost selection); when
			// the last delta raced past completion, the full answer guarantees a
			// correct final render (issue #83 AC1).
			m.messages[m.curStream].content = msgi.answer
			m.messages[m.curStream].reasoning = msgi.reasoning
			m.messages[m.curStream].streaming = false
			// Auto-collapse the thinking block once the turn's final answer lands
			// (issue #85 AC3): if the user expanded it mid-reasoning to watch, it
			// settles back to the one-line hint so the styled answer takes focus.
			m.messages[m.curStream].thinkingExpanded = false
			m.curStream = -1
		} else {
			m.messages = append(m.messages, message{role: "eitri", content: msgi.answer, reasoning: msgi.reasoning})
			return m, nil
		}
		return m, nil

	case clockTickMsg:
		// One-second telemetry refresh: re-issue the tick and re-render so the
		// session-elapsed timer in the status strip stays live.
		return m, clockTick()

	case spinnerTickMsg:
		// Advance the busy spinner by one frame and re-issue the tick while the
		// turn still runs, so the indicator animates without keyboard input. A
		// tick that lands after the turn completed (or motion disabled) stops the
		// chain silently.
		if !m.busy || !motionEnabled() {
			m.spinner = 0
			return m, nil
		}
		m.spinner = (m.spinner + 1) % len(busySpinnerFrames)
		return m, spinnerTick()

	case discoverDoneMsg:
		// A provider model-discovery command finished (issue #89 AC2): fold the
		// result into the open settings panel, or drop it if the panel has since
		// closed (Save/esc while discovery was in flight).
		if m.settings == nil {
			return m, nil
		}
		m.settings.models = msgi.models
		m.settings.discoverState = discoverIdle
		if msgi.err != nil {
			m.settings.discoverErr = msgi.err.Error()
			m.settings.discoverState = discoverError
		}
		return m, nil

	case skillDoneMsg:
		m.messages = append(m.messages, message{role: "eitri", content: msgi.payload})
		return m, nil
	}

	// Everything else reaches the composer too (focus management etc.).
	nm, cmd := m.composer.Update(msg)
	m.composer = nm
	return m, cmd
}

// updatePrompt handles a keypress while a continuation prompt is pending.
func (m Model) updatePrompt(msgi tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msgi.String() {
	case "y", "Y", "enter":
		m.prompting = false
		m.continueResp <- true
	case "n", "N", "esc", "ctrl+c":
		m.prompting = false
		m.continueResp <- false
	}
	return m, nil
}

// openSettings seeds the Settings form from the loaded config + discovery,
// borrowing the live telemetry for the cache/cost readout (issue #89 AC4).
func (m *Model) openSettings() *settingsForm {
	cfg := m.deps.Config
	if cfg.Provider == "" {
		cfg = config.Default()
	}
	sf := newSettingsForm(cfg, m.deps.Models)
	sf.theme = m.theme
	sf.telemetry = m.telemetry
	m.settings = &sf
	return &sf
}

// startSettings opens the Settings surface and returns the command to run. When
// no model list was pre-seeded and an on-demand discovery seam exists, it kicks
// off provider model discovery and reports a loading state to the panel (issue
// #89 AC2); otherwise it returns nil (no command), keeping the panel already
// populated.
func (m Model) startSettings() (tea.Model, tea.Cmd) {
	sf := m.openSettings()
	// On-demand discovery only when the panel was opened with no pre-seeded
	// model list (issue #89 AC2): a seeded list is already loaded, so nothing
	// to fetch. deps.Models (not sf.models) is the seed source; sf.models holds
	// the configured-model fallback newSettingsForm installs.
	if len(m.deps.Models) != 0 || m.deps.DiscoverModels == nil {
		return m, nil
	}
	sf.discoverState = discoverLoading
	return m, discoverCmd(m.deps.DiscoverModels)
}

// updateSettings drives the Settings surface from key input.
func (m Model) updateSettings(msgi tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := m.settings
	if s == nil {
		return m, nil
	}
	switch msgi.String() {
	case "esc", "ctrl+c":
		m.settings = nil
		return m, nil
	case "tab", "enter":
		// Enter on the Save button persists and closes.
		if msgi.String() == "enter" && s.onSave() {
			s.save(&m)
			return m, nil
		}
		if s.field == fieldPaths {
			// Leaving the free-form field; re-sync the draft.
			s.cfg.ExtraWritablePaths = splitPaths(s.pathBuf)
		}
		s.next()
	case "up", "shift+up", "left":
		s.adjust(-1)
	case "down", "shift+down", "right":
		s.adjust(1)
	default:
		// Free-form editing on the paths field: type to append, backspace to
		// delete the trailing char. msgi.Text (not String) is appended so a
		// space types as " " — bubbletea v2 reports a space key's String() as
		// "space", not " " (pass 2, issue #146).
		if s.field == fieldPaths {
			if msgi.String() == "backspace" && len(s.pathBuf) > 0 {
				s.SetPathBuf(s.pathBuf[:len(s.pathBuf)-1])
			} else if msgi.String() != "backspace" {
				s.SetPathBuf(s.pathBuf + msgi.Text)
			}
		}
	}
	m.settings = s
	return m, nil
}

// save persists the Settings draft via the Save seam and closes the surface.
func (s *settingsForm) save(m *Model) {
	cfg := s.draft()
	if m.deps.Save != nil {
		if err := m.deps.Save(cfg); err == nil {
			m.savedMsg = "saved"
		} else {
			m.savedMsg = "save failed: " + err.Error()
		}
	} else {
		m.savedMsg = "view-only"
	}
	if m.deps.SaveBack != nil {
		m.deps.SaveBack(cfg)
	}
	// The theme selection re-skins the surface live (issue #179): the model's
	// chrome palette and its render config both follow the saved value, so the
	// chrome and the Markdown body pick up the new theme immediately instead
	// of waiting for the next run.
	m.deps.Config = cfg
	m.theme = themeFor(cfg.Theme)
	m.settings = nil
}

// turnCmd reports a turn's completion back to the model.
func (m Model) turnCmd(prompt string) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		res, err := m.turn(ctx, prompt)
		if err != nil {
			return turnDoneMsg{prompt: prompt, err: err}
		}
		return turnDoneMsg{prompt: prompt, answer: res.Answer, reasoning: res.Reasoning}
	})
}

// appendStreamDelta grows the in-progress assistant message by one streamed
// delta (issue #83 / #85). It returns no additional command. On the first delta
// of a turn it appends a new assistant message and records its index as the
// current stream target; subsequent deltas extend that same message in place so
// the Markdown/thinking render grows token by token. Reasoning deltas accumulate
// onto the message's reasoning buffer and the answer deltas onto its content
// buffer; the two never interleave (docs/spec.md §6).
func (m *Model) appendStreamDelta(kind StreamKind, delta string) {
	if delta == "" {
		return
	}
	if m.curStream >= 0 && m.curStream < len(m.messages) && m.messages[m.curStream].streaming {
		if kind == ReasoningStream {
			m.messages[m.curStream].reasoning += delta
		} else {
			m.messages[m.curStream].content += delta
		}
		return
	}
	if kind == ReasoningStream {
		m.messages = append(m.messages, message{role: "eitri", reasoning: delta, streaming: true})
	} else {
		m.messages = append(m.messages, message{role: "eitri", content: delta, streaming: true})
	}
	m.curStream = len(m.messages) - 1
}

// applyToolUpdate folds one tool-call observation into the transcript's tool
// entries (issue #84): a Start opens a new entry anchored to the current turn's
// prompt; the matching Result fills it in with the delivered result and the
// compression/line-delta metadata. Tool calls complete sequentially within a
// turn, so the Result pairs with the most recent incomplete entry for its tool.
func (m *Model) applyToolUpdate(u ToolUpdate) {
	if u.Start != nil {
		m.tools = append(m.tools, toolEntry{
			name:      u.Start.Name,
			args:      u.Start.Args,
			anchor:    m.curToolAnchor,
			startedAt: time.Now(),
		})
		return
	}
	if u.Result != nil {
		// Pair with the most recent not-yet-complete entry for this tool.
		for i := len(m.tools) - 1; i >= 0; i-- {
			if m.tools[i].name == u.Result.Name && !m.tools[i].complete {
				m.tools[i].result = u.Result.Result
				m.tools[i].lines = u.Result.Lines
				m.tools[i].dropped = u.Result.Dropped
				m.tools[i].compressed = u.Result.Compressed
				m.tools[i].added = u.Result.Added
				m.tools[i].removed = u.Result.Removed
				m.tools[i].before = u.Result.Before
				m.tools[i].after = u.Result.After
				m.tools[i].path = u.Result.Path
				m.tools[i].doneAt = time.Now()
				m.tools[i].complete = true
				return
			}
		}
	}
}

// skillSnapshot captures the detected skills at construction so the slash
// completion has a stable list even if the Dependencies snapshot is nil or
// empty.
func skillSnapshot(d Dependencies) []SkillItem {
	if d.Skills != nil {
		return d.Skills.Items
	}
	return nil
}

// toolEntryLabel renders the category-colored `⊕ tool` label part of the
// entry head (issue #181 AC1).
func toolEntryLabel(te toolEntry) string {
	return g("⊕ ", "+ ") + te.name
}

// toolEntryArgs renders the dimmed detail part of the entry head: the display
// args hint, the invoked line range for range-limited reads (issue #204 AC1:
// `⊕ read  path:start-end`), and the line-delta tag for file-edit tools (issue
// #84 AC3: `[+N, −M]`). Split from the label so the transcript can color the
// tool name and dim the command detail (benchmark §4.1: label + dimmed path on
// tool cards).
func toolEntryArgs(te toolEntry) string {
	s := ""
	if arg := toolArgsHint(te.args); arg != "" {
		s += "  " + arg
		if te.name == "read" {
			if r := readRangeHint(te.args); r != "" {
				s += ":" + r
			}
		}
	}
	if te.name == "edit" || te.name == "write" {
		s += fmt.Sprintf("  [+%d, −%d]", te.added, te.removed)
	}
	return s
}

// toolEntryHead renders the compact one-line `⊕ tool  args` head shared by the
// transcript entry (issue #84) and the clipboard copy (issue #123): the tool
// name and display args, plus the [+N, −M] line-delta tag for file-edit tools.
func toolEntryHead(te toolEntry) string {
	return toolEntryLabel(te) + toolEntryArgs(te)
}

// copyTranscript copies the plain-text transcript to the system clipboard
// through the injected seam (issue #123): Ctrl+O and /copy both route here. The
// outcome is surfaced as a band status note ("copied" or "copy failed: …");
// the transcript and the agent loop are never touched.
func (m *Model) copyTranscript() {
	if m.clipboard == nil {
		m.savedMsg = "copy failed: clipboard unavailable"
		return
	}
	if err := m.clipboard(m.transcriptText()); err != nil {
		m.savedMsg = "copy failed: " + err.Error()
		return
	}
	m.savedMsg = "copied"
}

// transcriptText renders the conversation log as plain text for clipboard copy
// (issue #123): role-marked user prompts and assistant answers, per-turn
// reasoning blocks, and the interleaved tool-call entries (compact one-liner
// plus full result when complete) — all ANSI-free so the pasted session is
// clean. It never mutates the transcript or the agent loop.
func (m Model) transcriptText() string {
	var b strings.Builder
	for i, msg := range m.messages {
		if msg.role != "you" && msg.reasoning != "" {
			b.WriteString("🤔 " + msg.reasoning + "\n")
		}
		if msg.role == "you" {
			b.WriteString("you: " + msg.content + "\n")
		} else {
			b.WriteString("eitri: " + msg.content + "\n")
		}
		for _, te := range m.tools {
			if te.anchor != i {
				continue
			}
			b.WriteString(toolEntryHead(te))
			b.WriteString("\n")
			if te.complete && te.result != "" {
				b.WriteString("  " + strings.ReplaceAll(strings.TrimRight(te.result, "\n"), "\n", "\n  ") + "\n")
			}
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// slashCommand reports whether prompt is a `/skillname` activation command for a
// detected skill. It returns the exact skill name and true when the whole line
// names a detected skill (leading/trailing whitespace already trimmed).
func slashCommand(prompt string, skills []SkillItem) (string, bool) {
	if len(skills) == 0 || !strings.HasPrefix(prompt, "/") {
		return "", false
	}
	name := strings.TrimPrefix(prompt, "/")
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, " \t") {
		return "", false
	}
	for _, it := range skills {
		if it.Name == name {
			return name, true
		}
	}
	return "", false
}

// activateSkill runs one slash-command activation through the SkillsSurface
// activation seam (the T8 skill tool) on a detached command and renders the
// result as an assistant note.
func (m Model) activateSkill(name string) (tea.Model, tea.Cmd) {
	m.messages = append(m.messages, message{role: "you", content: "/" + name})
	if m.deps.Skills == nil || m.deps.Skills.Activate == nil {
		m.messages = append(m.messages, message{role: "eitri", content: failurePrefix() + "no skill activation available"})
		return m, nil
	}
	return m, skillCmd(m.deps.Skills.Activate, name)
}

// discoverCmd runs one on-demand provider model discovery off the main loop and
// reports its result (issue #89 AC2). It keeps the provider seam (Models) off
// the UI goroutine so discovery latency never blocks rendering.
func discoverCmd(discover func(ctx context.Context) ([]string, error)) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		models, err := discover(ctx)
		return discoverDoneMsg{models: models, err: err}
	})
}

// skillCmd runs a skill activation off the main loop and reports its payload.
func skillCmd(activate func(ctx context.Context, name string) (string, error), name string) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		payload, err := activate(ctx, name)
		if err != nil {
			return turnDoneMsg{err: fmt.Errorf("activate skill %q: %w", name, err)}
		}
		return skillDoneMsg{payload: payload}
	})
}

// slashCandidates returns the ordered slash-command completion candidates for
// the current composer value (issue #87 AC1): the built-in `/settings` command
// first, then every detected skill whose name starts with the `/...` partial.
// A partial of "" means a bare `/`, listing every command & skill. It returns
// nil when the value does not start with `/`.
func slashCandidates(value string, skills []SkillItem) []string {
	if !strings.HasPrefix(value, "/") {
		return nil
	}
	partial := strings.TrimSpace(strings.TrimPrefix(value, "/"))
	cands := make([]string, 0, len(skills)+2)
	if partial == "" || strings.HasPrefix("settings", partial) {
		cands = append(cands, "/settings")
	}
	if partial == "" || strings.HasPrefix("copy", partial) {
		cands = append(cands, "/copy")
	}
	for _, it := range skills {
		// The built-in /settings command owns the `settings` name; a skill of
		// the same name is not separately completable to avoid a duplicate row.
		if it.Name == "settings" {
			continue
		}
		if strings.HasPrefix(it.Name, partial) {
			cands = append(cands, "/"+it.Name)
		}
	}
	return cands
}

// completeSlashCommand fills the composer with the next slash-command completion
// candidate, cycling deterministicly through the built-in `/settings` command
// and matching detected skills (issue #87 AC1). The candidate list is driven off
// slashPrefix (the raw prefix the user typed, captured on edit), so repeated
// tabs walk the whole list even after one candidate is filled in — the fill
// never narrows the list of remaining options. The selection (slashIdx) moves
// forward one candidate per press, wrapping around to the start.
func (m *Model) completeSlashCommand() {
	cands := slashCandidates(m.slashPrefix, m.skills)
	if len(cands) == 0 {
		return
	}
	if m.slashIdx < 0 || m.slashIdx >= len(cands) {
		m.slashIdx = 0
	}
	m.composer.SetValue(cands[m.slashIdx])
	m.composer.SetCursorColumn(len(cands[m.slashIdx]))
	m.syncComposerHeight()
	// Advance for the next press so repeated tabs walk the whole list.
	m.slashIdx = (m.slashIdx + 1) % len(cands)
}

// View renders the conversation plus composer as a tea.View (bubbletea v2).
// The view declares the alternate screen and mouse-cell-motion mode as fields
// (pass 1, issue #145): every frame is a clean full-surface repaint into the
// alt buffer, so a resize never duplicates or scatters text — the settings that
// v1 pushed through program options (tea.WithAltScreen,
// tea.WithMouseCellMotion) live here declaratively.
func (m Model) View() tea.View {
	content := m.viewString()
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	// The composer's caret is the terminal's hardware cursor (issue #168): the
	// textarea's software caret cell is disabled, so the frame attaches the
	// caret at the composer's true edit cell instead.
	v.Cursor = m.composerCursor(content)
	return v
}

// viewString renders the surface content string (the tea.View content). It
// renders committed messages and the composer as a full-frame repaint; the
// alternate-screen renderer clears stale surface state each frame, so a resize
// never duplicates or scatters text. The Settings surface and the continuation
// prompt are rendered on top when active.
func (m Model) viewString() string {
	if m.settings != nil {
		return settingsView(*m.settings)
	}
	if m.prompting {
		return promptView(m.theme)
	}

	// The right context rail (issue #88, Layout A): when visible, the rendered
	// transcript pane and the state rail sit side by side — one pane for time
	// (transcript), one for state (rail). The rail never steals width from the
	// full-width except where it auto-shows wide (railVisible gates it, and the
	// composer width already shrank so the transcript re-wraps to the freed
	// space).
	left := m.renderPane()
	if m.rail != nil && m.railVisible() {
		right := styledRail(m.rail.render(m.telemetry, m.theme), m.railClampHeight())
		return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}
	return left
}

// railClampHeight returns the maximum number of rows the right context rail may
// occupy so it matches the history region's visible height (issue T05 AC1):
// both panes clamp to the rows left over by the fixed bottom band, so the two
// form one coherent row. It is -1 before the first resize lands, leaving the
// rail unclamped — mirroring renderHistoryViewport; a non-negative result is
// the actual row budget (0 when the band fills the whole terminal, in which
// case the rail renders nothing).
func (m Model) railClampHeight() int {
	if m.height <= 0 {
		return -1
	}
	// The rail shares the history viewport's vertical budget: terminal height
	// minus whatever the fixed bottom band occupies.
	vh := m.height - m.bandHeight()
	if vh < 0 {
		return 0
	}
	return vh
}

// maxComposerRows is how tall the composer may grow inside the fixed bottom
// band before it scrolls internally (issue #121 AC5): a long draft never
// spills into the transcript — the textarea's own viewport scrolls past this
// bound, and the band stays pinned while the history viewport yields rows.
const maxComposerRows = 8

// syncComposerHeight grows the composer with its draft up to maxComposerRows,
// then lets the textarea scroll internally (issue #121 AC5): a one-line draft
// keeps a compact single-row composer, each new line adds a row up to the
// bound, and beyond it the composer's internal viewport scrolls so the band
// never grows past the bound. It also clamps to the terminal height when a
// resize has landed so the band can never push the composer off-screen. It is
// re-run on every composer edit, on submit-reset, and on resize (soft wraps
// depend on the width).
func (m *Model) syncComposerHeight() {
	rows := composerContentRows(m.composer)
	if rows > maxComposerRows {
		rows = maxComposerRows
	}
	if m.height > 0 {
		// The band also holds the status strip and slash completion above the
		// composer; leave at least one row for them.
		if lim := m.height - 1; rows > lim {
			rows = lim
		}
	}
	if rows < 1 {
		rows = 1
	}
	m.composer.SetHeight(rows)
}

// composerContentRows estimates how many terminal rows the composer's current
// value occupies once word-wrapped at the composer width: one row per hard
// newline plus soft-wrap continuations, floored at one. It wraps at the same
// width the textarea itself wraps at, so the grown height tracks what the user
// sees; an off-by-one on a wrap boundary only trades a little internal scroll
// room (issue #121 AC5).
func composerContentRows(c textarea.Model) int {
	w := c.Width()
	if w < 1 {
		w = 1
	}
	rows := 0
	for _, line := range strings.Split(c.Value(), "\n") {
		width := lipgloss.Width(line)
		if width <= 0 {
			rows++
			continue
		}
		rows += (width + w - 1) / w
	}
	if rows < 1 {
		rows = 1
	}
	return rows
}

// bandHeight returns how many terminal rows the fixed bottom band (status
// strip, slash completion, composer) occupies, so the scroll region and the
// right rail can clamp to the rows it leaves behind.
func (m Model) bandHeight() int {
	var band strings.Builder
	m.renderBand(&band)
	return lineCount(band.String())
}

// renderPane renders the transcript + composer surface into the left pane. It
// is the single-pane view; the rail adds itself to the right when visible. It
// is used by the alternate-screen renderer, so every frame is a clean repaint.
//
// Render is split into explicit, ordered regions (issue T01): the review
// overlay region (when open) on top, the scroll region (history), then the
// fixed bottom band (status strip + composer). Each region renders independently
// into its own builder; renderPane just concatenates them in order. The scroll
// region is Height-aware: its content clamps to the terminal height, so the band
// stays pinned and only the history scrolls.

func (m Model) renderPane() string {
	var band strings.Builder
	m.renderBand(&band)
	bandStr := band.String()

	// Overlay region: the review panel takes over the top of the pane (Layout B,
	// issue #90), showing the dense changed-file summary + inline diff above the
	// transcript. It is its own height-clipped region (issue T06): a tall
	// expanded diff clips instead of overflowing the terminal, so the fixed
	// bottom band stays pinned. Settings and the continuation prompt are also
	// overlays but return earlier from View() as their own full-surface regions.
	var review strings.Builder
	reviewStr := ""
	reviewLines := 0
	if m.review != nil {
		m.renderReview(&review)
		reviewStr = review.String()
		reviewLines = m.reviewRegionRows(reviewStr, lineCount(bandStr))
	}

	// The scroll region renders through the native bubbletea/viewport component
	// (T1 alt-screen pivot, issue #119), which owns the history clip + follow; no
	// width-bucket cache or manual clip/anchor compensation layer is needed.
	var hist strings.Builder
	m.renderHistory(&hist, nil)
	if m.review != nil {
		// The review region is its own height-clipped overlay (issue T06).
		reviewStr = clipReviewRegion(reviewStr, reviewLines)
	}
	histRegion := m.renderHistoryViewport(hist.String(), lineCount(bandStr)+reviewLines)
	// The scroll region must end on its own row before the band joins: the
	// persisted viewport renders its rows newline-joined with no trailing
	// newline (and pads to the scroll height), so without this terminator the
	// band separator fuses onto the viewport's last padded row — doubling that
	// row's width and shoving the separator (and the rail, when visible) past
	// the terminal's right edge.
	if histRegion != "" && !strings.HasSuffix(histRegion, "\n") {
		histRegion += "\n"
	}
	return reviewStr + histRegion + bandStr
}

// renderHistoryViewport returns the Height-clamped scroll region: the rendered
// history content limited to the rows the
// non-reserved regions (the fixed bottom band, plus the review overlay when
// open — issue T06) do not occupy, so the band stays pinned at the very bottom
// and only the history clips. Until the first resize lands (m.height == 0) the
// history renders unclamped — the pre-session default, kept for lean embeds and
// tests that never size.
//
// The clip is served through the persisted bubbletea/viewport scroll component
// (T1 alt-screen pivot, issue #119): the viewport natively owns the scroll
// position + clip, re-anchoring to the newest output (GotoBottom) while the
// follow position is engaged so the newest content stays in view through
// streamed appends and a mid-stream resize (issue #108 AC1/AC2). T2 (issue
// #120) adds user navigation: PgUp/PgDn/Home/End and mouse-wheel scroll move
// it, and scrolling up breaks follow (histFollow) so the viewport holds a
// reading offset across re-renders until a new submit re-engages it.
func (m Model) renderHistoryViewport(content string, reserved int) string {
	if m.height <= 0 {
		// No size has landed yet: the unclamped render, kept for lean embeds and
		// tests that never size.
		return content
	}
	vh := m.height - reserved
	if vh <= 0 {
		// The non-scroll regions occupy the whole terminal; no room for history.
		return ""
	}
	vp := m.histViewport
	if vp == nil {
		// Without the persisted component (should not occur via NewModelCfg) the
		// history clips to the viewport's bottom-anchored range.
		return bottomSlice(content, vh)
	}
	// Re-hydrate the persisted viewport with the fresh content + dimensions so
	// it owns the scroll position relative to the current content, then follow
	// the newest output unless the user has broken follow by scrolling up (T2,
	// issue #120 AC3): while follow is engaged the newest content stays in view
	// through streamed appends and a mid-stream resize; while broken (reading an
	// earlier offset) the viewport holds that position across re-renders so
	// reading stays put until a new submit re-engages follow. SetContent clamps
	// an out-of-range offset so a content shrink never leaves a stale offset.
	vp.SetWidth(m.transcriptWidth())
	vp.SetHeight(vh)
	// An in-progress drag selection highlights its cell range in the full
	// content before the viewport clips it to the visible window (issue #124
	// AC1): the reverse-video markers render only where the range is on screen.
	if m.dragSel != nil {
		content = m.highlightSelection(content)
	}
	vp.SetContent(content)
	if m.histFollow {
		vp.GotoBottom()
	}
	return vp.View()
}

// navigateHistory applies a T2 (issue #120) keyboard scroll command to the
// persisted history viewport: PgUp/Home move toward the older output and break
// the follow position; PgDn/End move toward the newest and re-engage follow
// when they reach the bottom. It never touches the composer, so editing focus
// is preserved (AC4). The viewport holds its scroll state across renders even
// while the history re-renders each frame.
func (m *Model) navigateHistory(key string) {
	vp := m.histViewport
	if vp == nil {
		return
	}
	switch key {
	case "pgup":
		if vp.AtTop() {
			return // already at the oldest output; nothing to do
		}
		vp.PageUp()
		m.histFollow = false // scrolling up breaks follow
	case "home":
		if vp.AtTop() {
			return
		}
		vp.GotoTop()
		m.histFollow = false
	case "pgdown":
		vp.PageDown()
		if vp.AtBottom() {
			m.histFollow = true // paging to the newest re-engages follow
		}
	case "end":
		vp.GotoBottom()
		m.histFollow = true
	}
}

// navigateMouse applies a T2 (issue #120 AC1) mouse-wheel scroll to the
// persisted history viewport: wheel up scrolls toward older output and breaks
// follow; wheel down scrolls toward the newest and re-engages follow once it
// reaches the bottom. It never touches the composer, preserving input focus.
// Bubble Tea delivers mouse events only when the program enables them
// (internal/app/tui.go).
func (m *Model) navigateMouse(msg tea.MouseWheelMsg) {
	vp := m.histViewport
	if vp == nil {
		return
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		if vp.AtTop() {
			return
		}
		vp.ScrollUp(3)
		m.histFollow = false
	case tea.MouseWheelDown:
		vp.ScrollDown(3)
		if vp.AtBottom() {
			m.histFollow = true
		}
	}
}

// reviewRegionRows returns how many rows the review overlay region may occupy
// when open (issue T06 AC1): at most reviewRegionMax rows, and never more than
// the terminal leaves after the fixed bottom band, so a tall expanded diff clips
// instead of pushing the band (composer) off-screen while leaving the header +
// file list readable. content is the fully rendered review; bandLines is the
// fixed band's row count (already line-counted by the caller).
func (m Model) reviewRegionRows(content string, bandLines int) int {
	rrows := lineCount(content)
	capRows := reviewRegionMax
	if m.height > 0 {
		avail := m.height - bandLines
		if avail < capRows {
			capRows = avail
		}
		if capRows < 0 {
			capRows = 0
		}
	}
	if rrows > capRows {
		return capRows
	}
	return rrows
}

// clipReviewRegion keeps the first n rows of the rendered review region and
// discards the tail, so an over-height diff clips at the review region boundary
// (issue T06 AC1) instead of flowing over the history/band. A trailing newline
// is preserved so the region stays cleanly separated from the scroll region.
func clipReviewRegion(content string, n int) string {
	if n < 0 {
		n = 0
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if n < len(lines) {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n") + "\n"
}

// bottomSlice returns the bottom-anchored slice of the history content for a
// viewport of the given height — the fallback used when the model has no
// persisted viewport component (should not occur via NewModelCfg). It keeps the
// newest lines, dropping the head when the history overflows the viewport.
func bottomSlice(content string, vh int) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) <= vh {
		return content
	}
	if vh < 0 {
		vh = 0
	}
	return strings.Join(lines[len(lines)-vh:], "\n")
}

// lineCount reports how many rendered terminal rows a region string occupies,
// i.e. the number of newline-separated lines (a trailing newline does not add
// an extra row). It is used to compute how much of the terminal height the
// fixed bottom band consumes so the history viewport can clamp to the rest.
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if strings.HasSuffix(s, "\n") {
		n--
	}
	return n + 1
}

// toolRowRange maps a rendered history row span to the tool entry that owns
// it, so a mouse click on a collapsed tool head can toggle that entry's
// expansion (click-to-expand, benchmark §4.4). start/end are content-line
// indexes in the viewport's split space (the same space mouseToContent maps
// into); idx indexes m.tools.
type toolRowRange struct {
	start, end, idx int
}

// renderHistory renders the scroll region: the agent history that the user
// reads and scrolls. It surfaces the workspace header, every committed message
// (thinking blocks + markdown body), the interleaved tool entries, and the
// busy indicator. It is the only region T02+ makes scrollable and height-
// clamps. Detected skills surface through the slash-command completion list,
// never as a panel in the transcript (issue #188).
//
// toolRows, when non-nil, receives the row span of every tool entry written,
// in content-line coordinates, so click handling can map a pointer to the tool
// under it without re-deriving the layout. Every block ends on a newline, so
// the newline count before a write equals the content row index where it
// starts.
func (m Model) renderHistory(b *strings.Builder, toolRows *[]toolRowRange) {
	if toolRows != nil {
		*toolRows = (*toolRows)[:0]
	}
	nl := 0
	emit := func(s string) {
		b.WriteString(s)
		nl += strings.Count(s, "\n")
	}
	// Surface the project's read-only state (issue #82 AC1): the workspace
	// directory the run operates in, rendered as an informational header above
	// the transcript and never inside the composer the user types into.
	if m.deps.WorkspacePath != "" {
		emit(m.theme.statusStyle.Render("workspace: " + m.deps.WorkspacePath))
		emit("\n")
	}
	// The empty-transcript welcome: a one-line brand mark plus a hint, so a
	// fresh session reads as a designed surface instead of a blank scroll
	// region (first-run discoverability). It disappears on the first turn
	// (submit appends the "you" message before busy flips).
	if len(m.messages) == 0 && !m.busy {
		emit(idleWelcome(m.theme))
	}
	for i, msg := range m.messages {
		// Reasoning renders as a distinct, collapsible per-turn block — never
		// merged into the answer. Collapsed it is a one-line hint carrying the
		// token estimate + effort; `tab` expands just that turn's block (issue
		// #85, docs/spec.md §6).
		if msg.role != "you" && msg.reasoning != "" {
			emit(thinkingHeader(m.theme, msg.reasoning, m.reasoningEffort))
			if msg.thinkingExpanded {
				emit(msg.reasoning + "\n")
			}
		}
		w := m.composer.Width()
		if msg.role == "you" {
			// User prompts render as a carded bubble: the theme's near-background
			// tint with breathing padding fills the pane width, so the user side
			// reads as a card against the bare agent panes (benchmark §4.1). The
			// markdown wraps inside the padding at pane width minus the 2-col
			// gutter; the fill always spans the pane, never hugging ragged lines.
			md, _ := RenderMarkdown(msg.content, w-4, m.deps.Config.Theme)
			bubble := m.theme.userBubbleStyle.Width(w).Render(strings.TrimRight(md, "\n"))
			emit(bubble + "\n")
		} else {
			// Left-bordered agent pane (issue #122 AC1): the answer renders in
			// a bordered pane; a failing turn (⚠) gets the error-colored border
			// so errors are as readable as answers (issue #122 AC2).
			md, _ := RenderMarkdown(msg.content, w-2, m.deps.Config.Theme)
			pane := m.theme.agentPaneStyle
			if strings.HasPrefix(msg.content, failurePrefix()) {
				pane = m.theme.errorPaneStyle
			}
			// The pane border char follows the glyph charter at render time: the
			// default theme's styles are built at package init, before any ASCII
			// override is known, so the char is re-applied per frame.
			pane = pane.Border(lipgloss.Border{Left: g("│", "|")})
			// Trim glamour's trailing blank line so the pane ends on its last
			// content row instead of a lone border column.
			emit(fmt.Sprintf("%s\n", pane.Render(strings.TrimRight(md, "\n"))))
		}
		// Interleave the turn's tool-call entries right after its prompting "you"
		// message (issue #84): compact one-liners, collapsed by default, expanded
		// on demand to the full result (per-entry via mouse click, globally via
		// alt+y).
		for ti, te := range m.tools {
			if te.anchor == i {
				// While a turn runs the live elapsed ticks (the busy spinner drives
				// the re-render); idle frames pass a zero time so completed tools
				// freeze their span.
				now := time.Time{}
				if m.busy {
					now = time.Now()
				}
				start := nl
				s := renderToolEntry(m.theme, te, m.showToolResult || te.expanded, now, w)
				rows := strings.Count(s, "\n")
				emit(s)
				if toolRows != nil && rows > 0 {
					*toolRows = append(*toolRows, toolRowRange{start: start, end: start + rows - 1, idx: ti})
				}
			}
		}
	}
	// The busy indicator normally lives in the always-visible status strip
	// (renderBand), so the working state never scrolls away with the history
	// (benchmark §4.3: spinner + working label in the statusline). Lean embeds
	// and tests without a telemetry strip keep the history footer row instead.
	if m.busy && m.telemetry == nil {
		emit(m.theme.statusStyle.Render(busyLine(m.spinner)))
		emit("\n")
	}
}

// toolEntryAtLine returns the tool entry whose rendered rows include the given
// content line, and whether that entry is currently collapsed (a click on a
// collapsed head toggles it open; on an open entry it toggles closed). The
// lookup re-renders the history with row accounting — the same split space the
// viewport and mouse coordinates use — so it never drifts from what the user
// sees.
func (m Model) toolEntryAtLine(line int) (idx int, collapsed bool, ok bool) {
	var hist strings.Builder
	var rows []toolRowRange
	m.renderHistory(&hist, &rows)
	for _, r := range rows {
		if line >= r.start && line <= r.end {
			if r.idx < len(m.tools) {
				return r.idx, !m.tools[r.idx].expanded, true
			}
			return 0, false, false
		}
	}
	return 0, false, false
}

// toggleToolEntry flips one tool entry's expansion state (mouse click
// click-to-expand). It never touches other entries or the global alt+y flag.
func (m *Model) toggleToolEntry(idx int) {
	if idx < 0 || idx >= len(m.tools) {
		return
	}
	m.tools[idx].expanded = !m.tools[idx].expanded
}

// vimKey routes a keypress while the composer is in vim normal mode: motion
// keys map onto the textarea's own movement handlers (so caret geometry stays
// consistent), i/a/enter/esc return to insert mode without inserting or
// submitting, and every other printable key is ignored — normal mode never
// types. ctrl+c still quits.
func (m Model) vimKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var mapped *tea.KeyPressMsg
	switch msg.String() {
	case "h":
		mapped = &tea.KeyPressMsg{Code: tea.KeyLeft}
	case "l":
		mapped = &tea.KeyPressMsg{Code: tea.KeyRight}
	case "j":
		mapped = &tea.KeyPressMsg{Code: tea.KeyDown}
	case "k":
		mapped = &tea.KeyPressMsg{Code: tea.KeyUp}
	case "w":
		mapped = &tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt} // word forward
	case "b":
		mapped = &tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt} // word backward
	case "0":
		mapped = &tea.KeyPressMsg{Code: tea.KeyHome}
	case "$":
		mapped = &tea.KeyPressMsg{Code: tea.KeyEnd}
	case "i", "a", "enter", "esc":
		// Return to insert mode: the key is consumed (never inserted, never
		// submitted) so the draft is untouched by the mode switch.
		m.vimNormal = false
		m.syncComposerRail()
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	default:
		return m, nil // normal mode ignores everything else
	}
	nm, cmd := m.composer.Update(*mapped)
	m.composer = nm
	return m, cmd
}

// syncComposerRail recolors the composer's prompt rail by editing state: the
// accent rail signals an editable composer, while a running turn or an open
// review panel makes the composer inert, so the rail dims to a muted accent
// (state-as-color — the mode-colored composer border pattern, benchmark
// §4.3/§4.4). The rail's glyph and width never change, so the caret geometry
// is untouched.
func (m *Model) syncComposerRail() {
	c := m.theme.accent
	switch {
	case m.vimNormal:
		// Vim normal mode: the rail sits between accent (insert) and the busy
		// dim — a distinct, calmer shade signals "navigating, not typing".
		c = dimmed(m.theme.accent, 0.6)
	case m.busy || m.review != nil:
		c = dimmed(m.theme.accent, 0.45)
	}
	st := m.composer.Styles()
	st.Focused.Prompt = lipgloss.NewStyle().Foreground(c)
	m.composer.SetStyles(st)
}

// idleWelcome renders the empty-transcript welcome block (issue #212): the
// brand mark in the accent hue plus faint capability + keybinding hints, so
// the first launch reads as a designed surface. One accent, no decoration —
// the restrained brand treatment, not a logo wall.
func idleWelcome(th Theme) string {
	return th.headerStyle.Render("Eitri") + th.statusStyle.Render(g(" — ", " - ")+"your terminal coding agent") + "\n" +
		th.statusStyle.Render("  ask me to fix a bug, refactor code, explain a system, or run the tests") + "\n" +
		th.statusStyle.Render("  ctrl+s settings · ctrl+b rail · / for commands") + "\n"
}

// bandHints returns the right-aligned keybinding hint strip for the status
// row (benchmark §4.4: one consistent hint system from the central keymap).
// Hints are the real, wired bindings — never advertised keys that no-op.
func bandHints(m Model) string {
	if m.vimNormal {
		return strings.Join([]string{"h j k l move", "w b word", "0 $ line", "i insert", "esc exit"}, g(" · ", " . "))
	}
	hints := []string{"ctrl+s settings", "ctrl+b rail", "ctrl+d review", "ctrl+o copy"}
	if m.review != nil {
		hints = []string{"enter diff", "o browser", "ctrl+d close"}
	}
	return strings.Join(hints, g(" · ", " . "))
}

// renderBand renders the fixed bottom band: the live status strip (when wired)
// plus the slash-command completion list and the composer, in that order. This
// is the region T02+ pins at the bottom so it never scrolls away on resize.
func (m Model) renderBand(b *strings.Builder) {
	var inner strings.Builder
	// Live status strip (issues #86, #182), rendered above the composer so
	// model, effort, thinking, turns/max, cost, and the cache gauge stay
	// glanceable; it carries the accent hue to match the colorized rail (issue
	// #182 AC4). On wide enough terminals the keybinding hints right-align onto
	// the same row and the strip uses its compact telemetry form (the model/
	// effort/thinking details already live in the right rail), so the row never
	// grows and the band height is unchanged; narrow windows drop the hints and
	// keep the full strip.
	w := m.transcriptWidth()
	if m.telemetry != nil {
		stripW := w
		hints := ""
		if w >= 100 {
			hints = m.theme.statusStyle.Render(bandHints(m))
			stripW = collapseWidth - 1 // compact telemetry makes room for hints
		}
		strip := m.theme.bandStatusStyle.Render(m.telemetry.render(stripW))
		// While a turn runs the spinner also rides the always-visible strip, so
		// the working state stays glanceable even when the history is scrolled
		// away from the busy footer row (the spinner tick drives the re-render).
		if m.busy {
			strip = m.theme.bandStatusStyle.Render(busyLine(m.spinner)) + "  " + strip
		}
		if hints != "" {
			pad := w - lipgloss.Width(strip) - lipgloss.Width(hints)
			// Only right-align the hints when there's room; a narrow transcript keeps the
			// strip alone rather than clipping the hints at the edge.
			if pad > 2 {
				strip += strings.Repeat(" ", pad) + hints
			}
		}
		inner.WriteString(strip)
		inner.WriteString("\n")
	}
	// The slash-command completion list (issue #87 AC1) sits above the composer
	// whenever the input line is a `/...` command, listing the built-in
	// /settings command + matching skills with the current selection marked.
	renderSlashCompletion(&inner, m.theme, m.slashPrefix, m.composer.Value(), m.skills, m.slashIdx)
	inner.WriteString(m.composer.View())
	if m.savedMsg != "" {
		inner.WriteString("\n" + m.theme.statusStyle.Render(m.savedMsg))
	}
	// The whole band is framed by an accent separator row so it reads as one
	// coherent fixed region under the transcript (issue #122 AC3). The
	// separator spans the transcript column (the rail joins to the right).
	tw := m.transcriptWidth()
	if tw < 2 {
		tw = 2
	}
	b.WriteString(m.theme.bandSeparatorStyle.Render(strings.Repeat(g("─", "-"), tw)))
	b.WriteString("\n")
	b.WriteString(inner.String())
}

// composerCursor returns the composer's hardware caret for the current frame,
// or nil when the composer is not the active editing surface (issues #168, #169). The
// textarea reports its caret relative to its own top-left cell; the band is
// pinned to the bottom of the frame, so the caret is offset by the rows that
// render above the composer — everything above the band, plus the band's own
// pre-composer rows (separator, status strip, slash completion). content is the
// frame's rendered content, whose line count is the frame height.
func (m Model) composerCursor(content string) *tea.Cursor {
	if m.settings != nil || m.prompting || m.review != nil || m.busy {
		// The composer is not the active editing surface, so no caret (issue #169):
		// Settings and the continuation prompt are full-surface overlays (no
		// composer on screen), the review panel routes keys away from the
		// composer, and while a turn is running the composer is inert (keys are
		// ignored, ticket #57). The caret returns on the next frame once the
		// composer is editable again.
		return nil
	}
	cur := m.composer.Cursor()
	if cur == nil {
		return nil
	}
	var band strings.Builder
	m.renderBand(&band)
	pre := m.composerPreRows()
	// The left pane starts at column 0, so the textarea's own X offset (prompt
	// + internal padding) is already frame-absolute; only the row needs the
	// band offset.
	cur.Y += lineCount(content) - lineCount(band.String()) + pre
	return cur
}

// composerPreRows returns how many band rows render above the composer: the
// accent separator, the live status strip (when wired), and one row per
// slash-completion candidate (issue #168). It mirrors renderBand's ordering so
// the caret lands on the composer's true frame row; savedMsg rows sit below
// the composer and are excluded.
func (m Model) composerPreRows() int {
	n := 1 // accent separator
	if m.telemetry != nil {
		n++
	}
	return n + len(slashCandidates(m.slashPrefix, m.skills))
}

// promptView renders the interactive max-turns continuation prompt.
func promptView(th Theme) string {
	// The max-turns continuation decision, framed like the other overlays: an
	// accent title, the question, and the honest y/n/esc bindings from
	// updatePrompt.
	return th.headerStyle.Render("run paused at the max-turns cap") + "\n\n" +
		"  Continue the run with more turns?\n" +
		"  " + th.statusStyle.Render("y") + " continue" + g(" · ", " . ") + th.statusStyle.Render("n") + " stop" + g(" · ", " . ") + th.statusStyle.Render("esc") + " cancel\n"
}

// thinkingHeader renders a turn's collapsible reasoning block header. Collapsed
// it is a one-line hint carrying a token estimate and the reasoning-effort tier
// (issue #85 AC2: "🤔 1.4k tok · medium"); the block renders distinctly from the
// answer so reasoning is recognizable but secondary, and settles back to this
// hint when the turn's answer lands. reasoning is the accumulated thinking text;
// effort is the run's reasoning-effort tier (empty drops the suffix).
func thinkingHeader(th Theme, reasoning, effort string) string {
	hint := fmt.Sprintf("%s %s tok", g("🤔", "?"), formatTokens(tokenEstimate(reasoning)))
	if effort != "" {
		hint += g(" · ", " . ") + effort
	}
	return th.thinkingStyle.Render(hint) + "\n"
}

// tokenEstimate estimates a reasoning stream's token count from its assembled
// text length, using the conventional ~4 chars/token yardstick. It backs the
// collapsed thinking hint's token readout so the user can gauge the turn's
// reasoning cost at a glance (issue #85 AC2).
func tokenEstimate(s string) int {
	return len([]rune(s)) / 4
}

// renderSlashCompletion appends the slash-command completion list to the view
// above the composer (issue #87 AC1): the built-in `/settings` command plus any
// matching detected skills. It marks the candidate currently in the composer
// (tab-filled or typed) as selected; on a bare prefix it points at the next
// tab-cycling candidate (slashIdx) as a forward hint. It renders nothing for a
// non-slash line or when there are no candidates, so normal typing is
// unaffected (issue #87 AC4).
func renderSlashCompletion(b *strings.Builder, th Theme, value string, cur string, skills []SkillItem, selected int) {
	cands := slashCandidates(value, skills)
	if len(cands) == 0 {
		return
	}
	// Highlight the candidate currently in the composer (tab-filled or typed);
	// when the line is still a bare prefix, fall back to the tab selection
	// (slashIdx) as the forward hint.
	sel := selected
	for i, c := range cands {
		if c == cur {
			sel = i
			break
		}
	}
	if sel < 0 || sel >= len(cands) {
		sel = 0
	}
	for i, c := range cands {
		if i == sel {
			// Selected candidate is highlighted with the agent accent so the
			// completion list reads as part of the coherent band (issue #122
			// AC3).
			b.WriteString(th.slashSelectStyle.Render(g("▸ ", "> ") + c))
		} else {
			b.WriteString(th.statusStyle.Render("  " + c))
		}
		b.WriteString("\n")
	}
}

// renderToolEntry renders one tool-call entry as a compact, glanceable line —
// `⊕ tool  args` — with the result collapsed by default to a summary, never a
// raw dump into the scroll (issue #84). A file-mutating edit carries a [+N,-M]
// line-delta tag, and a compressed result carries an explicit "+N more" tail
// marker. When expanded (showToolResult), the full inline result is rendered so
// nothing is silently truncated — every collapse has an expand path.
func renderToolEntry(th Theme, te toolEntry, expanded bool, now time.Time, width int) string {
	var b strings.Builder
	// The ⊕ tool glyph is constant; a delivered result tags the entry with a
	// ✓/✗ outcome marker (issue #122 AC2) so success and failure are
	// glanceable without expanding the collapsed summary. The entry line
	// itself renders in the tool's category hue (shell/file/web/skill, issue
	// #181 AC1), with the glyph + color pair keeping meaning from ever
	// depending on color alone (issue #181 AC5).
	outcome := ""
	if te.complete {
		if isToolFailure(te.result) {
			outcome = " " + th.outcomeErrStyle.Render(g("✗", "X"))
		} else {
			outcome = " " + th.outcomeOKStyle.Render(g("✓", "ok"))
		}
	}
	// The entry head splits into the category-colored ⊕ tool label and the
	// dimmed command detail (args/range/delta): color marks the tool kind, the
	// detail recedes so a busy session reads calmly (benchmark §4.1 tool-cards:
	// label + dimmed path). Long details truncate to the pane width with an
	// ellipsis so a huge URL or command never cuts abruptly at the edge; the
	// full arguments stay in the clipboard copy and the expanded result.
	label := toolEntryLabel(te)
	args := toolEntryArgs(te)
	budget := width - lipgloss.Width(label) - 8 // room for the outcome + timer
	if budget > 1 && lipgloss.Width(args) > budget {
		args = truncateWidth(args, budget-1) + g("…", "...")
	}
	head := th.toolCategoryStyle(toolCategoryOf(te.name)).Render(label)
	if args != "" {
		head += th.statusStyle.Render(args)
	}
	b.WriteString(head + outcome)
	// Elapsed-time readout on the entry head (benchmark §4.1): sub-second tools
	// stay silent — only a tool worth waiting on earns a timer. Completed tools
	// freeze the span; a running tool (non-zero now, e.g. while the busy
	// spinner ticks) shows the live elapsed.
	if !te.startedAt.IsZero() {
		var d time.Duration
		if te.complete && !te.doneAt.IsZero() {
			d = te.doneAt.Sub(te.startedAt)
		} else if !now.IsZero() {
			d = now.Sub(te.startedAt)
		}
		if d >= time.Second {
			b.WriteString(" " + th.statusStyle.Render(formatElapsed(d)))
		}
	}
	b.WriteString("\n")

	if !expanded {
		// Collapsed summary: line count + explicit "+N more" tail marker when
		// the result was compressed (docs/spec.md §5). Never a raw dump.
		if te.lines > 0 || te.dropped > 0 {
			summary := fmt.Sprintf("%d line%s", te.lines, plural(te.lines))
			if te.compressed && te.dropped > 0 {
				summary += fmt.Sprintf(" (+%d more)", te.dropped)
			}
			b.WriteString(th.statusStyle.Render("  " + summary))
			b.WriteString("\n")
		}
		return b.String()
	}

	// Expanded: the full result framed as a card — a left border in the
	// entry's category hue with the content plain, so an expanded tool reads
	// as one designed block instead of a raw text dump (benchmark §4.1: tool
	// cards; the border color repeats the label's category color).
	if te.result != "" {
		frame := lipgloss.NewStyle().
			Border(lipgloss.Border{Left: g("│", "|")}).
			BorderLeft(true).
			PaddingLeft(1).
			BorderForeground(th.toolCategoryStyle(toolCategoryOf(te.name)).GetForeground())
		b.WriteString(frame.Render(strings.TrimSuffix(te.result, "\n")))
		b.WriteString("\n")
	}
	return b.String()
}

// formatElapsed renders a duration in the tool-timer vocabulary (Codex-style):
// seconds under a minute, minutes+seconds under an hour, hours+minutes beyond.
func formatElapsed(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	if m < 60 {
		return fmt.Sprintf("%dm %02ds", m, s%60)
	}
	return fmt.Sprintf("%dh %02dm", m/60, m%60)
}

// plural returns the English plural suffix for a count: "" for one, "s"
// otherwise ("1 line", "3 lines").
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// truncateWidth keeps the longest rune prefix of s whose display width is at
// most w (the caller appends the ellipsis). It is the width-aware truncation
// shared by the tool-entry args and any other fixed-width single-line detail.
func truncateWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	var sb strings.Builder
	cw := 0
	for _, r := range s {
		if cw+1 > w {
			break
		}
		sb.WriteRune(r)
		cw++
	}
	return sb.String()
}

// readRangeHint extracts the explicit 1-based line range a `read` call was
// invoked with from its raw JSON args (issue #204). Both start_line and
// end_line must be present as positive integers; omitted or null limits
// (whole-file reads), fractional values, and malformed shapes return "" so the
// entry head falls back to the path-only rendering — never a crash.
func readRangeHint(argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	start, ok := lineArg(args, "start_line")
	if !ok {
		return ""
	}
	end, ok := lineArg(args, "end_line")
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d-%d", start, end)
}

// lineArg reads a 1-based integer tool argument from raw JSON args. It reports
// ok=false when the arg is absent, null, non-numeric, fractional, or
// non-positive, so range parsing can never emit a bogus tag from an unexpected
// argument shape.
func lineArg(args map[string]any, key string) (int, bool) {
	v, ok := args[key].(float64)
	if !ok || v != math.Trunc(v) || v < 1 {
		return 0, false
	}
	return int(v), true
}

// toolArgsHint extracts a short display hint from a tool call's raw JSON args:
// the `path` for file tools, the `command` for bash, else the raw string
// trimmed to a single line. It keeps the one-line entry glanceable and never
// throws away the model's full arguments (those stay in the engine transcript).
func toolArgsHint(argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		s := strings.TrimSpace(argsJSON)
		if s == "{}" {
			return ""
		}
		return s
	}
	for _, key := range []string{"path", "command", "url"} {
		if s, ok := args[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// telemetryWait returns a command that blocks until the next live telemetry
// update arrives on the engine seam channel, then delivers it to the UI loop as
// a telemetryUpdateMsg. The model re-issues it after each update so the strip
// keeps refreshing live (issue #86), even with no keyboard input. When the
// channel closes it returns nil so the polling stops.
func telemetryWait(te *Telemetry) tea.Cmd {
	return func() tea.Msg {
		u, ok := <-te.updates
		if !ok {
			return nil
		}
		return telemetryUpdateMsg{update: u}
	}
}

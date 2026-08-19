package tui

import (
	"context"
	"fmt"
	"io"
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

// Busy-state motion: the static "… thinking" line is the
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

// Turn runs one agent conversation turn (user prompt -> assistant answer) over
// the shared engine seam. It is what the model depends on; both the real engine
// (internal/engine) and tests implement it, so conversation behavior is testable
// without a terminal or a live provider.
//
// payload carries a slash-activated skill payload: when non-empty,
// the args turn reaches the engine with the skill body in context so the model
// acts on the args with the skill instructions loaded.
type Turn func(ctx context.Context, prompt string, payload string) (TurnResult, error)

// payload carries the slash-activated skill payload into the model's context
// for a follow-up args turn . It is the rendered
// <skill_content>/<skill_resources> body; an empty string means no injection
// (bare `/skillname` or an ordinary user turn).
// TurnResult is the outcome of one conversation turn: the final assistant
// answer plus any reasoning produced along the way. Reasoning is kept on a
// separate channel so the TUI can render it as a collapsible thinking block and
// never merge it into the answer (ticket #17).
type TurnResult struct {
	Answer    string
	Reasoning string
	// Stopped is true when the turn was aborted by the user (via the TUI's
	// cancel handle) rather than failing: the engine's stop sentinel is mapped
	// to this by the app adapter, so the TUI never imports the engine package
	// to tell a stop from an error.
	Stopped bool
}

// message is one committed line of the conversation log.
type message struct {
	role      string // "you" or "eitri"
	content   string
	reasoning string // assistant chain-of-thought, rendered as a collapsible block
	streaming bool   // true while this assistant reply is still growing from the answer stream
	// thinkingRequested records whether the turn that produced this message
	// actually asked for chain-of-thought (the provider's ThinkingEnabled at
	// request construction). It is folded into message state at the moment the
	// turn is requested and NEVER re-sniffed from config at render time, so an
	// error/retry turn stays faithful to how it was actually requested (issue
	// #264). The transcript and clipboard renderers show a reasoning block only
	// when thinking was requested AND reasoning content is non-empty — a
	// defense-in-depth backstop against a backend that sneaks reasoning through
	// a thinking-off turn.
	thinkingRequested bool
	// thinkingExpanded is true while this turn's reasoning block is expanded
	//: it defaults false (auto-collapsed) so reasoning never clogs
	// the final reply, and collapses back when the turn's answer lands. Tab
	// toggles it during and after streaming.
	thinkingExpanded bool
	// thinkingCollapsed is a per-turn collapse override that keeps this turn's
	// reasoning block collapsed even while the global Ctrl+E expanded-view mode
	// is ON . It mirrors the tool log's collapsedOverride: with the
	// mode ON the tab thinking toggle flips this to collapse/re-expand a single
	// block independent of the mode, and the effective expansion computed at
	// render time resolves it.
	thinkingCollapsed bool
	// stopped is true when this assistant message is the partial output of a
	// user-stopped turn: it renders with the stopped marker and a distinct
	// pane, never as an error, and keeps whatever content had already streamed.
	stopped bool
}

type turnDoneMsg struct {
	prompt    string
	answer    string
	reasoning string
	err       error
	// stopped is true when the turn was aborted by the user: the partial
	// answer/reasoning fields carry what had been produced, and the UI keeps
	// it on screen marked as stopped instead of dropping it as an error.
	stopped bool
}

// telemetryUpdateMsg carries one queued live telemetry update from the engine
// seam into the UI loop. It is produced by a waiting command (telemetryWait) so
// the status strip refreshes live even with no keyboard input.
type telemetryUpdateMsg struct {
	update TelemetryUpdate
}

// skillDoneMsg reports a slash-command skill activation's result. args, when
// non-empty, carries the trailing `/skillname <args>` remainder that must run
// as a follow-up user turn after the injected skill note .
type skillDoneMsg struct {
	payload string
	args    string
}

// loginCodeMsg reports the human-visible device-flow code for an in-flight
// `/login` command, plus the channel waiter should block on for the next login
// event. The model renders the code immediately, then re-issues the wait so
// the eventual completion still lands.
type loginCodeMsg struct {
	code LoginCode
	next <-chan tea.Msg
}

// loginDoneMsg reports the result of one built-in `/login` command. On
// success it carries the fresh config persisted by the login seam.
type loginDoneMsg struct {
	cfg config.Config
	err error
}

// discoverDoneMsg reports the outcome of one on-demand provider model
// discovery started from Settings . provider tags the draft
// provider the request was issued for so stale results from an earlier draft
// switch can be dropped.
type discoverDoneMsg struct {
	provider string
	models   []string
	err      error
}

// SkillItem is one detected skill surfaced to the TUI's slash-command surface:
// its name is what `/skillname` matches and what the `/` completion list
// offers. The rail no longer carries a skills panel, so no
// scope or activation state is tracked TUI-side .
type SkillItem struct {
	Name string
}

// SkillsSurface wires the TUI's slash-command activation to the run's tool
// layer . Items lists the detected skill names; Activate runs one skill
// activation (the T8 `skill` tool via the engine/registry seam) and returns
// the activation payload. Nil means no skills were detected.
type SkillsSurface struct {
	Items    []SkillItem
	Activate func(ctx context.Context, name string) (string, error)
}

// LoginCode is the human-visible device-flow challenge surfaced by `/login`:
// the verification URL to open and the short user code to enter there.
type LoginCode struct {
	UserCode        string
	VerificationURI string
}

// Dependencies wires a Model to its environment: the conversation Turn, model
// discovery + loaded config for the Settings surface, and a persistence seam.
// Fields are optional; a zero Dependencies yields a plain chat-only model (the
// pre-settings default, kept for tests and lean embeds).
type Dependencies struct {
	// Turn drives one conversation turn (engine). Required for chat.
	Turn Turn
	// WorkspacePath is the project/read-only state surfaced above the
	// transcript: the workspace directory the run operates in.
	// Empty means no workspace header is rendered (the plain chat default).
	WorkspacePath string
	// Models is the provider-discovered model list surfaced in Settings.
	Models []string
	// DiscoverModels, when non-nil, is an on-demand provider model-discovery
	// seam: it is invoked for the current Settings draft when
	// the panel opens with no pre-seeded Models list, and again when the draft
	// provider changes, so provider selection re-discovers that provider's
	// model lineup before Save. Nil disables on-demand discovery (the pre-
	// seeded list, or the configured model alone, is shown).
	DiscoverModels func(ctx context.Context, cfg config.Config) ([]string, error)
	// Config is the loaded config seeded into the Settings draft.
	Config config.Config
	// Save persists a Settings edit to the config layer. When nil, Settings can
	// still be opened/dismissed but saving is a no-op (view-only).
	Save func(config.Config) error
	// SaveBack, when non-nil, is invoked after an in-TUI config mutation
	// succeeds (Settings Save or `/login`) so the app can refresh its in-process
	// config/provider view.
	SaveBack func(config.Config)
	// Login, when non-nil, backs the built-in `/login` slash command. The seam
	// may surface a device-flow code via onCode, then returns the fresh config
	// once the login completed and persisted.
	Login func(ctx context.Context, onCode func(LoginCode)) (config.Config, error)
	// Skills, when non-nil, backs the slash-command surface: `/skillname`
	// activation and the `/` completion list. Nil disables `/skillname`
	// commands (no skills). The right rail never renders skills .
	Skills *SkillsSurface
	// Telemetry, when non-nil, renders the live bottom status strip:
	// model, effort, thinking, turns/max, and the cache hit-ratio gauge,
	// fed live from the engine seam. The cost readout was removed in issue
	// #374. Nil disables the strip.
	Telemetry *Telemetry
	// Stream, when non-nil, feeds the live assistant answer-text stream (issue
	// #83): the engine's AnswerStream deltas arrive here and the in-progress
	// assistant message re-renders in place, growing token by token instead of
	// one full-reply dump on completion. Nil falls back to the historical
	// blocking answer-on-completion behaviour.
	Stream *Streamer
	// Tools, when non-nil, feeds the live tool-call stream: each
	// ToolCallEvent/ToolResultEvent arrives here and renders as a compact,
	// collapsed `⊕ tool args` one-liner that expands to the full result.
	// Nil disables tool entries (the pre-seam default).
	Tools *ToolFeed
	// Rail, when non-nil, enables the toggleable right context rail (issue
	// #88): a fixed-width pane alongside the transcript showing STATS / CONTEXT
	// / MODEL, fed from the telemetry surface. Nil hides the rail (the plain
	// chat default).
	Rail *Rail
	// ThinkingSuppression, when non-nil, reports whether the run's provider can
	// actually suppress reasoning on the wire when thinking is off (
	// AC-3). Nil assumes support (keeps view-only panels and providers without
	// the declared capability warning-free). The Settings panel shows a warning
	// when thinking is off and this seam reports false.
	ThinkingSuppression func() bool
	// Clipboard writes text to the system clipboard: Ctrl+O and
	// /copy copy the full transcript through it. Nil falls back to the
	// atotto/clipboard package default.
	Clipboard func(text string) error
	// OSC52Out, when non-nil, is the terminal output the copy fallback writes
	// its OSC 52 sequence to: when the Clipboard path fails (e.g.
	// no xclip/wl-clipboard), the copy emits the OSC 52 clipboard sequence to
	// this writer and the terminal puts the text on the system clipboard. Nil
	// defaults to os.Stdout, so the fallback works out of the box in the
	// interactive TUI.
	OSC52Out io.Writer
}

// Model is the Bubble Tea state backing the TUI. Since the expand-contract
// refactor, it owns ONLY its own regions and wires: the
// single textarea composer, the Settings surface (ctrl+s), the interactive
// max-turns continuation prompt, and the detected-skills slash completion, plus
// the composable seams it depends on (turn, deps, theme, telemetry, stream,
// toolFeed, clipboard). EVERY transcript concern (history, tool log, rail,
// width/height, follow, drag-select) lives on the owned value
// Transcript field tx: the transcript is a genuinely owned,
// mutating surface rather than a per-frame value rebuilt from duplicated Model
// fields. It renders through the alternate screen (T1 pivot, ), so
// every frame is a clean full-surface repaint and the history scroll/clip lives
// in the native viewport the Transcript owns.
type Model struct {
	composer textarea.Model
	td       *TurnDispatch
	deps     Dependencies

	// tx is the owned transcript surface: the single owner of the
	// history, tool log, right rail, width/height, follow position,
	// drag-select, and their render/navigation. Model mutates it in
	// place and never keeps a second transcript state copy. See transcript.go.
	tx Transcript

	// settings state: non-nil means the Settings surface is open.
	settings *settingsForm
	savedMsg string

	// interactive continuation: continueReq/continueResp carry the max-turns
	// prompt between the running engine goroutine and the main loop. prompting
	// is true while a decision is being awaited.
	continueReq  chan struct{}
	continueResp chan bool
	prompting    bool

	// skills is the live list backing the slash-command completion, refreshed
	// from the Dependencies snapshot at construction.
	skills []SkillItem

	// slashIdx is the combo completion's currently selected candidate index into
	// slashCandidates for the composer's current `/...` line . It
	// is the highlighted row of the on-screen completion list; 0 (the built-in
	// /settings row) by default, advanced as the user tabs-through or edits.
	slashIdx int
	// slashPrefix is the raw `/...` prefix the user typed into the composer
	// before any tab-autocompletion filled the remainder. The completion list
	// and tab-cycling are driven off it so a bare `/` keeps its full candidate
	// list even as tab fills one candidate in (tab walks the whole list, then
	// wraps). It updates only while the user edits the line, never on tab-fill.
	slashPrefix string

	// telemetry is the live status strip state ; nil disables it.
	telemetry *Telemetry

	// stream is the live answer-text stream ; nil disables streaming.
	stream *Streamer

	// toolFeed is the live tool-call stream ; nil disables tool
	// entries.
	toolFeed *ToolFeed

	// clipboard writes the plain-text transcript to the system clipboard (issue
	// #123): Ctrl+O and /copy route here. It is the injected Dependencies
	// seam, defaulting to the atotto/clipboard package's WriteAll wrapped in
	// the OSC 52 fallback so a failing system-clipboard path still
	// copies through an OSC 52-capable terminal.
	clipboard func(text string) error
}

// NewModel builds a bare chat-only model (no Settings surface), the historical
// default signature. The interactive app uses NewModelCfg for Settings.
func NewModel(t Turn) Model {
	return NewModelCfg(Dependencies{Turn: t})
}

// Composer caret style policy: the composer's hardware caret is
// deliberately a steady (non-blinking) block rather than whatever the textarea
// or terminal defaults would draw.
//
// Shape is block: it keeps the visual identity of the software reverse-video
// block caret the hardware caret replaced and matches the
// near-universal terminal default, so a terminal that ignores the shape request
// degrades to the same visible block — the caret is never hidden by the policy.
//
// Blink is off and fixed (not the terminal default): the caret's presence
// already signals editability — it is attached only while the composer is the
// active editing surface and hidden otherwise — so blinking adds
// noise without carrying information.
//
// Color is not forced: the textarea default paints a fixed white
// caret (lipgloss.Color("7")), and since the composer's caret is the terminal's
// hardware caret that non-nil color propagates into the reported
// tea.Cursor and the renderer emits a SetCursorColor sequence every frame,
// overwriting the user's configured cursor color. A nil color means the
// renderer emits no SetCursorColor, so the terminal draws the caret in its own
// configured color, matching pre-Eitri state.
const (
	composerCaretShape = tea.CursorBlock
	composerCaretBlink = false
)

// NewModelCfg builds a TUI model wired to the given dependencies.
func NewModelCfg(d Dependencies) Model {
	comp := textarea.New()
	comp.Placeholder = g("Ask Eitri something…", "Ask Eitri something...")
	if !localeSupportsUTF8() {
		comp.Prompt = "| " // ASCII composer rail
	} else {
		comp.Prompt = g("┃ ", "| ")
	}
	comp.Focus()
	comp.CharLimit = 0
	comp.ShowLineNumbers = false
	// Start compact: the composer rests at minComposerRows and grows with the
	// draft up to maxComposerRows, so an empty composer reads as a two-row
	// multi-line input.
	comp.SetHeight(minComposerRows)
	// The composer's caret is the terminal's hardware cursor: the
	// textarea's software reverse-video caret cell is disabled so the terminal
	// itself draws the caret at the edit position.
	comp.SetVirtualCursor(false)
	// Apply the explicit caret style policy instead of inheriting
	// the textarea default (block + blink), and give the composer rail the
	// agent accent so the input surface reads as accent-framed (benchmark
	// §4.4: mode-colored composer border).
	th := themeFor(d.Config.Theme)
	st := comp.Styles()
	st.Cursor.Shape = composerCaretShape
	st.Cursor.Blink = composerCaretBlink
	// No caret color is forced: leave Color nil so the renderer
	// emits no SetCursorColor and the hardware caret uses the terminal's
	// configured cursor color instead of the textarea default white.
	st.Cursor.Color = nil
	st.Focused.Prompt = lipgloss.NewStyle().Foreground(th.accent)
	comp.SetStyles(st)

	// The owned transcript surface: the single owner of the
	// history, tool log, rail, width/height, follow, and drag-select. Model
	// mutates it in place; there is no duplicate transcript state elsewhere.
	transcript := Transcript{
		theme:           th,
		configTheme:     d.Config.Theme,
		workspacePath:   d.WorkspacePath,
		reasoningEffort: d.Config.ReasoningEffort,
		telemetry:       d.Telemetry,
		rail:            d.Rail,
		railWidth:       d.Config.RailWidth,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		layout:          &transcriptLayout{dirty: true},
	}

	m := Model{
		composer:     comp,
		td:           NewTurnDispatch(d.Turn),
		deps:         d,
		tx:           transcript,
		continueReq:  make(chan struct{}, 1),
		continueResp: make(chan bool, 1),
		skills:       skillSnapshot(d),
		telemetry:    d.Telemetry,
		stream:       d.Stream,
		toolFeed:     d.Tools,
		clipboard:    newClipboard(d),
	}
	// The layout starts stale, but the pointer share means the Transcript's
	// first hit-test builds it lazily; the explicit dirty below keeps the
	// semantic explicit .
	m.td.SetThinkingEnabled(d.Config.ThinkingEnabled)
	m.tx.layout.dirty = true
	// An unknown hand-edited theme warns once on startup via the status strip,
	// naming the fallback, instead of failing silently: the renderer still
	// falls back to dark per . Valid themes never
	// warn. The warning is one-shot — savedMsg renders once, then clears.
	if !isSupportedTheme(d.Config.Theme) {
		m.savedMsg = fmt.Sprintf("unknown theme %q, using %s", d.Config.Theme, config.DefaultTheme)
	}
	return m
}

// newClipboard returns the clipboard write seam: the injected
// Dependencies.Clipboard when set, else the atotto/clipboard package default so
// Ctrl+O and /copy work out of the box. The returned seam is wrapped in the
// OSC 52 fallback: when the primary path fails, the copy re-routes
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
// alt-screen pivot, ) as a heap-allocated bubbletea/viewport. It is a
// pointer so scroll-state changes made by View (which runs on a value copy)
// survive across render cycles. It starts unsized (0x0) until the first
// WindowSizeMsg lands.
//
// Mouse-wheel and keyed navigation live on the Transcript, not the
// component's own Update: Transcript.navigateMouse / navigateHistory call
// ScrollUp/ScrollDown directly so the follow position (histFollow) can break on
// scroll-up and re-engage on reaching the bottom. The wheel delta of 3 matches
// the component's own default MouseWheelDelta.
func newHistoryViewport() *viewport.Model {
	v := viewport.New()
	v.MouseWheelEnabled = false
	return &v
}

// SetTurnDispatch wires the TurnDispatch that owns the turn state machine.
// It is used at boot to wire the engine seam after the Model is constructed.
func (m *Model) SetTurnDispatch(td *TurnDispatch) { m.td = td }

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
// with no keyboard input, plus the one-second clock that keeps the
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
	// the copy note), then any later message drops it .
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
		// refreshing live, with no keyboard input required.
		if m.telemetry == nil {
			return m, nil
		}
		m.telemetry.apply(msgi.update)
		return m, telemetryWait(m.telemetry)

	case toolUpdateMsg:
		// A tool-call observation arrived through the waiting command: fold it
		// into the tool log owned by the Transcript and immediately re-issue the
		// waiter so further tool calls stream in . Updates arriving
		// when no feed is wired are dropped so they never spawn spurious entries.
		if m.toolFeed == nil {
			return m, nil
		}
		m.tx.apply(msgi.update) // tool updates route through the Transcript 
		return m, toolWait(m.toolFeed)

	case streamDeltaMsg:
		// A streamed delta (reasoning or answer text) arrived through the waiting
		// command: grow the in-progress assistant message's thinking or answer
		// buffer in place and immediately re-issue the waiter so the streams keep
		// coming . Deltas arriving after the turn completed (a
		// race with the final delta) are dropped so they never spawn a spurious
		// assistant message.
		if m.stream == nil || !m.tx.busy {
			return m, nil
		}
		m.appendStreamDelta(msgi.kind, msgi.delta)
		m.tx.layout.dirty = true // the in-progress message grew
		return m, streamWait(m.stream)

	case tea.WindowSizeMsg:
		m.tx.width = msgi.Width
		m.tx.height = msgi.Height
		m.syncWidths()
		m.tx.layout.dirty = true // width change re-wraps the transcript rows
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
		// Review overlay is gone: ctrl+d is deliberately released,
		// not re-mapped, so there is no dedicated key routing before the composer.
		switch msgi.String() {
		case "ctrl+c":
			// Ctrl+C stops a running turn or quits when idle: a natural
			// dual binding where the action matches the current state.
			if m.tx.busy {
				m.stopTurn()
				return m, nil
			}
			return m, tea.Quit
		case "esc":
			// esc while a turn runs stops it: the cancel handle aborts the
			// in-flight turn at the context boundary (the provider wire and any
			// running tool see the cancellation; the engine surfaces the stop).
			// The partial reply already on screen stays, marked as stopped. esc
			// while idle remains a no-op (vim-normal mode is gone).
			if m.tx.busy {
				m.stopTurn()
			}
			return m, nil
		case "ctrl+s":
			return m.startSettings()
		case "pgup", "home":
			// History scroll navigation: PgUp pages up and
			// Home jumps to the oldest output. Scrolling up breaks the follow
			// position so the transcript stays put instead of being yanked to
			// the newest; navigation never steals composer focus (the composer
			// keeps arrow-key editing). The Transcript owns the viewport + follow
			// decision ; Model mutates it in place.
			m.tx.navigateHistory(msgi.String())
			return m, nil
		case "pgdown", "end":
			// PgDn pages down and End jumps to the newest output. Reaching the
			// bottom re-engages the follow position ; the
			// Transcript owns the decision .
			m.tx.navigateHistory(msgi.String())
			return m, nil
		case "ctrl+b":
			// Reserved no-op: the right context rail is the permanent stats
			// surface with no show/hide toggle, so ctrl+b is no
			// longer bound. The key stays unused rather than re-mapped, keeping
			// the composer's caret/selection editing untouched.
			return m, nil
		case "ctrl+d":
			// Deliberately UNBOUND: Ctrl+D was the modal review
			// panel's toggle and reads as
			// "exit" in other tools, so the key is released rather than
			// re-mapped. File-change inspection now lives in-flow on the
			// Ctrl+E expanded cards ; nothing consumes this key.
			m.syncComposerRail()
			return m, nil
		case "ctrl+o":
			// Copy the full transcript to the system clipboard,
			// reporting success/failure as a band status note. Never mutates the
			// transcript or the agent loop.
			m.copyTranscript()
			return m, nil
		case "ctrl+e":
			// Toggle the persistent Ctrl+E expanded-view mode over the whole
			// transcript: one global flag that switches every tool
			// entry between its collapsed delta summary and the fully expanded
			// framed result. Sticky until toggled off; per-entry click-to-expand
			// stays orthogonal and independent of the mode. The flag lives
			// on the owned Transcript, which mutates in place.
			m.tx.toggleExpandAll()
			return m, nil
		case "ctrl+j", "shift+enter":
			// Shift+Enter newline: terminals deliver Shift+Enter
			// two ways. Legacy terminals send the line-feed byte, which Bubble Tea
			// surfaces as KeyCtrlJ; terminals with the enhanced (CSI u / kitty)
			// keyboard protocol send an explicit Shift+Enter key, decoded as
			// "shift+enter". Both insert a line break instead of submitting;
			// no-op while a turn is running (ticket #57).
			if m.tx.busy {
				return m, nil
			}
			m.composer.InsertString("\n")
			m.syncComposerHeight()
			return m, nil
		case "enter":
			if m.tx.busy {
				return m, nil
			}
			prompt := strings.TrimSpace(m.composer.Value())
			if prompt == "" {
				return m, nil
			}
			// A new submitted turn re-engages the follow position (
			// AC3): the user is now following new output, so the viewport stops
			// holding a stale reading offset and re-anchors to the newest. The
			// Transcript owns the follow flag .
			m.tx.histFollow = true
			m.composer.Reset()
			m.syncComposerHeight()
			m.slashIdx = 0
			m.slashPrefix = ""
			// A built-in slash command routes to its handler instead of the
			// engine: `/settings` opens Settings, `/copy` copies the transcript,
			// `/login` runs interactive provider login, and `/skillname`
			// activates that skill via the T8 run, surfacing the result rather
			// than sending the raw command as a chat prompt. Any other `/...`
			// line is a normal prompt and is sent to the engine seam unchanged
			// .
			if prompt == "/settings" {
				return m.startSettings()
			}
			// /copy copies the full transcript to the clipboard through the same
			// seam as Ctrl+O ; the command is never sent as a
			// prompt.
			if prompt == "/copy" {
				m.copyTranscript()
				return m, nil
			}
			// /login runs the provider-backed interactive login seam (Copilot's
			// device flow today) and surfaces the code/result inside the TUI
			// instead of sending `/login` as a normal prompt.
			if prompt == "/login" {
				return m.startLogin()
			}
			// /help appends the help message as an assistant entry in the transcript,
			// never sent to the engine. The same rendered output the `?` keybinding
			// produces, keeping both paths identical.
			if prompt == "/help" {
				m.tx.messages = append(m.tx.messages, message{role: "eitri", content: helpView(m.tx.theme)})
				return m, nil
			}
			if name, args, ok := slashCommand(prompt, m.skills); ok {
				return m.activateSkill(name, args)
			}
			cmd := m.startTurn(prompt, "")
			return m, cmd
		case "tab":
			// Fresh `/` with tab walks the slash completion list: the built-in
			// `/settings` command plus matching detected skills .
			// Otherwise tab toggles the thinking stream.
			if m.composer.Value() != "" && strings.HasPrefix(m.composer.Value(), "/") && len(slashCandidates(m.composer.Value(), m.skills)) > 0 {
				m.completeSlashCommand()
				return m, nil
			}
			// Toggle the current/last turn's collapsible thinking block (auto-collapsed
			// by default; per-turn expansion, ). Toggling the newest
			// assistant block lets the user watch that turn's reasoning on demand.
			// The messages and layout belong to the owned Transcript .
			if m.td.s.curStream >= 0 && m.td.s.curStream < len(m.tx.messages) {
				// During a stream, target the in-progress assistant message.
				m.tx.toggleThinking(m.td.s.curStream)
			} else {
				// Otherwise toggle the most recent assistant (eitri) message.
				for i := len(m.tx.messages) - 1; i >= 0; i-- {
					if m.tx.messages[i].role != "you" {
						m.tx.toggleThinking(i)
						break
					}
				}
			}
			return m, nil
		case "ctrl+x", "ctrl+shift+[":
			// Ctrl+X (Danish/Nordic: also Ctrl+Shift+[ on US) shrinks the right
			// context rail by 2 columns, clamped to minWidthRail, and persists
			// the new width to config. X is a dedicated alphabetic key on every
			// layout, whereas the bracket lives behind AltGr+8/9 on Nordic
			// keyboards, so the X binding is the universally reachable one.
			m.adjustRailWidth(-2)
			return m, nil
		case "ctrl+z", "ctrl+shift+]":
			// Ctrl+Z (Danish/Nordic: also Ctrl+Shift+] on US) grows the right
			// context rail by 2 columns and persists the new width to config.
			// Z is the dedicated alphabetic twin of X and reachable on every
			// layout. Neither Ctrl+Z nor Ctrl+X is claimed by the composer
			// textarea (it has no undo/cut bindings), so both stay free.
			m.adjustRailWidth(+2)
			return m, nil
		case "alt+0":
			// Alt+0 resets the right context rail to the default width and
			// persists the reset to config.
			m.tx.setRailWidth(defaultRailWidth)
			m.persistRailWidth()
			m.syncWidths()
			return m, nil
		case "?":
			// `?` shows help when the composer is empty and the app is idle
			// (not busy, not prompting, not settings open). With text in the
			// composer, `?` falls through to the textarea so it inserts a literal
			// character like any other key.
			if m.composer.Value() == "" && !m.tx.busy {
				m.tx.messages = append(m.tx.messages, message{role: "eitri", content: helpView(m.tx.theme)})
				return m, nil
			}
		}
		// The Ctrl+E expanded-view toggle is handled as a dedicated
		// case above; the legacy alt+y global tool-expand path has been superseded
		// and removed so there is exactly one way to toggle the mode.
		// Let the textarea handle editing (cursor, backspace, etc.).
		nm, cmd := m.composer.Update(msg)
		m.composer = nm
		// Editing a `/...` line re-syncs the completion: the raw typed prefix is
		// remembered (so tab can walk the full list even after filling one
		// candidate) and the selection resets to the first candidate (
		// AC1). Tab-selection survives because it returns before reaching here.
		val := m.composer.Value()
		if strings.HasPrefix(val, "/") {
			m.slashPrefix = val
			m.slashIdx = 0
		} else {
			// The line no longer starts with `/` (e.g. emptied by backspace): clear
			// the remembered prefix and selection so the completion list and its
			// reserved rows are dismissed on this/next render .
			m.slashPrefix = ""
			m.slashIdx = 0
		}
		// The draft changed: re-grow the composer within the band (
		// AC5), so the textarea's height tracks the new line count / wraps.
		m.syncComposerHeight()
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case tea.MouseMsg:
		// History mouse-wheel scroll plus click-drag selection
		//: wheel up/down over the transcript scrolls it (Up
		// breaks follow, Down reaching the bottom re-engages it); a left-button
		// drag over the history highlights a cell range and release copies it to
		// the clipboard. Requires the Bubble Tea program enabled mouse events.
		// bubbletea v2 delivers mouse events as an interface: updateMouse
		// type-switches on the concrete wheel/click/motion/release messages.
		m.updateMouse(msgi)
		return m, nil

	case turnDoneMsg:
		m.td.handleTurnDone(&m.tx, msgi)
		m.syncComposerRail()
		return m, nil
	case clockTickMsg:
		// One-second telemetry refresh: re-issue the tick and re-render so the
		// session-elapsed timer in the status strip stays live.
		return m, clockTick()

	case spinnerTickMsg:
		// Advance the busy spinner by one frame and re-issue the tick while the
		// turn still runs, so the indicator animates without keyboard input. A
		// tick that lands after the turn completed (or motion disabled) stops the
		// chain silently. The busy/spinner state lives on the owned Transcript.
		if !m.tx.busy || !motionEnabled() {
			m.tx.spinner = 0
			return m, nil
		}
		m.tx.spinner = (m.tx.spinner + 1) % len(busySpinnerFrames)
		if m.tx.busyPulse > 0 {
			m.tx.busyPulse--
		}
		return m, spinnerTick()

	case discoverDoneMsg:
		// A provider model-discovery command finished: fold the
		// result into the open settings panel, or drop it if the panel has since
		// closed (Save/esc while discovery was in flight).
		if m.settings == nil {
			return m, nil
		}
		if m.settings.cfg.Provider != msgi.provider {
			return m, nil
		}
		m.settings.models = msgi.models
		m.deps.Models = append([]string(nil), msgi.models...)
		m.settings.discoverState = discoverIdle
		m.settings.discoverErr = ""
		if msgi.err != nil {
			m.settings.discoverErr = msgi.err.Error()
			m.settings.discoverState = discoverError
			return m, nil
		}
		if len(msgi.models) != 0 && indexOf(msgi.models, m.settings.cfg.Model) < 0 {
			m.settings.cfg.Model = msgi.models[0]
		}
		return m, nil

	case skillDoneMsg:
		m.tx.messages = append(m.tx.messages, message{role: "eitri", content: msgi.payload})
		m.tx.layout.dirty = true // a skill result appended to the transcript
		if msgi.args != "" {
			// A `/skillname <args>` activation queues the args as a normal user
			// turn AFTER the injected skill note so message order renders
			// note-then-args . Bare `/skillname` has empty args and
			// dispatches no turn.
			cmd := m.startTurn(msgi.args, msgi.payload)
			return m, cmd
		}
		return m, nil

	case loginCodeMsg:
		m.tx.messages = append(m.tx.messages, message{role: "eitri", content: fmt.Sprintf("Open %s and enter code: %s", msgi.code.VerificationURI, msgi.code.UserCode)})
		m.tx.layout.dirty = true
		return m, loginWait(msgi.next)

	case loginDoneMsg:
		if msgi.err != nil {
			m.tx.messages = append(m.tx.messages, message{role: "eitri", content: failurePrefix() + msgi.err.Error()})
			m.tx.layout.dirty = true
			return m, nil
		}
		m.deps.Config = msgi.cfg
		if m.deps.SaveBack != nil {
			m.deps.SaveBack(msgi.cfg)
		}
		m.tx.messages = append(m.tx.messages, message{role: "eitri", content: "login saved"})
		m.tx.layout.dirty = true
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
// borrowing the live telemetry for the cache hit-ratio readout (the cost
// readout was removed in issue #374).
func (m *Model) openSettings() *settingsForm {
	cfg := m.deps.Config
	if cfg.Provider == "" {
		cfg = config.Default()
	}
	sf := newSettingsForm(cfg, m.deps.Models)
	sf.theme = m.tx.theme
	sf.telemetry = m.telemetry
	// The run's provider thinking-suppression capability: nil
	// assumes support, matching the settingsForm default.
	sf.thinkingSuppression = m.deps.ThinkingSuppression
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
	// model list: a seeded list is already loaded, so nothing
	// to fetch. deps.Models (not sf.models) is the seed source; sf.models holds
	// the configured-model fallback newSettingsForm installs.
	if len(m.deps.Models) != 0 || m.deps.DiscoverModels == nil {
		return m, nil
	}
	sf.discoverState = discoverLoading
	return m, discoverCmd(m.deps.DiscoverModels, sf.cfg)
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
		before := s.cfg.Provider
		s.adjust(-1)
		if s.cfg.Provider != before {
			m.settings = s
			return m, m.maybeDiscoverSettingsModels(s)
		}
	case "down", "shift+down", "right":
		before := s.cfg.Provider
		s.adjust(1)
		if s.cfg.Provider != before {
			m.settings = s
			return m, m.maybeDiscoverSettingsModels(s)
		}
	default:
		// Free-form editing on the paths field: type to append, backspace to
		// delete the trailing char. msgi.Text (not String) is appended so a
		// space types as " " — bubbletea v2 reports a space key's String() as
		// "space", not " " (pass 2, ).
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

func (m *Model) maybeDiscoverSettingsModels(s *settingsForm) tea.Cmd {
	if m.deps.DiscoverModels == nil || s == nil {
		return nil
	}
	s.discoverState = discoverLoading
	s.discoverErr = ""
	s.models = []string{s.cfg.Model}
	return discoverCmd(m.deps.DiscoverModels, s.cfg)
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
	// The theme selection re-skins the surface live: the
	// Transcript is the single owner of the palette, so the chrome (which reads
	// m.tx.theme) and the Markdown body (tx.configTheme) both follow the saved
	// value immediately instead of waiting for the next run.
	m.deps.Config = cfg
	m.tx.theme = themeFor(cfg.Theme)
	m.tx.configTheme = cfg.Theme
	m.settings = nil
}

// startTurn delegates to the TurnDispatch, which installs the per-turn
// cancel handle, appends a user message, marks busy, resets the stream
// cursor, and anchors the tool log. Model adds the stream/spinner batch
// and syncs the composer rail.
func (m *Model) startTurn(prompt string, payload string) tea.Cmd {
	m.td.startTurn(&m.tx, prompt, payload)
	m.syncComposerRail()
	// With a live answer stream, the composer turn and the stream waiter run
	// concurrently so the reply grows in place as deltas arrive;
	// the spinner tick rides the same batch so the busy indicator animates.
	// The non-streaming fallback keeps the single turn command.
	if m.stream != nil {
		return tea.Batch(m.td.turnCmd(prompt, payload), streamWait(m.stream), spinnerTick())
	}
	return m.td.turnCmd(prompt, payload)
}

// stopTurn delegates to the TurnDispatch, which cancels the per-turn
// context. It is a no-op when nothing is running.
func (m *Model) stopTurn() { m.td.stopTurn() }

// appendStreamDelta delegates to the TurnDispatch, which grows the
// in-progress assistant message by one streamed delta.
func (m *Model) appendStreamDelta(kind StreamKind, delta string) {
	m.td.appendStreamDelta(&m.tx, kind, delta)
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

// copyTranscript copies the plain-text transcript to the system clipboard
// through the injected seam: Ctrl+O and /copy both route here. The
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
//: role-marked user prompts and assistant answers, per-turn
// reasoning blocks, and the interleaved tool-call entries (compact one-liner
// plus full result when complete) — all ANSI-free so the pasted session is
// clean. It never mutates the transcript or the agent loop.
func (m Model) transcriptText() string {
	var b strings.Builder
	for i, msg := range m.tx.messages {
		// The clipboard honors the same thinking-requested gate as the transcript
		//: a reasoning block is transcribed only for a turn that
		// requested thinking. A backend sneaking reasoning through a thinking-off
		// turn never reaches the clipboard.
		if msg.role != "you" && msg.thinkingRequested && msg.reasoning != "" {
			b.WriteString("🤔 " + msg.reasoning + "\n")
		}
		if msg.role == "you" {
			b.WriteString("you: " + msg.content + "\n")
		} else {
			b.WriteString("eitri: " + msg.content + "\n")
		}
		// The turn's tool entries transcribe through the tool log's plain-text
		// surface, so the clipboard and transcript never disagree
		// on an entry.
		b.WriteString(m.tx.log.PlainText(i))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// slashCommand reports whether prompt is a `/skillname` activation command for a
// detected skill . It returns the bare skill name, any trailing args,
// and ok. A `/skillname` line returns the name with no args; a
// `/skillname <args>` line splits the args on the first whitespace after the
// name (trimmed), so the parser recognises name + args while bare `/skillname`
// still returns a no-args name. Non-command `/...` lines (real paths, unknown
// skills) fall through with ok=false and are sent as a normal prompt.
func slashCommand(prompt string, skills []SkillItem) (name, args string, ok bool) {
	if len(skills) == 0 || !strings.HasPrefix(prompt, "/") {
		return "", "", false
	}
	// Split on the first whitespace after the leading slash: the head is the
	// candidate skill name, and any remainder (trimmed) is the trailing args. A
	// skill-only line (bare `/name`, or `/name ` / `/name\t` whose trailing
	// whitespace trims away) yields no args.
	rest := strings.TrimSpace(strings.TrimPrefix(prompt, "/"))
	name, args = rest, ""
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		name = rest[:i]
		args = rest[i+1:]
	}
	if name == "" {
		return "", "", false
	}
	for _, it := range skills {
		if it.Name == name {
			return name, strings.TrimSpace(args), true
		}
	}
	return "", "", false
}

// activateSkill runs one slash-command activation through the SkillsSurface
// activation seam (the T8 skill tool) on a detached command and renders the
// result as an assistant note.
func (m Model) activateSkill(name, args string) (tea.Model, tea.Cmd) {
	m.tx.messages = append(m.tx.messages, message{role: "you", content: "/" + name})
	m.tx.layout.dirty = true
	if m.deps.Skills == nil || m.deps.Skills.Activate == nil {
		m.tx.messages = append(m.tx.messages, message{role: "eitri", content: failurePrefix() + "no skill activation available"})
		return m, nil
	}
	return m, skillCmd(m.deps.Skills.Activate, name, args)
}

// startLogin runs the built-in `/login` command through the interactive login
// seam and renders its code/result as assistant notes.
func (m Model) startLogin() (tea.Model, tea.Cmd) {
	m.tx.messages = append(m.tx.messages, message{role: "you", content: "/login"})
	m.tx.layout.dirty = true
	if m.deps.Login == nil {
		m.tx.messages = append(m.tx.messages, message{role: "eitri", content: failurePrefix() + "no login flow available"})
		return m, nil
	}
	return m, loginCmd(m.deps.Login)
}

// discoverCmd runs one on-demand provider model discovery off the main loop and
// reports its result . It keeps the provider seam (Models) off
// the UI goroutine so discovery latency never blocks rendering.
func discoverCmd(discover func(ctx context.Context, cfg config.Config) ([]string, error), cfg config.Config) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		models, err := discover(ctx, cfg)
		return discoverDoneMsg{provider: cfg.Provider, models: models, err: err}
	})
}

// skillCmd runs a skill activation off the main loop and reports its payload.
// args, when non-empty, rides along on skillDoneMsg so the handler can queue
// the follow-up user turn after injecting the skill note .
func skillCmd(activate func(ctx context.Context, name string) (string, error), name, args string) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		payload, err := activate(ctx, name)
		if err != nil {
			return turnDoneMsg{err: fmt.Errorf("activate skill %q: %w", name, err)}
		}
		return skillDoneMsg{payload: payload, args: args}
	})
}

// loginCmd runs the interactive login seam off the main loop. Device-flow code
// arrivals are forwarded immediately as loginCodeMsg values; completion lands
// as a final loginDoneMsg.
func loginCmd(login func(ctx context.Context, onCode func(LoginCode)) (config.Config, error)) tea.Cmd {
	ch := make(chan tea.Msg, 2)
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cfg, err := login(ctx, func(code LoginCode) {
			ch <- loginCodeMsg{code: code, next: ch}
		})
		ch <- loginDoneMsg{cfg: cfg, err: err}
		close(ch)
	}()
	return loginWait(ch)
}

// loginWait blocks for the next in-flight login event, returning nil once the
// login goroutine has finished and closed the channel.
func loginWait(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// slashCandidates returns the ordered slash-command completion candidates for
// the current composer value: the built-in `/settings`,
// `/copy`, and `/login` commands first, then every detected skill whose name
// starts with the `/...` partial. A partial of "" means a bare `/`, listing
// every command & skill. It returns nil when the value does not start with `/`.
func slashCandidates(value string, skills []SkillItem) []string {
	if !strings.HasPrefix(value, "/") {
		return nil
	}
	partial := strings.TrimSpace(strings.TrimPrefix(value, "/"))
	cands := make([]string, 0, len(skills)+3)
	if partial == "" || strings.HasPrefix("settings", partial) {
		cands = append(cands, "/settings")
	}
	if partial == "" || strings.HasPrefix("copy", partial) {
		cands = append(cands, "/copy")
	}
	if partial == "" || strings.HasPrefix("login", partial) {
		cands = append(cands, "/login")
	}
	if partial == "" || strings.HasPrefix("help", partial) {
		cands = append(cands, "/help")
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
// candidate, cycling deterministicly through the built-in commands and
// matching detected skills . The candidate list is driven off
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
// The view declares the alternate screen as a field (pass 1, ): every frame
// is a clean full-surface repaint into the alt buffer, so a resize never
// duplicates or scatters text — the settings that v1 pushed through program
// options (tea.WithAltScreen) live here declaratively.
func (m Model) View() tea.View {
	content := m.viewString()
	v := tea.NewView(content)
	v.AltScreen = true
	// The composer's caret is the terminal's hardware cursor: the
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
		return promptView(m.tx.theme)
	}

	// The right context rail: the rendered transcript pane
	// and the state rail sit side by side — one pane for time (transcript), one
	// for state (rail). Since the rail surface — its visibility, band/
	// transcript width accounting, clamp height, and render — resolves entirely
	// on the owned Transcript (viewWithRail); the band stays a Model-owned
	// concern, so Model passes its rendered row count down. The band spans the
	// full terminal width under the rail, which floats above it.
	return m.tx.viewWithRail(m.renderPane(), m.bandHeight())
}

// minComposerRows is how tall the composer rests when the draft is empty,
// so the input field reads as a multi-line composer rather than a single-line
// prompt. It is the floor the composer returns to after submit/reset; the
// short-terminal clamp below can still shrink it further so the band never
// pushes off-screen.
const minComposerRows = 2

// maxComposerRows is how tall the composer may grow inside the fixed bottom
// band before it scrolls internally: a long draft never
// spills into the transcript — the textarea's own viewport scrolls past this
// bound, and the band stays pinned while the history viewport yields rows.
const maxComposerRows = 8

// syncComposerHeight grows the composer with its draft up to maxComposerRows,
// then lets the textarea scroll internally: an empty draft
// rests at minComposerRows, each new line adds a row up to the
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
	if rows < minComposerRows {
		rows = minComposerRows
	}
	// The terminal height lives on the owned Transcript .
	if m.tx.height > 0 {
		// The band also holds the status strip and slash completion above the
		// composer; the clamp wins over the resting height so the band never
		// pushes the composer off-screen on a very short terminal.
		if lim := m.tx.height - 1; rows > lim {
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
// room .
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
// is the single-pane view; when the rail is visible it is overlaid onto the
// pane's top-right by viewString's surfaceWithRail rather than
// joined to the pane's right, so the full-width bottom band stays edge-to-edge.
// It is used by the alternate-screen renderer, so every frame is a clean
// repaint. The band (status strip + composer) is a Model-owned composer
// concern; the transcript region's render delegates to the owned Transcript
//, which composes the scroll region + band and passes
// the band in below them (see Transcript.renderPane).
func (m Model) renderPane() string {
	var band strings.Builder
	m.renderBand(&band)
	return m.tx.renderPane(band.String())
}

type toolRowRange struct {
	start, end, idx int
}

// msgRowRange maps a rendered history row span to the message that owns it, so
// the transcript exposes a row->message index alongside the
// row->tool-entry index. start/end are content-line indexes in the viewport's
// split space (the same space mouseToContent maps into); idx indexes the
// Transcript-owned messages.
type msgRowRange struct {
	start, end, idx int
}

// transcriptLayout is the persistent layout cache for the history region
//: one batched renderHistory pass captures the row->tool-entry
// mapping (rows), the row->message mapping (msgs), both in content-line
// coordinates, and the ANSI-stripped history rows (plain, the drag-select copy
// space) so the mouse hit-test reads the recorded index instead of re-deriving
// layout on every pointer event. dirty is true when a transcript-affecting
// change makes the cached index stale; the lazy hit-test rebuilds exactly once
// per invalidate. It is owned by the Transcript .
type transcriptLayout struct {
	rows  []toolRowRange // row->tool-entry index in content-line coordinates
	msgs  []msgRowRange  // row->message index in content-line coordinates
	plain []string       // ANSI-stripped history rows (the drag-select space)
	dirty bool
	// builds counts the batched layout passes run. It is a read-only test hook
	//: a regression test asserts a drag's repeated hit-tests
	// build the layout exactly once. Production never inspects it.
	builds int
}

// syncComposerRail recolors the composer's prompt rail by editing state: the
// accent rail signals an editable composer, while a running turn makes the
// composer inert, so the rail dims to a muted accent
// (state-as-color — the mode-colored composer border pattern, benchmark
// §4.3). The rail's glyph and width never change, so the caret geometry
// is untouched.
// adjustRailWidth moves the right context rail by delta columns, clamped to
// minWidthRail, and persists the new width to config. It is the shared helper
// for the Ctrl+Shift+[ / Ctrl+Shift+] keyboard shortcuts.
func (m *Model) adjustRailWidth(delta int) {
	w := m.tx.railWidthOrDefault() + delta
	if w < minWidthRail {
		w = minWidthRail
	}
	m.tx.setRailWidth(w)
	m.persistRailWidth()
	m.syncWidths()
}

// persistRailWidth writes the current rail width into deps.Config and persists
// it via the Save seam so the width round-trips across sessions.
func (m *Model) persistRailWidth() {
	m.deps.Config.RailWidth = m.tx.railWidthOrDefault()
	if m.deps.Save != nil {
		_ = m.deps.Save(m.deps.Config)
	}
}

func (m *Model) syncComposerRail() {
	c := m.tx.theme.accent
	if m.tx.busy {
		c = dimmed(m.tx.theme.accent, 0.45)
	}
	st := m.composer.Styles()
	st.Focused.Prompt = lipgloss.NewStyle().Foreground(c)
	m.composer.SetStyles(st)
}

// renderBand renders the fixed bottom band: the hints-only status row (when
// telemetry is wired; the row carries keybinding hints plus the busy spinner,
// never telemetry numbers — )
// plus the slash-command completion list and the composer, in that order. This
// is the region T02+ pins at the bottom so it never scrolls away on resize.
func (m Model) renderBand(b *strings.Builder) {
	var inner strings.Builder
	// Status row: the bottom band is now the only
	// home of the keybinding hints, since the right rail is the sole,
	// permanent stats surface. The strip renders no telemetry numbers
	// (turns/max, cache gauge, elapsed all live in the rail's STATS
	// section; the cost line was removed in issue #374); it is a clean single
	// line of keybinding hints, with the busy
	// spinner leading while a turn runs so the working state stays glanceable
	// even when the history is scrolled away (the spinner tick drives the
	// re-render). The hints are always shown; no width/collapse threshold cuts
	// into the telemetry anymore (there is none to collapse).
	statusRow := ""
	if m.telemetry != nil {
		// The live working indicator rides the always-visible status row,
		// accent-tinted to match the rail, while a turn runs. The busy/spinner
		// state lives on the owned Transcript .
		if m.tx.busy {
			statusRow = m.tx.theme.bandStatusStyle.Render(busyLine(m.tx.spinner, m.tx.phase())) + "  "
		}
		hints := bandHints()
		if m.tx.busy {
			// ctrl+C stops the running turn; it is a real binding only while a
			// turn runs, so the idle hint set stays unchanged.
			hints += g(" · ", " . ") + "ctrl+c stop"
		}
		statusRow += m.tx.theme.statusStyle.Render(hints)
		// The status strip is edge-to-edge with the rest of the band (issue
		// #232 AC1): pad it to the full band width so it runs under the rail's
		// right column instead of stopping short. The separator and composer
		// already span bandWidth via their own sizing.
		statusRow = lipgloss.NewStyle().Width(m.tx.bandWidth()).Render(statusRow)
		inner.WriteString(statusRow)
		inner.WriteString("\n")
	}
	// The slash-command completion list sits above the composer
	// whenever the input line is a `/...` command, listing the built-in
	// /settings command + matching skills with the current selection marked.
	renderSlashCompletion(&inner, m.tx.theme, m.slashPrefix, m.composer.Value(), m.skills, m.slashIdx)
	inner.WriteString(m.composer.View())
	if m.savedMsg != "" {
		inner.WriteString("\n" + m.tx.theme.statusStyle.Render(m.savedMsg))
	}
	// The whole band is framed by an accent separator row so it reads as one
	// coherent fixed region under the transcript . The
	// separator spans the full band width via bandWidth() — the edge-to-edge
	// seam widened to the full terminal width under the rail.
	tw := m.tx.bandWidth()
	if tw < 2 {
		tw = 2
	}
	b.WriteString(m.tx.theme.bandSeparatorStyle.Render(strings.Repeat(g("─", "-"), tw)))
	b.WriteString("\n")
	b.WriteString(inner.String())
}

// composerCursor returns the composer's hardware caret for the current frame,
// or nil when the composer is not the active editing surface . The
// textarea reports its caret relative to its own top-left cell; the band is
// pinned to the bottom of the frame, so the caret is offset by the rows that
// render above the composer — everything above the band, plus the band's own
// pre-composer rows (separator, status strip, slash completion). content is the
// frame's rendered content, whose line count is the frame height.
func (m Model) composerCursor(content string) *tea.Cursor {
	if m.settings != nil || m.prompting || m.tx.busy {
		// The composer is not the active editing surface, so no caret:
		// Settings and the continuation prompt are full-surface overlays (no
		// composer on screen), and a running turn makes the composer inert;
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
// slash-completion candidate . It mirrors renderBand's ordering so
// the caret lands on the composer's true frame row; savedMsg rows sit below
// the composer and are excluded.
func (m Model) composerPreRows() int {
	n := 1 // accent separator
	if m.telemetry != nil {
		n++
	}
	return n + len(slashCandidates(m.slashPrefix, m.skills))
}

// renderSlashCompletion appends the slash-command completion list to the view
// above the composer: the built-in slash commands plus any
// matching detected skills. It marks the candidate currently in the composer
// (tab-filled or typed) as selected; on a bare prefix it points at the next
// tab-cycling candidate (slashIdx) as a forward hint. It renders nothing for a
// non-slash line or when there are no candidates, so normal typing is
// unaffected .
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
			// completion list reads as part of the coherent band (
			// AC3).
			b.WriteString(th.slashSelectStyle.Render(g("▸ ", "> ") + c))
		} else {
			b.WriteString(th.statusStyle.Render("  " + c))
		}
		b.WriteString("\n")
	}
}

// telemetryWait returns a command that blocks until the next live telemetry
// update arrives on the engine seam channel, then delivers it to the UI loop as
// a telemetryUpdateMsg. The model re-issues it after each update so the strip
// keeps refreshing live, even with no keyboard input. When the
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

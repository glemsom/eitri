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

// Busy-state motion: the static "… thinking" line is the reduced-motion fallback; the default is an animated braille spinner advanced every busySpinnerTick while a turn runs.
const busySpinnerTick = 80 * time.Millisecond

// busySpinnerFrames is the OpenCode-style braille frame set.
var busySpinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// spinnerTickMsg advances the busy spinner by one frame.
type spinnerTickMsg struct{}

// spinnerTick returns the command that delivers the next spinner frame after busySpinnerTick.
func spinnerTick() tea.Cmd {
	return tea.Tick(busySpinnerTick, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// motion gate: animated indicators run unless the user opts out (EITRI_NO_MOTION set) or the locale cannot render braille (non-UTF-8), mirroring the benchmark's reduced-motion + ASCII-fallback requirements (§4.3).
var (
	localeOnce sync.Once
	localeUTF8 bool
)

// localeSupportsUTF8 sniffs the process locale once: an explicit non-UTF-8 locale (LC_ALL/LC_CTYPE/LANG without UTF-8/UTF8) means non-ASCII glyphs and braille would render as tofu, so the surface degrades to ASCII (see glyphs.go and motionEnabled).
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

// Turn runs one agent conversation turn (user prompt -> assistant answer) over the shared engine seam.
type Turn func(ctx context.Context, prompt string, payload string) (TurnResult, error)

// payload carries the slash-activated skill payload into the model's context for a follow-up args turn .
type TurnResult struct {
	Answer    string
	Reasoning string
	Stopped   bool
}

// message is one committed line of the conversation log.
type message struct {
	role              string          // "you" or "eitri"
	content           string          // derived snapshot of the turn's answer text
	reasoning         string          // derived snapshot of the turn's chain-of-thought
	events            []TimelineEvent // arrival-ordered event log the turn's snapshots derive from
	streaming         bool            // true while this assistant reply is still growing from the answer stream
	thinkingRequested bool
	// expansion owns the reasoning block's expansion forces on the ExpansionState
	// seam (issue #469): the whole-block force keyed on reasoningWholeID (the
	// migrated thinkingExpanded / thinkingCollapsed flags) plus a per-fragment
	// force for each interleaved fragment on a turn whose chain-of-thought
	// renders as multiple blocks (issue #449), keyed by the fragment's emission
	// order index. It replaces the scattered thinkingExpanded / thinkingCollapsed
	// / fragmentForces leaf flags; the open/collapsed decision and every toggle
	// read and write through the seam instead.
	expansion ExpansionState
	stopped   bool
}

type turnDoneMsg struct {
	prompt    string
	answer    string
	reasoning string
	err       error
	stopped   bool
}

// telemetryUpdateMsg carries one queued live telemetry update from the engine seam into the UI loop.
type telemetryUpdateMsg struct {
	update TelemetryUpdate
}

// skillDoneMsg reports a slash-command skill activation's result. name is the activated skill; args, when non-empty, carries the trailing `/skillname <args>` remainder that becomes the turn prompt, and a bare `/skillname` falls back to a default prompt so a turn always runs.
type skillDoneMsg struct {
	name    string
	payload string
	args    string
}

// loginCodeMsg reports the human-visible device-flow code for an in-flight `/login` command, plus the channel waiter should block on for the next login event.
type loginCodeMsg struct {
	code LoginCode
	next <-chan tea.Msg
}

// loginDoneMsg reports the result of one built-in `/login` command.
type loginDoneMsg struct {
	cfg config.Config
	err error
}

// discoverDoneMsg reports the outcome of one on-demand provider model discovery started from Settings . provider tags the draft provider the request was issued for so stale results from an earlier draft switch can be dropped.
type discoverDoneMsg struct {
	provider string
	models   []string
	err      error
}

// SkillItem is one detected skill surfaced to the TUI's slash-command surface: its name is what `/skillname` matches and what the `/` completion list offers.
type SkillItem struct {
	Name string
}

// SkillsSurface wires the TUI's slash-command activation to the run's tool layer .
type SkillsSurface struct {
	Items    []SkillItem
	Activate func(ctx context.Context, name string) (string, error)
}

// LoginCode is the human-visible device-flow challenge surfaced by `/login`: the verification URL to open and the short user code to enter there.
type LoginCode struct {
	UserCode        string
	VerificationURI string
}

// Dependencies wires a Model to its environment: the conversation Turn, model discovery + loaded config for the Settings surface, and a persistence seam.
type Dependencies struct {
	Turn                Turn
	WorkspacePath       string
	Models              []string
	DiscoverModels      func(ctx context.Context, cfg config.Config) ([]string, error)
	Config              config.Config
	Save                func(config.Config) error
	SaveBack            func(config.Config)
	Login               func(ctx context.Context, onCode func(LoginCode)) (config.Config, error)
	Skills              *SkillsSurface
	Telemetry           *Telemetry
	Events              *EventFeed
	Rail                *Rail
	ThinkingSuppression func() bool
	Clipboard           func(text string) error
	OSC52Out            io.Writer
	// Splash enables the animated launch splash (matrix rain resolving into the rainbow wordmark); tests default it off so views start settled.
	Splash bool
	// KittyDA1 is the optional live probe for the Kitty graphics DA1 fallback: when TERM_PROGRAM names no known Kitty-graphics terminal, the probe emits the CSI ? u query and reports whether the answer carries the graphics attribute. Tests leave it nil so detection stays environment-free.
	KittyDA1 func() bool
	// TitleOut is where OSC 0 window-title sequences are written; nil defaults to os.Stdout. Tests point it at a buffer.
	TitleOut io.Writer
	// TerminalTitle reports the terminal title current before the splash starts, so the splash can restore it on exit. Nil means the title was empty.
	TerminalTitle func() string
}

// titleOut is where OSC 0 window-title escapes are written: the injected Dependencies.TitleOut when set, else os.Stdout.
func (d Dependencies) titleOut() io.Writer {
	if d.TitleOut != nil {
		return d.TitleOut
	}
	return os.Stdout
}

// previousTerminalTitle resolves the title to restore after the splash: the injected reader when present, else the empty string.
func previousTerminalTitle(d Dependencies) string {
	if d.TerminalTitle == nil {
		return ""
	}
	return d.TerminalTitle()
}

// Model is the Bubble Tea state backing the TUI.
type Model struct {
	composer textarea.Model
	session  *TurnSession
	fold     *Fold
	deps     Dependencies

	tx *Transcript

	settings *SettingsOverlay
	savedMsg string

	continueReq  chan struct{}
	continueResp chan bool
	prompting    bool

	slash *SkillActivation

	telemetry *Telemetry

	events *EventFeed

	clipboard func(text string) error

	splash *Splash // non-nil while the launch splash is playing; nil once it settled or was skipped

	kittyCap bool // the terminal supports the Kitty graphics protocol (see kitty.go)
}

// NewModel builds a bare chat-only model (no Settings surface), the historical default signature.
func NewModel(t Turn) Model {
	return NewModelCfg(Dependencies{Turn: t})
}

// Composer caret style policy: the composer's hardware caret is deliberately a steady (non-blinking) block rather than whatever the textarea or terminal defaults would draw.
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
	comp.SetHeight(minComposerRows)
	comp.SetVirtualCursor(false)
	th := themeFor(d.Config.Theme)
	st := comp.Styles()
	st.Cursor.Shape = composerCaretShape
	st.Cursor.Blink = composerCaretBlink
	st.Cursor.Color = nil
	st.Focused.Prompt = lipgloss.NewStyle().Foreground(th.accent)
	comp.SetStyles(st)

	transcript := &Transcript{
		theme:               th,
		configTheme:         d.Config.Theme,
		workspacePath:       d.WorkspacePath,
		reasoningEffort:     d.Config.ReasoningEffort,
		telemetry:           d.Telemetry,
		rail:                d.Rail,
		railWidth:           d.Config.RailWidth,
		histFollow:          true,
		histViewport:        newHistoryViewport(),
		layout:              transcriptLayout{dirty: true},
		cotExpanded:         !d.Config.CoTCollapsedByDefault,
		toolResultsExpanded: !d.Config.ToolResultsCollapsedByDefault,
	}

	kittyCap := detectKittyGraphics(liveKittyEnv, d.KittyDA1)
	m := Model{
		composer:     comp,
		session:      NewTurnSession(d.Turn),
		deps:         d,
		tx:           transcript,
		continueReq:  make(chan struct{}, 1),
		continueResp: make(chan bool, 1),
		slash:        NewSkillActivation(d),
		telemetry:    d.Telemetry,
		events:       d.Events,
		clipboard:    newClipboard(d),
		splash:       newSplash(d, transcript, kittyCap),
		kittyCap:     kittyCap,
	}
	m.fold = NewFold(m.session)
	m.session.SetThinkingEnabled(d.Config.ThinkingEnabled)
	if !isSupportedTheme(d.Config.Theme) {
		m.savedMsg = fmt.Sprintf("unknown theme %q, using %s", d.Config.Theme, config.DefaultTheme)
	}
	return m
}

// kittyGraphics reports whether the terminal supports the Kitty graphics protocol: every Kitty-gated feature reads this flag instead of probing the environment itself, so a non-Kitty terminal never sees a single Kitty escape sequence.
func (m *Model) kittyGraphics() bool { return m.kittyCap }

// newClipboard returns the clipboard write seam: the injected Dependencies.Clipboard when set, else the atotto/clipboard package default so Ctrl+O and /copy work out of the box.
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

// newHistoryViewport builds the persisted history scroll component (T1 alt-screen pivot, ) as a plain bubbletea/viewport value.
func newHistoryViewport() viewport.Model {
	v := viewport.New()
	v.MouseWheelEnabled = false
	return v
}

// SetTurnSession wires the TurnSession that owns turn start/stop and commits turn completion.
func (m *Model) SetTurnSession(ts *TurnSession) {
	m.session = ts
	m.fold = NewFold(ts)
}

// ContinueHook returns the interactive continuation hook wired to this Model's prompt channels.
func (m Model) ContinueHook() func() bool {
	return func() bool { return internalContinue(m.continueReq, m.continueResp) }
}

// internalContinue signals a continuation prompt and blocks for the user's y/n decision from the main loop.
func internalContinue(req chan struct{}, resp chan bool) bool {
	req <- struct{}{}
	return <-resp
}

// clockTickMsg re-renders the surface once per second so the statusline's live session-elapsed timer advances even with no input or stream activity.
type clockTickMsg struct{}

// clockTick returns the command that delivers the next one-second clock tick.
func clockTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return clockTickMsg{} })
}

// Init returns any startup commands.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.splash != nil {
		cmds = append(cmds, m.splash.Start())
	}
	if m.telemetry != nil {
		cmds = append(cmds, telemetryWait(m.telemetry))
	}
	if m.events != nil {
		cmds = append(cmds, eventWait(m.events))
	}
	cmds = append(cmds, clockTick())
	return tea.Batch(cmds...)
}

// Update handles a UI event and returns the next state plus any commands.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// While the splash owns the screen, every message first lands on the
	// splash module's single Handle entry point: the animation tick advances
	// it and any keypress skips it, both wholly inside the module. Nothing
	// that arrives during the splash reaches the hot path below.
	if m.splash != nil {
		res := m.splash.Handle(msg)
		if res.handled {
			if res.ended {
				m.splash = nil
			}
			return m, res.cmd
		}
	}

	var cmds []tea.Cmd

	m.savedMsg = ""

	select {
	case <-m.continueReq:
		m.prompting = true
	default:
	}

	switch msgi := msg.(type) {
	case telemetryUpdateMsg:
		if m.telemetry == nil {
			return m, nil
		}
		m.telemetry.apply(msgi.update)
		return m, telemetryWait(m.telemetry)

	case eventMsg:
		if m.events == nil {
			return m, nil
		}
		if msgi.update.Stream != nil {
			m.applyStreamDelta(*msgi.update.Stream)
		}
		if msgi.update.Tool != nil {
			m.applyToolUpdate(*msgi.update.Tool)
		}
		return m, eventWait(m.events)

	case tea.WindowSizeMsg:
		m.tx.SetSize(msgi.Width, msgi.Height)
		m.syncWidths()
		return m, nil

	case tea.KeyPressMsg:
		if m.settings != nil {
			return m.updateSettings(msgi)
		}
		if m.prompting {
			return m.updatePrompt(msgi)
		}
		switch msgi.String() {
		case "ctrl+c":
			if m.tx.busy {
				m.stopTurn()
				return m, nil
			}
			return m, tea.Quit
		case "esc":
			if m.tx.busy {
				m.stopTurn()
			}
			return m, nil
		case "ctrl+s":
			return m.startSettings()
		case "pgup", "home":
			m.tx.navigateHistory(msgi.String())
			return m, nil
		case "pgdown", "end":
			m.tx.navigateHistory(msgi.String())
			return m, nil
		case "ctrl+b":
			return m, nil
		case "ctrl+d":
			m.syncComposerRail()
			return m, nil
		case "ctrl+o":
			m.copyTranscript()
			return m, nil
		case "ctrl+e":
			m.tx.toggleExpandAll()
			return m, nil
		case "e":
			if m.composer.Value() != "" {
				break // composing: the letter goes to the textarea
			}
			m.tx.setExpandAll(true)
			return m, nil
		case "E":
			if m.composer.Value() != "" {
				break // composing: the letter goes to the textarea
			}
			m.tx.setCollapseAll(true)
			return m, nil
		case "ctrl+j", "shift+enter":
			if m.tx.busy {
				return m, nil
			}
			m.composer.InsertString("\n")
			m.syncComposerHeight()
			return m, nil
		case "enter":
			prompt := strings.TrimSpace(m.composer.Value())
			if prompt == "" {
				m.tx.toggleFocused() // empty composer: Enter toggles the focused block (works while busy too)
				return m, nil
			}
			if m.tx.busy {
				return m, nil
			}
			m.tx.histFollow = true
			m.composer.Reset()
			m.syncComposerHeight()
			m.slash.Reset()
			if prompt == "/settings" {
				return m.startSettings()
			}
			if prompt == "/copy" {
				m.copyTranscript()
				return m, nil
			}
			if prompt == "/login" {
				return m.startLogin()
			}
			if prompt == "/help" {
				m.tx.appendMsg(helpView())
				return m, nil
			}
			if name, args, ok := m.slash.Command(prompt); ok {
				return m, m.slash.Activate(m.tx, m.deps.Skills, name, args)
			}
			cmd := m.startTurn(prompt, "")
			return m, cmd
		case "tab":
			if m.composer.Value() != "" && strings.HasPrefix(m.composer.Value(), "/") && len(m.slash.Candidates(m.composer.Value())) > 0 {
				m.completeSlashCommand()
				return m, nil
			}
			if m.composer.Value() == "" {
				m.tx.focusNext() // empty composer: Tab cycles the block focus
				return m, nil
			}
			// Non-slash draft: fall through to the textarea, which handles the tab.
		case "ctrl+x", "ctrl+shift+[":
			m.adjustRailWidth(-2)
			return m, nil
		case "ctrl+z", "ctrl+shift+]":
			m.adjustRailWidth(+2)
			return m, nil
		case "alt+0":
			m.tx.setRailWidth(defaultRailWidth)
			m.persistRailWidth()
			m.syncWidths()
			return m, nil
		case "?":
			if m.composer.Value() == "" && !m.tx.busy {
				m.tx.appendMsg(helpView())
				return m, nil
			}
		}
		nm, cmd := m.composer.Update(msg)
		m.composer = nm
		m.slash.TrackComposer(m.composer.Value())
		m.syncComposerHeight()
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case tea.MouseMsg:
		m.updateMouse(msgi)
		return m, nil

	case turnDoneMsg:
		m.session.Commit(m.tx, msgi)
		m.syncComposerRail()
		return m, nil
	case clockTickMsg:
		return m, clockTick()

	case spinnerTickMsg:
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
		if m.settings == nil {
			return m, nil
		}
		res := m.settings.Handle(msgi)
		if res.handled {
			m.deps.Models = append([]string(nil), res.models...)
		}
		return m, nil

	case skillDoneMsg:
		// The skill payload is injected into the turn's context (single delivery) instead of being
		// echoed as an assistant note, and every slash activation starts an agent turn.
		cmd := m.startTurn(m.slash.TurnPrompt(msgi.name, msgi.args), msgi.payload)
		return m, cmd

	case loginCodeMsg:
		m.tx.appendMsg(fmt.Sprintf("Open %s and enter code: %s", msgi.code.VerificationURI, msgi.code.UserCode))
		return m, loginWait(msgi.next)

	case loginDoneMsg:
		if msgi.err != nil {
			m.tx.appendMsg(failurePrefix() + msgi.err.Error())
			return m, nil
		}
		m.deps.Config = msgi.cfg
		if m.deps.SaveBack != nil {
			m.deps.SaveBack(msgi.cfg)
		}
		m.tx.appendMsg("login saved")
		return m, nil
	}

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

// startSettings opens the Settings surface and returns the command to run.
func (m Model) startSettings() (tea.Model, tea.Cmd) {
	o, cmd := openSettingsOverlay(m.deps.Config, m.deps.Models, m.tx.theme, m.telemetry, m.deps.ThinkingSuppression, m.deps)
	m.settings = o
	return m, cmd
}

// updateSettings routes one message through the open Settings overlay and applies its outcome.
func (m Model) updateSettings(msgi tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	res := m.settings.Handle(msgi)
	switch res.outcome {
	case outcomeClosed:
		m.settings = nil
	case outcomeSaved:
		m.savedMsg = res.status
		m.deps.Config = *res.saved
		m.tx.applySettings(*res.saved)
		m.settings = nil
	}
	return m, res.cmd
}

// startTurn begins the turn through the session, which owns all of turn start. The live merged event feed is re-armed here, and the spinner tick starts so the busy indicator animates.
func (m *Model) startTurn(prompt string, payload string) tea.Cmd {
	cmd := m.session.Begin(m.tx, prompt, payload)
	m.syncComposerRail()
	if m.events != nil {
		return tea.Batch(cmd, eventWait(m.events), spinnerTick())
	}
	return cmd
}

// stopTurn cancels the in-flight turn through the session.
func (m *Model) stopTurn() { m.session.Stop() }

// appendStreamDelta folds one streamed delta through the Fold, the sole
// writer of the streaming assistant message and live timeline.
func (m *Model) appendStreamDelta(kind StreamKind, delta string) {
	m.fold.Stream(m.tx, kind, delta)
}

// applyStreamDelta folds one streamed delta from the merged event feed into the live turn; deltas arriving while no turn runs are dropped, matching the pre-timeline stream behavior.
func (m *Model) applyStreamDelta(u StreamUpdate) {
	if !m.tx.busy {
		return
	}
	m.appendStreamDelta(u.Kind, u.Delta) // the Fold invalidates the layout itself
}

// applyToolUpdate folds one tool observation from the merged event feed through the Fold, arming the tool-start pulse for thinking-off turns along the way.
func (m *Model) applyToolUpdate(u ToolUpdate) {
	m.fold.Tool(m.tx, u) // tool updates route through the Fold
	if u.Start != nil && !m.session.ThinkingEnabled() && motionEnabled() {
		m.tx.busyPulse = 3
	}
}

// copyTranscript copies the plain-text transcript to the system clipboard through the injected seam: Ctrl+O and /copy both route here.
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

// transcriptText renders the conversation log as plain text for clipboard copy : role-marked user prompts and assistant answers, per-turn reasoning blocks, and the interleaved tool-call entries (compact one-liner plus full result when complete) — all ANSI-free so the pasted session is clean.
func (m Model) transcriptText() string {
	var b strings.Builder
	for i, msg := range m.tx.messages {
		if msg.role != "you" && msg.thinkingRequested && msg.reasoning != "" {
			b.WriteString("🤔 " + msg.reasoning + "\n")
		}
		if msg.role == "you" {
			b.WriteString("you: " + msg.content + "\n")
		} else {
			b.WriteString("eitri: " + msg.content + "\n")
		}
		b.WriteString(clipboardToolText(m.tx.log, i))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// clipboardToolText renders every entry anchored to the given message as plain text for the clipboard transcript: the tool head plus the indented full result when complete. It reads the log through its data accessors; the log itself never renders.
func clipboardToolText(l toolLog, anchor int) string {
	var b strings.Builder
	for _, idx := range l.anchoredIndices(anchor) {
		te := l.Entry(idx)
		b.WriteString(toolEntryHead(te))
		b.WriteString("\n")
		if te.complete && te.result != "" {
			b.WriteString("  " + strings.ReplaceAll(strings.TrimRight(te.result, "\n"), "\n", "\n  ") + "\n")
		}
	}
	return b.String()
}

// startLogin runs the built-in `/login` command through the interactive login seam and renders its code/result as assistant notes.
func (m Model) startLogin() (tea.Model, tea.Cmd) {
	m.tx.appendUserMsg("/login")
	if m.deps.Login == nil {
		m.tx.appendMsg(failurePrefix() + "no login flow available")
		return m, nil
	}
	return m, loginCmd(m.deps.Login)
}

// discoverCmd runs one on-demand provider model discovery off the main loop and reports its result .
func discoverCmd(discover func(ctx context.Context, cfg config.Config) ([]string, error), cfg config.Config) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		models, err := discover(ctx, cfg)
		return discoverDoneMsg{provider: cfg.Provider, models: models, err: err}
	})
}

// loginCmd runs the interactive login seam off the main loop.
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

// loginWait blocks for the next in-flight login event, returning nil once the login goroutine has finished and closed the channel.
func loginWait(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// completeSlashCommand delegates the completion cycle to the SkillActivation module, applying the chosen candidate to the composer.
func (m *Model) completeSlashCommand() {
	m.slash.Complete(func(candidate string) {
		m.composer.SetValue(candidate)
		m.composer.SetCursorColumn(len(candidate))
		m.syncComposerHeight()
	})
}

// View renders the conversation plus composer as a tea.View (bubbletea v2).
func (m Model) View() tea.View {
	content := m.viewString()
	v := tea.NewView(content)
	v.AltScreen = true
	// Cell-motion mode turns on SGR mouse reporting so wheel events (scroll)
	// and click-drag (selection) reach updateMouse; without it bubbletea v2
	// defaults to MouseModeNone and no mouse input is delivered, even though
	// the navigateMouse handlers exist.
	v.MouseMode = tea.MouseModeCellMotion
	v.Cursor = m.composerCursor(content)
	return v
}

// viewString renders the surface content string (the tea.View content).
func (m Model) viewString() string {
	if m.splash != nil {
		return m.splash.View(m.tx.width, m.tx.height)
	}
	if m.settings != nil {
		return m.settings.View()
	}
	if m.prompting {
		return promptView(m.tx.theme)
	}

	return m.tx.viewWithRail(m.renderPane(), m.bandHeight())
}

// minComposerRows is how tall the composer rests when the draft is empty, so the input field reads as a multi-line composer rather than a single-line prompt.
const minComposerRows = 2

// maxComposerRows is how tall the composer may grow inside the fixed bottom band before it scrolls internally: a long draft never spills into the transcript — the textarea's own viewport scrolls past this bound, and the band stays pinned while the history viewport yields rows.
const maxComposerRows = 8

// syncComposerHeight grows the composer with its draft up to maxComposerRows, then lets the textarea scroll internally: an empty draft rests at minComposerRows, each new line adds a row up to the bound, and beyond it the composer's internal viewport scrolls so the band never grows past the bound.
func (m *Model) syncComposerHeight() {
	rows := composerContentRows(m.composer)
	if rows > maxComposerRows {
		rows = maxComposerRows
	}
	if rows < minComposerRows {
		rows = minComposerRows
	}
	if m.tx.height > 0 {
		if lim := m.tx.height - 1; rows > lim {
			rows = lim
		}
	}
	if rows < 1 {
		rows = 1
	}
	m.composer.SetHeight(rows)
}

// composerContentRows estimates how many terminal rows the composer's current value occupies once word-wrapped at the composer width: one row per hard newline plus soft-wrap continuations, floored at one.
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

// bandHeight returns how many terminal rows the fixed bottom band (status strip, slash completion, composer) occupies, so the scroll region and the right rail can clamp to the rows it leaves behind.
func (m Model) bandHeight() int {
	var band strings.Builder
	m.renderBand(&band)
	return lineCount(band.String())
}

// renderPane renders the transcript + composer surface into the left pane.
func (m Model) renderPane() string {
	var band strings.Builder
	m.renderBand(&band)
	return m.tx.renderPane(band.String())
}

type toolRowRange struct {
	start, end, idx int
}

// msgRowRange maps a rendered history row span to the message that owns it, so the transcript exposes a row->message index alongside the row->tool-entry index. start/end are content-line indexes in the viewport's split space (the same space mouseToContent maps into); idx indexes the Transcript-owned messages.
type msgRowRange struct {
	start, end, idx int
}

// transcriptLayout is the persistent layout cache for the history region : one batched renderHistory pass captures the row->tool-entry mapping (rows), the row->message mapping (msgs), both in content-line coordinates, and the ANSI-stripped history rows (plain, the drag-select copy space) so the mouse hit-test reads the recorded index instead of re-deriving layout on every pointer event. dirty is true when a transcript-affecting change makes the cached index stale; the lazy hit-test rebuilds exactly once per invalidate.
type transcriptLayout struct {
	rows   []toolRowRange // row->tool-entry index in content-line coordinates
	msgs   []msgRowRange  // row->message index in content-line coordinates
	plain  []string       // ANSI-stripped history rows (the drag-select space)
	dirty  bool
	builds int
}

// syncComposerRail recolors the composer's prompt rail by editing state: the accent rail signals an editable composer, while a running turn makes the composer inert, so the rail dims to a muted accent (state-as-color — the mode-colored composer border pattern, benchmark §4.3).
func (m *Model) adjustRailWidth(delta int) {
	w := m.tx.railWidthOrDefault() + delta
	if w < minWidthRail {
		w = minWidthRail
	}
	m.tx.setRailWidth(w)
	m.persistRailWidth()
	m.syncWidths()
}

// persistRailWidth writes the current rail width into deps.Config and persists it via the Save seam so the width round-trips across sessions.
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

// renderBand renders the fixed bottom band: the hints-only status row (when telemetry is wired; the row carries keybinding hints plus the busy spinner, never telemetry numbers — ) plus the slash-command completion list and the composer, in that order.
func (m Model) renderBand(b *strings.Builder) {
	var inner strings.Builder
	statusRow := ""
	if m.telemetry != nil {
		if m.tx.busy {
			statusRow = m.tx.theme.bandStatusStyle.Render(busyLine(m.tx.spinner, m.tx.phase())) + "  "
		}
		hints := bandHints()
		if m.tx.busy {
			hints += g(" · ", " . ") + "ctrl+c stop"
		}
		statusRow += m.tx.theme.statusStyle.Render(hints)
		statusRow = lipgloss.NewStyle().Width(m.tx.bandWidth()).Render(statusRow)
		inner.WriteString(statusRow)
		inner.WriteString("\n")
	}
	m.slash.RenderCompletion(&inner, m.tx.theme, m.composer.Value())
	inner.WriteString(m.composer.View())
	if m.savedMsg != "" {
		inner.WriteString("\n" + m.tx.theme.statusStyle.Render(m.savedMsg))
	}
	tw := m.tx.bandWidth()
	if tw < 2 {
		tw = 2
	}
	b.WriteString(m.tx.theme.bandSeparatorStyle.Render(strings.Repeat(g("─", "-"), tw)))
	b.WriteString("\n")
	b.WriteString(inner.String())
}

// composerCursor returns the composer's hardware caret for the current frame, or nil when the composer is not the active editing surface .
func (m Model) composerCursor(content string) *tea.Cursor {
	if m.settings != nil || m.prompting || m.tx.busy {
		return nil
	}
	cur := m.composer.Cursor()
	if cur == nil {
		return nil
	}
	var band strings.Builder
	m.renderBand(&band)
	pre := m.composerPreRows()
	cur.Y += lineCount(content) - lineCount(band.String()) + pre
	return cur
}

// composerPreRows returns how many band rows render above the composer: the accent separator, the live status strip (when wired), and one row per slash-completion candidate .
func (m Model) composerPreRows() int {
	n := 1 // accent separator
	if m.telemetry != nil {
		n++
	}
	n += m.slash.CandidateCount(m.composer.Value())
	return n
}

// telemetryWait returns a command that blocks until the next live telemetry update arrives on the engine seam channel, then delivers it to the UI loop as a telemetryUpdateMsg.
func telemetryWait(te *Telemetry) tea.Cmd {
	return func() tea.Msg {
		u, ok := <-te.updates
		if !ok {
			return nil
		}
		return telemetryUpdateMsg{update: u}
	}
}

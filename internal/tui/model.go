package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/config"
)

// Turn runs one agent conversation turn (user prompt -> assistant answer) over the shared engine seam.
type Turn func(ctx context.Context, prompt string, payload string) (TurnResult, error)

// TurnResult reports one completed agent turn: the final answer snapshot, its
// chain-of-thought, and whether the user stopped the turn early.
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

	slash   *SkillActivation
	mention *Mention

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
		mention:      NewMention(d.WorkspacePath),
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
			if m.mention.isOpen() {
				m.mention.Reset()
				return m, nil
			}
			if m.tx.busy {
				m.stopTurn()
			}
			return m, nil
		case "up":
			if m.mention.isOpen() {
				m.mention.Move(-1)
				return m, nil
			}
		case "down":
			if m.mention.isOpen() {
				m.mention.Move(1)
				return m, nil
			}
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
			cmd := m.trackComposer()
			m.syncComposerHeight()
			return m, cmd
		case "enter":
			if m.mention.isOpen() {
				return m.selectMention()
			}
			// slash completion selection happens on Tab; Enter submits
			return m.submitPrompt()
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
		cmds = append(cmds, cmd)
		cmds = append(cmds, m.trackComposer())
		m.syncComposerHeight()
		return m, tea.Batch(cmds...)

	case tea.MouseMsg:
		m.updateMouse(msgi)
		return m, nil

	case mentionWalkMsg:
		m.mention.setManifest(msgi.paths)
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

// submitPrompt handles Enter on the composer: an empty draft toggles the focused
// transcript block, a busy turn swallows the key, and a draft routes to the
// built-in slash commands or a slash-activated skill before falling back to a
// plain agent turn.
func (m Model) submitPrompt() (tea.Model, tea.Cmd) {
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
	m.mention.Reset()
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

// trackComposer updates both completion surfaces after a composer mutation: the
// slash surface from the value, and the @ mention surface from the caret position.
// It returns any background command the mention walk requested (e.g. the first
// async workspace manifest read), so the UI loop pages in candidates off the
// main thread.
func (m *Model) trackComposer() tea.Cmd {
	m.slash.TrackComposer(m.composer.Value())
	return m.mention.Track(m.composer.Value(), m.composerByteOffset())
}

// selectMention replaces the tracked @partial with the chosen candidate and
// closes the dropdown; the resulting composer state is re-tracked.
func (m Model) selectMention() (tea.Model, tea.Cmd) {
	value := m.composer.Value()
	next, ok := m.mention.Select(value)
	if ok {
		m.composer.SetValue(next)
		m.composer.SetCursorColumn(len(next))
		m.syncComposerHeight()
	}
	return m, nil
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

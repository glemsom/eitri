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

	settings *settingsForm
	savedMsg string

	continueReq  chan struct{}
	continueResp chan bool
	prompting    bool

	skills []SkillItem

	slashIdx    int
	slashPrefix string

	telemetry *Telemetry

	events *EventFeed

	clipboard func(text string) error

	splash *splashState // non-nil while the launch splash is playing; nil once it settled or was skipped
	// prevTitle is the terminal window title captured before the splash replaced it with the branding title, so splash end can restore it.
	prevTitle string

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

	m := Model{
		composer:     comp,
		session:      NewTurnSession(d.Turn),
		deps:         d,
		tx:           transcript,
		continueReq:  make(chan struct{}, 1),
		continueResp: make(chan bool, 1),
		skills:       skillSnapshot(d),
		telemetry:    d.Telemetry,
		events:       d.Events,
		clipboard:    newClipboard(d),
		splash:       splashFor(d.Splash),
		kittyCap:     detectKittyGraphics(liveKittyEnv, d.KittyDA1),
		prevTitle:    previousTerminalTitle(d),
	}
	if m.splash != nil {
		m.splash.kitty = m.kittyCap
	}
	m.fold = NewFold(m.session)
	m.session.SetThinkingEnabled(d.Config.ThinkingEnabled)
	m.tx.layout.dirty = true
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
		cmds = append(cmds, splashTick(), splashStartCmd(m.deps.titleOut()))
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
		m.tx.width = msgi.Width
		m.tx.height = msgi.Height
		m.syncWidths()
		m.tx.layout.dirty = true // width change re-wraps the transcript rows
		return m, nil

	case splashTickMsg:
		if m.splash == nil || m.tx.hasContent() {
			m.splash = nil
			return m, splashEndCmd(m.deps.titleOut(), m.prevTitle)
		}
		m.splash.advance()
		if m.splash.done() {
			m.splash = nil
			return m, splashEndCmd(m.deps.titleOut(), m.prevTitle)
		}
		return m, splashTick()

	case tea.KeyPressMsg:
		// Any keypress skips the launch splash instantly; the key itself still lands on the composer.
		if m.splash != nil {
			m.splash = nil
			return m, splashEndCmd(m.deps.titleOut(), m.prevTitle)
		}
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
			m.slashIdx = 0
			m.slashPrefix = ""
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
			if name, args, ok := slashCommand(prompt, m.skills); ok {
				return m.activateSkill(name, args)
			}
			cmd := m.startTurn(prompt, "")
			return m, cmd
		case "tab":
			if m.composer.Value() != "" && strings.HasPrefix(m.composer.Value(), "/") && len(slashCandidates(m.composer.Value(), m.skills)) > 0 {
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
		val := m.composer.Value()
		if strings.HasPrefix(val, "/") {
			m.slashPrefix = val
			m.slashIdx = 0
		} else {
			m.slashPrefix = ""
			m.slashIdx = 0
		}
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
		// The skill payload is injected into the turn's context (single delivery) instead of being
		// echoed as an assistant note, and every slash activation starts an agent turn: the trailing
		// args become the prompt, while a bare `/skillname` falls back to a default apply-skill prompt.
		prompt := msgi.args
		if prompt == "" {
			prompt = fmt.Sprintf("apply the %s skill", msgi.name)
		}
		cmd := m.startTurn(prompt, msgi.payload)
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

// openSettings seeds the Settings form from the loaded config + discovery, borrowing the live telemetry for the cache hit-ratio readout (the cost readout was removed in issue #374).
func (m *Model) openSettings() *settingsForm {
	cfg := m.deps.Config
	if cfg.Provider == "" {
		cfg = config.Default()
	}
	sf := newSettingsForm(cfg, m.deps.Models)
	sf.theme = m.tx.theme
	sf.telemetry = m.telemetry
	sf.thinkingSuppression = m.deps.ThinkingSuppression
	m.settings = &sf
	return &sf
}

// startSettings opens the Settings surface and returns the command to run.
func (m Model) startSettings() (tea.Model, tea.Cmd) {
	sf := m.openSettings()
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
		if msgi.String() == "enter" && s.onSave() {
			s.save(&m)
			return m, nil
		}
		if s.field == fieldPaths {
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
	m.deps.Config = cfg
	m.tx.theme = themeFor(cfg.Theme)
	m.tx.configTheme = cfg.Theme
	m.tx.cotExpanded = !cfg.CoTCollapsedByDefault
	m.tx.toolResultsExpanded = !cfg.ToolResultsCollapsedByDefault
	m.tx.layout.dirty = true // the flip can re-wrap the transcript
	m.settings = nil
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

// skillSnapshot captures the detected skills at construction so the slash completion has a stable list even if the Dependencies snapshot is nil or empty.
func skillSnapshot(d Dependencies) []SkillItem {
	if d.Skills != nil {
		return d.Skills.Items
	}
	return nil
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

// slashCommand reports whether prompt is a `/skillname` activation command for a detected skill .
func slashCommand(prompt string, skills []SkillItem) (name, args string, ok bool) {
	if len(skills) == 0 || !strings.HasPrefix(prompt, "/") {
		return "", "", false
	}
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

// activateSkill runs one slash-command activation through the SkillsSurface activation seam (the skill tool) on a detached command; the resulting payload is injected into the follow-up agent turn's context so the model acts on the skill instructions.
func (m Model) activateSkill(name, args string) (tea.Model, tea.Cmd) {
	m.tx.messages = append(m.tx.messages, message{role: "you", content: "/" + name})
	m.tx.layout.dirty = true
	if m.deps.Skills == nil || m.deps.Skills.Activate == nil {
		m.tx.appendMsg(failurePrefix() + "no skill activation available")
		return m, nil
	}
	return m, skillCmd(m.deps.Skills.Activate, name, args)
}

// startLogin runs the built-in `/login` command through the interactive login seam and renders its code/result as assistant notes.
func (m Model) startLogin() (tea.Model, tea.Cmd) {
	m.tx.messages = append(m.tx.messages, message{role: "you", content: "/login"})
	m.tx.layout.dirty = true
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

// skillCmd runs a skill activation off the main loop and reports its payload. name and args ride along
// on skillDoneMsg so the handler can start the agent turn (args as the prompt, or a default prompt for
// a bare `/skillname`) with the payload injected into context.
func skillCmd(activate func(ctx context.Context, name string) (string, error), name, args string) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		payload, err := activate(ctx, name)
		if err != nil {
			return turnDoneMsg{err: fmt.Errorf("activate skill %q: %w", name, err)}
		}
		return skillDoneMsg{name: name, payload: payload, args: args}
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

// slashCandidates returns the ordered slash-command completion candidates for the current composer value: the built-in `/settings`, `/copy`, and `/login` commands first, then every detected skill whose name starts with the `/...` partial.
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
		if it.Name == "settings" {
			continue
		}
		if strings.HasPrefix(it.Name, partial) {
			cands = append(cands, "/"+it.Name)
		}
	}
	return cands
}

// completeSlashCommand fills the composer with the next slash-command completion candidate, cycling deterministicly through the built-in commands and matching detected skills .
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
	m.slashIdx = (m.slashIdx + 1) % len(cands)
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
		return renderSplash(m.splash, m.tx.width, m.tx.height)
	}
	if m.settings != nil {
		return settingsView(*m.settings)
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
	renderSlashCompletion(&inner, m.tx.theme, m.slashPrefix, m.composer.Value(), m.skills, m.slashIdx)
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
	return n + len(slashCandidates(m.slashPrefix, m.skills))
}

// renderSlashCompletion appends the slash-command completion list to the view above the composer: the built-in slash commands plus any matching detected skills.
func renderSlashCompletion(b *strings.Builder, th Theme, value string, cur string, skills []SkillItem, selected int) {
	cands := slashCandidates(value, skills)
	if len(cands) == 0 {
		return
	}
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
			b.WriteString(th.slashSelectStyle.Render(g("▸ ", "> ") + c))
		} else {
			b.WriteString(th.statusStyle.Render("  " + c))
		}
		b.WriteString("\n")
	}
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

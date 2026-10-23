package tui

import (
	"context"
	"errors"
	"fmt"
	"github.com/glemsom/eitri/internal/tui/livekey"
	"github.com/glemsom/eitri/internal/tui/pulse"
	"github.com/glemsom/eitri/internal/tui/telemetry"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/session"
)

// defaultPromptHistoryCap caps how many submitted prompts the Model's
// in-memory prompt-history ring retains.
const defaultPromptHistoryCap = 100

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
	// seam: the whole-block force keyed on reasoningWholeID (the migrated
	// thinkingExpanded / thinkingCollapsed flags) plus a per-fragment force for
	// each interleaved reasoning fragment, keyed by its emission order index. It
	// replaces the scattered thinkingExpanded / thinkingCollapsed / fragmentForces
	// leaf flags; every expansion decision and toggle reads and writes through the
	// seam instead.
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

type telemetryUpdateMsg struct {
	update telemetry.TelemetryUpdate
}

// skillDoneMsg reports a slash-command skill activation's result. name is the activated skill; args, when non-empty, carries the trailing `/skillname <args>` remainder that becomes the turn prompt, and a bare `/skillname` falls back to a default prompt so a turn always runs.
type skillDoneMsg struct {
	name    string
	payload string
	args    string
	err     error
	seq     int
}

// loginCodeMsg reports the human-visible device-flow code for an in-flight `/login` command, plus the channel waiter should block on for the next login event.
type loginCodeMsg struct {
	code LoginCode
	next <-chan tea.Msg
}

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
	// Skipped names packs whose SKILL.md failed to parse and so were dropped
	// from the surface; the TUI surfaces them as a warning badge so a lenient
	// discovery drop is never silent.
	Skipped []string
}

// Dependencies wires a Model to its environment: the conversation Turn, model discovery + loaded config for the Settings surface, and a persistence seam.
type Dependencies struct {
	Turn                func(context.Context, string, string) (TurnResult, error)
	WorkspacePath       string
	Models              []string
	DiscoverModels      func(ctx context.Context, cfg config.Config) ([]string, error)
	Config              config.Config
	Save                func(config.Config) error
	SaveBack            func(config.Config)
	Login               func(ctx context.Context, onCode func(LoginCode)) (config.Config, error)
	Skills              *SkillsSurface
	Telemetry           *telemetry.Telemetry
	Events              *EventFeed
	Rail                *Rail
	ThinkingSuppression func() bool
	Clipboard           func(text string) error
	OpenURL             func(context.Context, string) error
	OSC52Out            io.Writer
	// HistoryPath is the path of the prompt-history file submitted prompts
	// persist to (a sibling of config.json in the data directory); empty leaves
	// the ring in-memory only.
	HistoryPath string
	// LiveKey is the shared mutable session key the engine-turn seam and the
	// rail read per turn; `/new` mints a fresh GUID onto it so the next turn
	// opens a clean engine session history while the old GUID's on-disk session
	// stays orphaned and auditable.
	LiveKey *livekey.LiveSessionKey
	// NewGUID mints a fresh session GUID string for `/new`; nil falls back to the
	// session package's random hex mint.
	NewGUID func() string
	// SessionCleared is called with the old session key when `/new` re-mints,
	// so the caller can drop any cached state tied to that session (e.g.
	// the engine's in-memory session history).
	SessionCleared func(string)
	// NeedsSetup is true when the selected provider has no usable credentials
	// at boot; the TUI opens Settings automatically so the user can configure
	// them instead of exiting with an error.
	NeedsSetup bool
	// Tick is the timer schedule the model and its pulse consult for every
	// animation tick (clock, face draw, spinner, idle ember); nil uses the
	// production real-timer default (`defaultTick`).
	Tick pulse.TickFunc
}

type feedbackKind int

const (
	feedbackNeutral feedbackKind = iota
	feedbackSuccess
	feedbackFailure
)

type composerFeedback struct {
	text string
	kind feedbackKind
}

func neutralFeedback(text string) composerFeedback {
	return composerFeedback{text: text, kind: feedbackNeutral}
}
func successFeedback(text string) composerFeedback {
	return composerFeedback{text: text, kind: feedbackSuccess}
}
func failureFeedback(text string) composerFeedback {
	return composerFeedback{text: text, kind: feedbackFailure}
}

// Model is the Bubble Tea state backing the TUI.
type Model struct {
	composer textarea.Model
	deps     Dependencies
	tx       *Transcript

	// liveKey is the shared mutable session key wired via Dependencies; `/new`
	// re-mints it to a fresh GUID on confirm.
	liveKey *livekey.LiveSessionKey

	settings *SettingsOverlay
	help     *HelpOverlay
	feedback composerFeedback

	continueReq  chan struct{}
	continueResp chan bool
	prompting    bool

	slash        *SkillActivation
	mention      *Mention
	skillPending bool
	skillCancel  context.CancelFunc
	skillSeq     int

	telemetry *telemetry.Telemetry

	runtime *TurnRuntime

	// pendingDone is a completion held until feed events already removed by a
	// waiter have been projected into the live turn.
	pendingDone *turnDoneMsg

	clipboard func(text string) error

	// history is the Model-owned in-memory ring of submitted user prompts that
	// arrow-key recall reads from; it survives a `/new` because it lives on the
	// Model, not the transcript or runtime.
	history *PromptHistory

	// histIdx is the arrow-recall cursor into the history ring, or -1 while no
	// prompt is being recalled. histDraft pins the composer draft that recall
	// displaced (the node `down` returns to), or the empty string when recall
	// started from an empty draft.
	histIdx   int
	histDraft string

	// faceDirty marks the kitty face as needing (re)upload. It is set only by
	// damage that actually moves or resizes the face (boot/terminal resize,
	// rail-width change, theme change, live turn events) and cleared by the
	// upload attempt itself, so an idle model uploads the face once and then
	// stops: the 50 ms faceDrawTick loop only runs while dirty.
	faceDirty bool

	// tick is the timer schedule the clock and face-draw loops arm on, shared
	// with the transcript's pulse so every animation tick reads one schedule.
	tick pulse.TickFunc
}

func newModelHistory(path string) *PromptHistory {
	if path == "" {
		return NewPromptHistory(defaultPromptHistoryCap)
	}
	return NewPersistedPromptHistory(defaultPromptHistoryCap, path)
}

func NewModelCfg(d Dependencies) Model {
	comp := textarea.New()
	comp.Prompt = lookup("cursor") + " "
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

	tick := d.Tick
	if tick == nil {
		tick = defaultTick
	}

	transcript := NewTranscript(TranscriptConfig{
		Theme:                         d.Config.Theme,
		ReasoningEffort:               d.Config.ReasoningEffort,
		Telemetry:                     d.Telemetry,
		Rail:                          d.Rail,
		RailWidth:                     d.Config.RailWidth,
		Tick:                          tick,
		CoTCollapsedByDefault:         d.Config.CoTCollapsedByDefault,
		ToolResultsCollapsedByDefault: d.Config.ToolResultsCollapsedByDefault,
	})

	m := Model{
		composer:     comp,
		deps:         d,
		tick:         tick,
		tx:           transcript,
		continueReq:  make(chan struct{}, 1),
		continueResp: make(chan bool, 1),
		slash:        NewSkillActivation(d),
		mention:      NewMention(d.WorkspacePath),
		telemetry:    d.Telemetry,
		liveKey:      d.LiveKey,
		clipboard:    newClipboard(d),
		history:      newModelHistory(d.HistoryPath),
		histIdx:      -1,
	}

	m.runtime = NewTurnRuntime(NewTurnSession(d.Turn), d.Events)
	m.runtime.SetTranscript(transcript)
	m.runtime.SetThinkingEnabled(d.Config.ThinkingEnabled)
	if !isSupportedTheme(d.Config.Theme) {
		m.feedback = neutralFeedback(fmt.Sprintf("unknown theme %q, using %s", d.Config.Theme, config.DefaultTheme))
	}
	if d.NeedsSetup {
		m.tx.AppendAssistantMsg("Welcome to Eitri! Please configure your provider in Settings to get started.")
		if o, _ := openSettingsOverlay(d.Config, d.Models, m.tx.Theme(), m.deps.ThinkingSuppression, m.deps); o != nil {
			m.settings = o
		}
	}
	return m
}

func newHistoryViewport() viewport.Model {
	v := viewport.New()
	v.MouseWheelEnabled = false
	return v
}

func (m *Model) SetTurnSession(ts *TurnSession) {
	m.runtime.SetSession(ts)
}

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
type faceDrawMsg struct{}

// clockTickInterval is the statusline's elapsed-timer cadence; faceDrawTickInterval
// is the kitty face's redraw cadence while it carries unrendered damage.
const (
	clockTickInterval    = time.Second
	faceDrawTickInterval = 50 * time.Millisecond
)

// defaultTick is the timer schedule the model and its pulse use when
// Dependencies.Tick is not injected. Production uses real tea.Tick timers;
// tests swap it for an immediate schedule so a submitted turn never sleeps.
var defaultTick pulse.TickFunc = pulse.RealTick

func (m Model) clockTick() tea.Cmd {
	return m.tick(clockTickInterval, func(time.Time) tea.Msg { return clockTickMsg{} })
}

func (m Model) faceDrawTick() tea.Cmd {
	return m.tick(faceDrawTickInterval, func(time.Time) tea.Msg { return faceDrawMsg{} })
}

func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if cmd := m.queueFaceDrawCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if m.telemetry != nil {
		cmds = append(cmds, telemetryWait(m.telemetry))
	}
	if m.runtime.HasEvents() {
		cmds = append(cmds, m.runtime.Wait())
	}
	if cmd := m.armIdleEmber(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	cmds = append(cmds, m.clockTick())
	return tea.Batch(cmds...)
}

func normalizeShiftPrintable(msg tea.KeyPressMsg) tea.KeyPressMsg {
	if msg.Text == "" && msg.Mod == tea.ModShift && msg.Code >= 'a' && msg.Code <= 'z' {
		msg.Text = strings.ToUpper(string(rune(msg.Code)))
		msg.Code = tea.KeyExtended
		msg.Mod = 0
	}
	return msg
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	m.feedback = composerFeedback{}

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
		m.telemetry.Apply(msgi.update)
		return m, telemetryWait(m.telemetry)

	case eventMsg:
		next, cmd := m.applyEvent(msgi.update)
		m = next.(Model)
		if msgi.feed != nil {
			msgi.feed.projected()
		}
		if m.pendingDone != nil && !m.deps.Events.Outstanding() {
			done := *m.pendingDone
			m.pendingDone = nil
			m.runtime.Commit(done)
			m.syncComposerRail()
			return m, tea.Batch(cmd, m.armIdleEmber())
		}
		return m, cmd

	case tea.WindowSizeMsg:
		m.tx.SetSize(msgi.Width, msgi.Height)
		m.syncWidths()
		// Any size change moves the face on screen; the boot-time resize is what
		// triggers the single idle upload.
		m.faceDirty = true
		if m.settings != nil {
			next, cmd := m.updateSettings(msgi)
			return next, cmd
		}
		if m.help != nil {
			return m.updateHelp(msgi)
		}
		return m, m.queueFaceDrawCmd()

	case tea.KeyPressMsg:
		msgi = normalizeShiftPrintable(msgi)
		if m.settings != nil {
			return m.updateSettings(msgi)
		}
		if m.help != nil {
			return m.updateHelp(msgi)
		}
		if m.prompting {
			return m.updatePrompt(msgi)
		}
		switch msgi.String() {
		case "ctrl+c":
			if m.tx.Status().Busy {
				m.stopTurn()
				return m, nil
			}
			return m, tea.Quit
		case "esc":
			if m.mention.isOpen() {
				m.mention.Reset()
				return m, nil
			}
			if m.slash.isOpen() {
				m.slash.Dismiss()
				return m, nil
			}
			m.endRecall()
			if m.tx.Status().Busy {
				m.stopTurn()
			}
			return m, nil
		case "up":
			if m.mention.isOpen() {
				m.mention.Move(-1)
				return m, nil
			}
			if m.slash.isOpen() {
				m.slash.Move(-1)
				return m, nil
			}
			var recalled bool
			m, recalled = m.handleArrowRecall(-1)
			if recalled {
				return m, nil
			}
		case "down":
			if m.mention.isOpen() {
				m.mention.Move(1)
				return m, nil
			}
			if m.slash.isOpen() {
				m.slash.Move(1)
				return m, nil
			}
			var recalled bool
			m, recalled = m.handleArrowRecall(1)
			if recalled {
				return m, nil
			}
		case "pgup", "home":
			m.tx.NavigateHistory(msgi.String())
			return m, nil
		case "pgdown", "end":
			m.tx.NavigateHistory(msgi.String())
			return m, nil
		case "ctrl+e":
			m.tx.ToggleExpandedMode()
			return m, nil
		case "ctrl+j", "shift+enter":
			if m.tx.Status().Busy {
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
			if m.slash.isOpen() && m.slash.SelectedCandidate() != m.composer.Value() {
				m.completeSlashCommand()
				return m, nil
			}
			return m.submitPrompt()
		case "tab":
			if m.mention.isOpen() {
				return m.selectMention()
			}
			if m.slash.isOpen() {
				m.completeSlashCommand()
				return m, nil
			}
			if strings.TrimSpace(m.composer.Value()) == "" {
				m.tx.FocusNext() // empty composer: Tab cycles the block focus
				return m, nil
			}
			if m.tx.Status().Busy {
				return m, nil // busy: no literal tab insert into the hidden draft
			}
			// Non-slash draft: fall through to the textarea, which handles the tab.
		case "ctrl+x":
			m.adjustRailWidth(-2)
			m, cmd := m.markFaceDamage()
			return m, cmd
		case "ctrl+z":
			m.adjustRailWidth(+2)
			m, cmd := m.markFaceDamage()
			return m, cmd
		}
		// While a turn streams the composer sits behind the forge panel with no
		// caret, so editing it would type into an invisible draft that resurfaces
		// once the turn commits — swallow composer-mutating keys.
		if m.tx.Status().Busy {
			return m, nil
		}
		// Any key not consumed above edits the composer directly, which ends an
		// active arrow recall so a recalled prompt doesn't linger as stale state.
		m.endRecall()
		nm, cmd := m.composer.Update(msgi)
		m.composer = nm
		cmds = append(cmds, cmd)
		cmds = append(cmds, m.trackComposer())
		m.syncComposerHeight()
		cmds = append(cmds, m.armIdleEmber())
		return m, tea.Batch(cmds...)

	case tea.PasteMsg:
		if m.settings != nil {
			return m.updateSettings(msgi)
		}
		// A busy turn, help overlay, or continuation prompt owns input instead
		// of the hidden composer, so discard bracketed paste just like key edits.
		if m.tx.Status().Busy || m.help != nil || m.prompting {
			return m, nil
		}
		m.endRecall()
		msgi.Content = sanitizeTerminalText(msgi.Content)
		nm, cmd := m.composer.Update(msgi)
		m.composer = nm
		cmds = append(cmds, cmd)
		cmds = append(cmds, m.trackComposer())
		m.syncComposerHeight()
		cmds = append(cmds, m.armIdleEmber())
		return m, tea.Batch(cmds...)

	case tea.MouseMsg:
		if m.settings != nil || m.help != nil {
			return m, nil
		}
		return m, m.updateMouse(msgi)

	case mentionWalkMsg:
		m.mention.setManifest(msgi.paths)
		return m, nil

	case turnDoneMsg:
		if m.runtime.HasEvents() && m.deps.Events.Outstanding() {
			m.pendingDone = &msgi
			return m, nil
		}
		m.runtime.Commit(msgi)
		m.syncComposerRail()
		return m, m.armIdleEmber()
	case clockTickMsg:
		return m, m.clockTick()

	case faceDrawMsg:
		m, cmd := m.drawFaceCmd()
		return m, cmd

	case pulse.SpinnerTickMsg:
		return m, m.tx.TickBusy()

	case pulse.IdleEmberTickMsg:
		return m, m.tx.TickEmber(m.overlayOpen())

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
		if msgi.seq != 0 && (!m.skillPending || msgi.seq != m.skillSeq) {
			return m, nil
		}
		m.skillPending = false
		m.skillCancel = nil
		if msgi.err != nil {
			m.tx.EndTurn()
			if !errors.Is(msgi.err, context.Canceled) {
				m.tx.AppendAssistantMsg(failurePrefix() + fmt.Errorf("activate skill %q: %w", msgi.name, msgi.err).Error())
			}
			m.syncComposerRail()
			return m, nil
		}
		// The skill payload is injected into the turn's context (single delivery) instead of being
		// echoed as an assistant note, and every slash activation starts an agent turn.
		cmd := m.startTurn(m.slash.TurnPrompt(msgi.name, msgi.args), msgi.payload)
		return m, cmd

	case loginCodeMsg:
		m.tx.AppendAssistantMsg(fmt.Sprintf("Open %s and enter code: %s", msgi.code.VerificationURI, msgi.code.UserCode))
		return m, loginWait(msgi.next)

	case loginDoneMsg:
		if msgi.err != nil {
			m.tx.AppendAssistantMsg(failurePrefix() + msgi.err.Error())
			return m, nil
		}
		m.deps.Config = msgi.cfg
		if m.deps.SaveBack != nil {
			m.deps.SaveBack(msgi.cfg)
		}
		m.tx.AppendAssistantMsg("login saved")
		return m, nil
	}

	if m.settings != nil {
		return m.updateSettings(msg)
	}
	if m.help != nil {
		return m.updateHelp(msg)
	}

	nm, cmd := m.composer.Update(msg)
	m.composer = nm
	return m, cmd
}

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

// mintNewSession re-keys the live session to a fresh GUID and resets the
// transcript, so the next turn starts with empty engine session history while
// the older GUID's on-disk session and engine history stay orphaned (auditable,
// no pruning).
func (m *Model) mintNewSession() {
	oldKey := ""
	if m.liveKey != nil {
		oldKey = m.liveKey.Get()
	}
	if m.deps.SessionCleared != nil {
		m.deps.SessionCleared(oldKey)
	}
	if m.liveKey == nil {
		m.liveKey = livekey.NewLiveSessionKey(m.newGUID())
	} else {
		m.liveKey.Set(m.newGUID())
	}
	m.tx.Reset()
	if m.telemetry != nil {
		m.telemetry.Reset()
	}
}

func (m Model) newGUID() string {
	if m.deps.NewGUID != nil {
		return m.deps.NewGUID()
	}
	guid, err := session.NewGUID()
	if err != nil {
		// Fall back so `/new` can never wedge the session on a random-source
		// failure; the transcript reset still gives a fresh view.
		if m.liveKey != nil {
			guid = m.liveKey.Get()
		}
	}

	return guid
}

// submitPrompt handles Enter on the composer: an empty draft toggles the focused
// transcript block, a busy turn swallows the key, and a draft routes to the
// built-in slash commands or a slash-activated skill before falling back to a
// plain agent turn.
func (m Model) submitPrompt() (tea.Model, tea.Cmd) {
	prompt := strings.TrimSpace(m.composer.Value())
	if prompt == "" {
		m.tx.ToggleFocused() // empty composer: Enter toggles the focused block (works while busy too)
		return m, nil
	}
	if m.tx.Status().Busy {
		return m, nil
	}
	m.tx.ResumeFollow()
	m.composer.Reset()
	m.syncComposerHeight()
	m.slash.Reset()
	m.mention.Reset()
	m.endRecall()
	if prompt == "/settings" {
		return m.startSettings()
	}
	if prompt == "/login" {
		return m.startLogin()
	}
	if prompt == "/help" {
		return m.startHelp()
	}
	if prompt == "/new" {
		// `/new` is a control slash command: never recorded into the history
		// ring. It immediately mints a fresh live session key and clears the
		// transcript and the live stats — the point of `/new` is a clean
		// session, so there is no confirmation. It is blocked while a turn
		// streams, a skill is pending, or the settings overlay is open.
		if m.tx.Status().Busy || m.skillPending || m.settings != nil {
			return m, nil
		}
		m.mintNewSession()
		return m, nil
	}

	if name, args, ok := m.slash.Command(prompt); ok {
		m.history.Push(prompt) // a `/skill ...` activation is recorded as its full line
		return m.startSkillActivation(name, args)
	}
	m.history.Push(prompt)
	cmd := m.startTurn(prompt, "")
	return m, cmd
}

// startTurn begins the turn through TurnRuntime, which owns live event
// acceptance for the turn: re-arming the run ID, draining the merged event
// feed, and starting the spinner tick so the busy indicator animates.
func (m *Model) startTurn(prompt string, payload string) tea.Cmd {
	m.tx.SettleIdleEmber()
	cmd := m.runtime.Begin(prompt, payload)
	m.syncComposerRail()
	return cmd
}

// handleArrowRecall implements readline-style prompt recall: `up`/`down` move
// a prior/following prompt into the draft only while the caret rests on the
// top/bottom line, else the key falls through to the textarea caret motion, so
// in-draft navigation and the shift variants are untouched. Recall is
// suppressed while a turn streams or a completion surface is open, and a
// recalled `/skill ...` line stays inert until Enter submits it.
func (m Model) handleArrowRecall(dir int) (Model, bool) {
	if !m.canRecall() {
		return m, false
	}
	entries := m.history.Entries()
	if len(entries) == 0 {
		return m, false
	}

	// A down key outside an active recall never starts one (readline: down is
	// only meaningful once up has armed a recall).
	if dir == 1 && m.histIdx == -1 {
		return m, false
	}

	// An up key from neutral arms the recall, archiving the draft, but only
	// when the caret already sits on the top line. Off that line the up key
	// simply moves the caret instead and is not handled here.
	if m.histIdx == -1 {
		if dir == -1 && m.composer.Line() <= 0 {
			m.histDraft = m.composer.Value()
			m.stepArrowRecall(dir, entries)
		} else {
			return m, false
		}
	} else {
		m.stepArrowRecall(dir, entries)
	}

	m.applyRecall(entries)
	return m, true
}

// stepArrowRecall moves the recall cursor by dir and clamps it to the ring
// bounds. Stepping down off the newest entry ends the recall (histIdx == -1)
// so the archived draft can be restored.
func (m *Model) stepArrowRecall(dir int, entries []string) {
	n := len(entries)
	switch dir {
	case -1:
		if m.histIdx == -1 {
			m.histIdx = n - 1
			return
		}
		if m.histIdx > 0 {
			m.histIdx--
		}
	case 1:
		if m.histIdx+1 >= n {
			m.histIdx = -1
			return
		}
		m.histIdx++
	}
}

// applyRecall writes the entry at the current recall cursor into the draft,
// or restores the archived draft when the recall ended (histIdx == -1). The
// composer is re-tracked so the slash/@ surfaces see the new value.
func (m *Model) applyRecall(entries []string) {
	if m.histIdx == -1 {
		m.composer.SetValue(m.histDraft)
	} else {
		m.composer.SetValue(entries[m.histIdx])
	}
	m.syncComposerHeight()
	m.composer.MoveToEnd()
	// Recalled prompts are inert drafts until edited or submitted. In
	// particular, do not open slash completion for a recalled `/skill` line:
	// doing so would consume the next Up key as completion navigation instead
	// of allowing recall to continue through older prompts.
	m.slash.Reset()
	m.mention.Reset()
}

// canRecall reports whether arrow recall may fire right now: an open
// completion menu or a streaming turn both block it.
func (m Model) canRecall() bool {
	if m.slash.isOpen() || m.mention.isOpen() {
		return false
	}
	return !m.tx.Status().Busy
}

// endRecall drops any active arrow recall, forgetting the archived draft and
// the recall cursor. It is idempotent and safe to call from the submit path and
// any non-arrow key that returns the composer to neutral editing.
func (m *Model) endRecall() {
	m.histIdx = -1
	m.histDraft = ""
}

func (m Model) startSkillActivation(name, args string) (tea.Model, tea.Cmd) {
	m.tx.AppendUserMsg("/" + name)
	if m.deps.Skills == nil || m.deps.Skills.Activate == nil {
		m.tx.AppendAssistantMsg(failurePrefix() + "no skill activation available")
		return m, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.skillSeq++
	m.skillPending = true
	m.skillCancel = cancel
	m.tx.BeginBusy()
	m.syncComposerRail()
	return m, skillCmdWithContext(ctx, m.deps.Skills.Activate, name, args, m.skillSeq)
}

func (m *Model) stopTurn() {
	if m.skillPending {
		if m.skillCancel != nil {
			m.skillCancel()
		}
		m.skillPending = false
		m.skillCancel = nil
		m.tx.EndTurn()
		m.tx.AppendAssistantMsg("[stopped]")
		m.syncComposerRail()
		return
	}
	m.runtime.Stop()
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
// closes the dropdown; the caret stays immediately after the completed token
// on the same line, before any draft tail.
func (m Model) selectMention() (tea.Model, tea.Cmd) {
	value := m.composer.Value()
	if !m.mention.isOpen() || len(m.mention.cands) == 0 {
		return m, nil
	}
	// Capture the tracked span before Select mutates the mention state: a file
	// selection resets it, a folder descend re-bases it on the new partial.
	partial := m.mention.partial
	cand := m.mention.cands[m.mention.idx]
	_, ok := m.mention.Select(value)
	if !ok {
		return m, nil
	}
	insert := cand
	if isFolderCandidate(cand) {
		insert = "@" + cand // folder descend keeps the @ so the dropdown re-opens
	}
	m.deleteComposerSpan(utf8.RuneCountInString(partial) + 1)
	m.composer.InsertString(insert)
	m.syncComposerHeight()
	return m, nil
}

func (m *Model) completeSlashCommand() {
	m.slash.Complete(func(candidate string) {
		// SetValue's insert already trails the caret after the pasted candidate,
		// so no SetCursorColumn fix-up is needed: a byte-length column would
		// overshoot non-ASCII skill names.
		m.composer.SetValue(candidate)
		m.syncComposerHeight()
	})
}

func (m Model) View() tea.View {
	content := m.viewString()
	v := tea.NewView(content)
	v.AltScreen = true
	// Cell-motion mode turns on SGR mouse reporting so wheel events (scroll)
	// and click-drag (selection) reach updateMouse; without it bubbletea v2
	// defaults to MouseModeNone and no mouse input is delivered, even though
	// the NavigateMouse handlers exist.
	v.MouseMode = tea.MouseModeCellMotion
	v.Cursor = m.composerCursor(content)
	return v
}

func (m Model) viewString() string {
	if m.settings != nil {
		return m.settings.View()
	}
	if m.help != nil {
		return m.help.View()
	}
	if m.prompting {
		return promptView(m.tx.Theme())
	}

	bandStr, bandHeight := m.band()
	return m.tx.View(bandStr, bandHeight)
}

func telemetryWait(te *telemetry.Telemetry) tea.Cmd {
	return func() tea.Msg {
		u, ok := <-te.Updates()
		if !ok {
			return nil
		}
		return telemetryUpdateMsg{update: u}
	}
}

func (m Model) queueFaceDrawCmd() tea.Cmd {
	if !m.faceNeedsDraw() {
		return nil
	}
	return m.faceDrawTick()
}

// markFaceDamage records that the face's placement or size may have changed
// and returns the model plus the command that (re)uploads it once the canvas
// is drawable. Dropping the returned command is safe: the flag itself survives
// on the returned model, so a later damage path can still arm the upload.
func (m Model) markFaceDamage() (Model, tea.Cmd) {
	m.faceDirty = true
	return m, m.queueFaceDrawCmd()
}

// faceNeedsDraw reports whether the face carries unrendered damage on a
// canvas that can show it.
func (m Model) faceNeedsDraw() bool {
	return m.faceDirty && m.canDrawFace()
}

// drawFaceCmd uploads the kitty face when damage demands it and the canvas
// exists, returning the model with the face marked clean after the attempt. A
// stray tick with a clean face is a no-op, so an idle model never re-uploads;
// an upload the terminal cannot display (no kitty graphics, unwritable scratch
// PNG) is also final, so the flag never sticks true between damage events.
func (m Model) drawFaceCmd() (Model, tea.Cmd) {
	if !m.faceNeedsDraw() {
		return m, nil
	}
	seq := kittyFaceUpload(m.tx.Geometry().RailWidth)
	m.faceDirty = false
	if seq == "" {
		return m, nil
	}
	return m, tea.Raw(seq)
}

func (m Model) canDrawFace() bool {
	g := m.tx.Geometry()
	if m.settings != nil || m.help != nil || m.prompting || !g.RailVisible || g.Width <= 0 || g.Height <= 0 {
		return false
	}
	_, faceRows := railFaceRows(g.RailWidth)
	bandHeight := m.bandHeight()
	return faceRows > 0 && m.tx.RailMaxHeight(bandHeight) >= m.tx.RailHeight(bandHeight)+railFaceTopGap+faceRows
}

func (m Model) applyEvent(update Event) (tea.Model, tea.Cmd) {
	if !m.runtime.HasEvents() {
		return m, nil
	}
	cmd := m.runtime.Handle(update)
	if update.TurnStart {
		return m, cmd
	}
	// A live turn's deltas can re-layout the rail and move the face's placement
	// under follow, so every event is face damage while the turn streams; once
	// the feed quiets the loop dies with the clean flag.
	m, damageCmd := m.markFaceDamage()
	return m, tea.Batch(cmd, damageCmd, m.armIdleEmber())
}

// overlayOpen reports whether a modal surface owns the screen, so the idle
// ember pauses while Settings, Help, or the max-turns prompt is open.
func (m Model) overlayOpen() bool {
	return m.settings != nil || m.help != nil || m.prompting
}

// armIdleEmber re-opens the bounded ember window on activity and returns the
// tick that drives it.
func (m *Model) armIdleEmber() tea.Cmd {
	return m.tx.ArmEmber(m.overlayOpen())
}

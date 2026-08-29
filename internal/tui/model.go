package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/session"
)

// defaultPromptHistoryCap is how many submitted prompts the Model's in-memory
// history ring retains at most (issue #610, part of #608).
const defaultPromptHistoryCap = 100

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
	err     error
	seq     int
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
	// HistoryPath is the path of the prompt-history file to persist submitted
	// prompts to (a sibling of config.json in the data directory, issue #612,
	// part of #608). Empty leaves the ring in-memory only.
	HistoryPath string
	// LiveKey is the shared mutable session key the engine-turn seam and the rail
	// read per turn (issue #609). `/new` mints a fresh GUID onto it so the next
	// turn opens a clean engine session history while the old GUID's on-disk
	// session stays orphaned and auditable.
	LiveKey *LiveSessionKey
	// NewGUID mints a fresh session GUID string for `/new`; nil falls back to the
	// session package's random hex mint.
	NewGUID func() string
}

// Model is the Bubble Tea state backing the TUI.
type Model struct {
	composer textarea.Model
	session  *TurnSession
	fold     *Fold
	deps     Dependencies
	tx       *Transcript

	// liveKey is the shared mutable session key wired via Dependencies (issue
	// #609); `/new` re-mints it to a fresh GUID on confirm.
	liveKey *LiveSessionKey

	settings *SettingsOverlay
	savedMsg string

	continueReq  chan struct{}
	continueResp chan bool
	prompting    bool

	slash        *SkillActivation
	mention      *Mention
	skillPending bool
	skillCancel  context.CancelFunc
	skillSeq     int

	telemetry *Telemetry

	events    *EventFeed
	liveRunID int

	clipboard func(text string) error

	// history is the Model-owned in-memory ring of submitted user prompts
	// (issue #610, part of #608); it is the data source the arrow-key recall
	// reads from and survives a `/new` because it lives on the Model, not the
	// transcript or session.
	history *PromptHistory

	// histIdx is the arrow-recall cursor into the history ring, or -1 while no
	// prompt is being recalled. histDraft pins the composer draft that recall
	// displaced (the node the `down` key returns to), or the empty string when
	// recall started from an empty composer. See handleArrowRecall (issue #611,
	// part of #608).
	histIdx   int
	histDraft string
}

// NewModel builds a bare chat-only model (no Settings surface), the historical default signature.
// newModelHistory builds the Model's prompt-history ring: file-backed when a
// HistoryPath is wired (loading any persisted entries, issue #612), else a plain
// in-memory ring.
func newModelHistory(path string) *PromptHistory {
	if path == "" {
		return NewPromptHistory(defaultPromptHistoryCap)
	}
	return NewPersistedPromptHistory(defaultPromptHistoryCap, path)
}

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
		liveRunID:    -1,
		liveKey:      d.LiveKey,
		clipboard:    newClipboard(d),
		history:      newModelHistory(d.HistoryPath),
		histIdx:      -1,
	}

	m.fold = NewFold(m.session)
	m.session.SetThinkingEnabled(d.Config.ThinkingEnabled)
	if !isSupportedTheme(d.Config.Theme) {
		m.savedMsg = fmt.Sprintf("unknown theme %q, using %s", d.Config.Theme, config.DefaultTheme)
	}
	return m
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
		if msgi.update.TurnStart {
			m.liveRunID = msgi.update.RunID
			return m, eventWait(m.events)
		}
		if m.acceptEvent(msgi.update) {
			if msgi.update.Stream != nil {
				m.applyStreamDelta(*msgi.update.Stream)
			}
			if msgi.update.Tool != nil {
				m.applyToolUpdate(*msgi.update.Tool)
			}
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
			if m.slash.isOpen() {
				m.slash.Dismiss()
				return m, nil
			}
			m.endRecall()
			if m.tx.busy {
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
		// Any key not consumed above edits the composer directly, which ends an
		// active arrow recall so a recalled prompt doesn't linger as stale state.
		m.endRecall()
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
		if msgi.seq != 0 && (!m.skillPending || msgi.seq != m.skillSeq) {
			return m, nil
		}
		m.skillPending = false
		m.skillCancel = nil
		if msgi.err != nil {
			m.tx.endTurn()
			if !errors.Is(msgi.err, context.Canceled) {
				m.tx.appendMsg(failurePrefix() + fmt.Errorf("activate skill %q: %w", msgi.name, msgi.err).Error())
			}
			m.syncComposerRail()
			return m, nil
		}
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

// updatePrompt handles a keypress on the engine's max-turns continuation
// prompt (issue #613): `y`/enter continues the run, `n`/esc stops it.
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
	if m.liveKey == nil {
		m.liveKey = NewLiveSessionKey(m.newGUID())
	} else {
		m.liveKey.Set(m.newGUID())
	}
	m.tx.Reset()
	if m.telemetry != nil {
		m.telemetry.Reset()
	}
}

// newGUID returns a fresh session GUID for `/new`: the injected Dependencies
// mint when present, else the session package's random hex mint.
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
	m.endRecall()
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
	if prompt == "/new" {
		// `/new` is a control slash command: never recorded into the history
		// ring. It immediately mints a fresh live session key and clears the
		// transcript and the live stats — the point of `/new` is a clean
		// session, so there is no confirmation. It is blocked while a turn
		// streams, a skill is pending, or the settings overlay is open.
		if m.tx.busy || m.skillPending || m.settings != nil {
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

// startTurn begins the turn through the session, which owns all of turn start. The live merged event feed is re-armed here, and the spinner tick starts so the busy indicator animates.
func (m *Model) startTurn(prompt string, payload string) tea.Cmd {
	m.liveRunID = -1
	if m.events != nil {
		m.events.Drain()
	}
	cmd := m.session.Begin(m.tx, prompt, payload)
	m.syncComposerRail()
	if m.events != nil {
		return tea.Batch(cmd, spinnerTick())
	}
	return cmd
}

// handleArrowRecall implements readline-style prompt recall on the composer
// (issue #611, part of #608). `up`/`down` recall a prior/following prompt into
// the draft only while the caret rests on the top (up) or bottom (down) line;
// elsewhere the key falls through to the textarea caret motion, so in-draft
// navigation and the shift variants are untouched. Recall is suppressed while
// a turn streams and while any completion surface is open; a recalled
// `/skill ...` line stays inert until Enter submits it through the slash path.
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
	m.composer.MoveToEnd()
	m.syncComposerHeight()
	m.trackComposer()
}

// canRecall reports whether arrow recall may fire right now: an open
// completion menu or a streaming turn both block it.
func (m Model) canRecall() bool {
	if m.slash.isOpen() || m.mention.isOpen() {
		return false
	}
	return !m.tx.busy
}

// endRecall drops any active arrow recall, forgetting the archived draft and
// the recall cursor. It is idempotent and safe to call from the submit path and
// any non-arrow key that returns the composer to neutral editing.
func (m *Model) endRecall() {
	m.histIdx = -1
	m.histDraft = ""
}

func (m Model) startSkillActivation(name, args string) (tea.Model, tea.Cmd) {
	m.tx.appendUserMsg("/" + name)
	if m.deps.Skills == nil || m.deps.Skills.Activate == nil {
		m.tx.appendMsg(failurePrefix() + "no skill activation available")
		return m, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.skillSeq++
	m.skillPending = true
	m.skillCancel = cancel
	m.tx.busy = true
	m.syncComposerRail()
	return m, skillCmdWithContext(ctx, m.deps.Skills.Activate, name, args, m.skillSeq)
}

func (m Model) acceptEvent(u Event) bool {
	if u.RunID == 0 {
		return true // tests and package-local callers can deliver direct events.
	}
	return m.liveRunID == u.RunID
}

// stopTurn cancels the in-flight turn through the session.
func (m *Model) stopTurn() {
	if m.skillPending {
		if m.skillCancel != nil {
			m.skillCancel()
		}
		m.skillPending = false
		m.skillCancel = nil
		m.tx.endTurn()
		m.tx.appendMsg("[stopped]")
		m.syncComposerRail()
		return
	}
	m.session.Stop()
}

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

// completeSlashCommand accepts highlighted slash completion into composer.
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

package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/glemsom/eitri/internal/config"
)

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

// SkillItem is one detected skill surfaced to the TUI's skills panel: its
// install scope and whether it is currently activated this session
// (docs/spec.md §9, eitri.md §2.3).
type SkillItem struct {
	Name        string
	Description string
	Scope       string
	Active      bool
}

// SkillsSurface wires the TUI's skills panel and slash-command activation to
// the run's tool layer (T8). Items lists the detected skills; Activate runs one
// skill activation (the T8 `skill` tool via the engine/registry seam) and
// returns the activation payload. Nil means no skills were detected.
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
	// Skills, when non-nil, backs the skills panel and slash-command activation.
	// Nil hides the panel and disables `/skillname` commands (no skills).
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
	// / MODEL, fed from the telemetry and skill surfaces. Nil hides the rail
	// (the plain chat default).
	Rail *Rail
}

// Model is the Bubble Tea state backing the TUI. It owns a single textarea
// composer and the conversation log, and drives agent turns over the injected
// Turn seam. It renders into the primary buffer via Bubble Tea's default
// (non-alt-screen) renderer, so native scrollback/selection/search survive.
// It also hosts the Settings surface (ctrl+s) and the interactive max-turns
// continuation prompt.
type Model struct {
	composer textarea.Model
	turn     Turn
	deps     Dependencies

	messages []message
	busy     bool

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

	// skills is the live list backing the skills panel, refreshed on slash
	// activation so the panel reflects per-session active state.
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
	// height is the terminal height of the last WindowSizeMsg (ADR-0006
	// decision 3, issue T02): the history viewport clamps to it so the fixed
	// bottom band never trails off-screen on a window shrink. It is 0 until the
	// first resize lands, in which case the history renders unclamped.
	height int
	// railAuto is true until the user first presses ctrl+b: the rail then follows
	// width (auto-show wide, auto-hide narrow). After a toggle it is false and
	// railShown owns the decision explicitly, so ctrl+b works on any width.
	railAuto bool
	// railShown is the explicit rail visibility after the user toggles (issue
	// #88 AC1). Current when !railAuto.
	railShown bool

	// histVer bumps every time the scroll region's inputs change (message, tool
	// entry, skills/active state, thinking expansion, busy flag, rail toggle) so
	// a stale scroll-cache rebuild is detected rather than served (ADR-0006
	// decision 4, issue T03). It is a value field bumped in Update and carried
	// forward by the returned model copy.
	histVer int
	// histCache is the bounded scroll-region render cache (ADR-0006 decision 4,
	// issue T03): it holds the rendered history content plus the width-bucket and
	// content version it was built at, so a terminal resize reuses prior markdown
	// instead of re-running the expensive Glamour pass over the whole history on
	// every tick. It is a pointer so the content written by View (which runs on a
	// value copy) survives across render cycles.
	histCache *scrollCache
}

// scrollCache is the bounded scroll-region render cache (ADR-0006 decision 4,
// issue T03): the rendered history content string plus the transcript width-
// bucket and history content version it was built at. A lookup hits when both
// match the model's current bucket and version, and rebuilds exactly once when
// either changes. rebuilds counts content rebuilds and is a test-only seam for
// asserting bounded re-render on resize.
type scrollCache struct {
	content string
	bkt     int
	version int
	// contentDone marks that a build has landed, so an genuinely-empty history
	// is not mistaken for an unbuilt cache.
	contentDone bool
	rebuilds    int
}

// NewModel builds a bare chat-only model (no Settings surface), the historical
// default signature. The interactive app uses NewModelCfg for Settings.
func NewModel(t Turn) Model {
	return NewModelCfg(Dependencies{Turn: t})
}

// NewModelCfg builds a TUI model wired to the given dependencies.
func NewModelCfg(d Dependencies) Model {
	tx := textarea.New()
	tx.Placeholder = "Ask Eitri something…"
	tx.Focus()
	tx.CharLimit = 0
	tx.ShowLineNumbers = false

	return Model{
		composer:     tx,
		turn:         d.Turn,
		deps:         d,
		continueReq:  make(chan struct{}, 1),
		continueResp: make(chan bool, 1),
		skills:       skillSnapshot(d),
		telemetry:    d.Telemetry,
		stream:       d.Stream,
		curStream:    -1,
		toolFeed:     d.Tools,
		rail:         d.Rail,
		railAuto:     true,
		// Seed the bounded scroll-cache with a sentinel bucket so the first view
		// always builds (an unbuilt empty cache must not be served as content).
		histCache:       &scrollCache{bkt: -1},
		reasoningEffort: d.Config.ReasoningEffort,
	}
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

// Init returns any startup commands. None are needed; input drives everything.
// Init returns any startup commands. It schedules the live telemetry waiter so
// the status strip starts refreshing from the engine seam immediately, even
// with no keyboard input (issue #86).
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
	return tea.Batch(cmds...)
}

// Update handles a UI event and returns the next state plus any commands.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

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

	case tea.KeyMsg:
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
		switch msgi.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "ctrl+s":
			return m.startSettings()
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
				return m, nil
			}
			rp := m.buildReview()
			m.review = &rp
			return m, nil
		case "ctrl+j":
			// Newline (Shift+Enter on terminals that report Enter and
			// Shift+Enter distinctly — bubbletea delivers the line-feed
			// "ctrl+j" key, never a plain "enter"). Inserts a line break
			// instead of submitting; no-op while a turn is running (ticket #57).
			if m.busy {
				return m, nil
			}
			m.composer.InsertString("\n")
			return m, nil
		case "enter":
			if m.busy {
				return m, nil
			}
			prompt := strings.TrimSpace(m.composer.Value())
			if prompt == "" {
				return m, nil
			}
			m.composer.Reset()
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
			if name, ok := slashCommand(prompt, m.skills); ok {
				return m.activateSkill(name)
			}
			m.messages = append(m.messages, message{role: "you", content: prompt})
			m.histVer++ // a caller prompt scores into the scroll region
			m.busy = true
			m.curStream = -1
			// Anchor new tool calls to this turn's prompt so entries interleave
			// after it (issue #84).
			m.curToolAnchor = len(m.messages) - 1
			// With a live answer stream, the composer turn and the stream waiter run
			// concurrently so the reply grows in place as deltas arrive (issue #83).
			if m.stream != nil {
				return m, tea.Batch(m.turnCmd(prompt), streamWait(m.stream))
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
			m.histVer++ // the thinking block's expansion changed the scroll region
			return m, nil
		}
		// alt+y toggles expanding tool-call entries to their full result (issue
		// #84): collapsed by default so the transcript stays clean, expanded on
		// demand so nothing is ever silently truncated.
		if msgi.Alt && msgi.Type == tea.KeyRunes && string(msgi.Runes) == "y" {
			m.showToolResult = !m.showToolResult
			m.histVer++ // tool entries flip between collapsed and expanded
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
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case turnDoneMsg:
		m.busy = false
		wasStreaming := m.curStream >= 0 && m.curStream < len(m.messages)
		if msgi.err != nil {
			// A streaming turn aborting with an error drops the partial reply and
			// renders the error in its place; the incremental buffer is advisory.
			m.curStream = -1
			m.messages = append(m.messages, message{role: "eitri", content: "⚠ " + msgi.err.Error()})
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
			m.histVer++ // the streamed answer settled to its final form
			m.curStream = -1
		} else {
			m.messages = append(m.messages, message{role: "eitri", content: msgi.answer, reasoning: msgi.reasoning})
			m.histVer++ // the committed answer scores into the scroll region
			return m, nil
		}
		return m, nil

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
		m.histVer++ // a skill result scores into the scroll region
		return m, nil
	}

	// Everything else reaches the composer too (focus management etc.).
	nm, cmd := m.composer.Update(msg)
	m.composer = nm
	return m, cmd
}

// updatePrompt handles a keypress while a continuation prompt is pending.
func (m Model) updatePrompt(msgi tea.KeyMsg) (tea.Model, tea.Cmd) {
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
func (m Model) updateSettings(msgi tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		// delete the trailing char.
		if s.field == fieldPaths {
			if msgi.String() == "backspace" && len(s.pathBuf) > 0 {
				s.SetPathBuf(s.pathBuf[:len(s.pathBuf)-1])
			} else if msgi.String() != "backspace" {
				s.SetPathBuf(s.pathBuf + msgi.String())
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
		m.histVer++ // the streaming message body grew
		return
	}
	if kind == ReasoningStream {
		m.messages = append(m.messages, message{role: "eitri", reasoning: delta, streaming: true})
	} else {
		m.messages = append(m.messages, message{role: "eitri", content: delta, streaming: true})
	}
	m.histVer++ // a new streaming assistant message opened
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
			name:   u.Start.Name,
			args:   u.Start.Args,
			anchor: m.curToolAnchor,
		})
		m.histVer++ // a new tool entry opened into the scroll region
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
				m.tools[i].complete = true
				m.histVer++ // the tool entry's result settled into the scroll region
				return
			}
		}
	}
}

// skillSnapshot captures the detected skills at construction so the panel has
// a stable, renderable list even if the Dependencies snapshot is nil or empty.
func skillSnapshot(d Dependencies) []SkillItem {
	if d.Skills != nil {
		return d.Skills.Items
	}
	return nil
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
// result as an assistant note. It flips the local panel state to active.
func (m Model) activateSkill(name string) (tea.Model, tea.Cmd) {
	m.messages = append(m.messages, message{role: "you", content: "/" + name})
	m.histVer++ // a skill command prompt scores into the scroll region
	if m.deps.Skills == nil || m.deps.Skills.Activate == nil {
		m.messages = append(m.messages, message{role: "eitri", content: "⚠ no skill activation available"})
		m.histVer++ // the unavailable-skill rejection scored in
		return m, nil
	}
	m.markActive(name)
	return m, skillCmd(m.deps.Skills.Activate, name)
}

// markActive sets the local panel skill (and any skill tool feedback) active.
func (m *Model) markActive(name string) {
	for i := range m.skills {
		if m.skills[i].Name == name {
			m.skills[i].Active = true
			m.histVer++ // the skills panel's active check changed the scroll region
		}
	}
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
	cands := make([]string, 0, len(skills)+1)
	if partial == "" || strings.HasPrefix("settings", partial) {
		cands = append(cands, "/settings")
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
	m.composer.SetCursor(len(cands[m.slashIdx]))
	// Advance for the next press so repeated tabs walk the whole list.
	m.slashIdx = (m.slashIdx + 1) % len(cands)
}

// View renders the conversation plus composer. It renders committed messages
// and the composer; works in primary buffer, so nothing is cleared. The
// Settings surface and the continuation prompt are rendered on top when active.
func (m Model) View() string {
	if m.settings != nil {
		return settingsView(*m.settings)
	}
	if m.prompting {
		return promptView()
	}

	// The right context rail (issue #88, Layout A): when visible, the rendered
	// transcript pane and the state rail sit side by side — one pane for time
	// (transcript), one for state (rail). The rail never steals width from the
	// primary buffer's native full-width selection except where it auto-shows
	// wide (railVisible gates it, and the composer width already shrank so the
	// transcript re-wraps to the freed space).
	left := m.renderPane()
	if m.rail != nil && m.railVisible() {
		right := styledRail(m.rail.render(m.telemetry, m.skills), m.railClampHeight())
		return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}
	return left
}

// railClampHeight returns the maximum number of rows the right context rail may
// occupy so it matches the history region's visible height (ADR-0006 decision
// 5, issue T05 AC1): both panes clamp to the rows left over by the fixed bottom
// band, so the two form one coherent row. It is -1 before the first resize
// lands, leaving the rail unclamped — mirroring renderHistoryViewport; a
// non-negative result is the actual row budget (0 when the band fills the whole
// terminal, in which case the rail renders nothing).
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

// bandHeight returns how many terminal rows the fixed bottom band (status
// strip, slash completion, composer) occupies, so the scroll region and the
// right rail can clamp to the rows it leaves behind (ADR-0006 decision 3/5).
func (m Model) bandHeight() int {
	var band strings.Builder
	m.renderBand(&band)
	return lineCount(band.String())
}

// widthBucketCols is the width granularity of the history render cache: the
// transcript width is bucketed into widthBucketCols-wide buckets, so a resize
// that changes the terminal width but not the bucket reuses prior rendered
// markdown instead of re-wrapping every message (ADR-0006 decision 4, issue
// T03 AC1). Coarse enough to absorb small drag-resize jitter, fine enough that
// a real rewrap does not wait several cols.
const widthBucketCols = 16

// widthBucket returns the render-cache bucket for a transcript width.
func widthBucket(width int) int {
	return width / widthBucketCols
}

// renderPane renders the transcript + composer surface into the left pane. It
// is the historical single-pane view; the rail adds itself to the right when
// visible. It works in the primary buffer, so nothing is cleared.
//
// Render is split into explicit, ordered regions (ADR-0006 decision 6, issue
// T01): the review overlay region (when open) on top, the scroll region
// (history), then the fixed bottom band (status strip + composer). Each region
// renders independently into its own builder; renderPane just concatenates them
// in order. The scroll region is Height-aware (ADR-0006 decision 3, issue T02):
// its content clamps to the terminal height, so the band stays pinned and only
// the history scrolls.

func (m Model) renderPane() string {
	var overlay, band strings.Builder
	// Overlay region: the review panel takes over the top of the pane (Layout B,
	// issue #90), showing the dense changed-file summary + inline diff above the
	// transcript. Settings and the continuation prompt are also overlays but
	// return earlier from View() as their own full-surface regions.
	if m.review != nil {
		m.renderReview(&overlay)
	}
	// The scroll region is served from the width-bucket cache (ADR-0006
	// decision 4, issue T03) so a resize reuses prior rendered history rather
	// than re-running the whole markdown pass each tick.
	histContent := m.historyContent()
	m.renderBand(&band)
	bandStr := band.String()
	return overlay.String() + m.renderHistoryViewport(histContent, lineCount(bandStr)) + bandStr
}

// historyContent returns the scroll region's rendered content, bounded per
// width-bucket (ADR-0006 decision 4, issue T03). It returns the cached history
// string when the transcript width-bucket and the content version are both
// unchanged since the last build — so a drag-resize that stays within a bucket,
// or re-renders that touch nothing, cost nothing — and rebuilds exactly once
// when either changes. The rebuild re-runs renderHistory and records the bucket
// + version it was built at for the next look-up.
func (m Model) historyContent() string {
	bkt := widthBucket(m.transcriptWidth())
	c := m.histCache
	if c.contentDone && c.bkt == bkt && c.version == m.histVer {
		return c.content
	}
	var hist strings.Builder
	m.renderHistory(&hist)
	// The cache is a heap pointer so the content written here (View runs on a
	// value copy) survives across render cycles.
	c.content = hist.String()
	c.bkt = bkt
	c.version = m.histVer
	c.contentDone = true
	c.rebuilds++
	return c.content
}

// renderHistoryViewport returns the Height-clamped scroll region (ADR-0006
// decision 3, issue T02): the rendered history content limited to the rows the
// fixed bottom band does not occupy, so the band stays pinned at the very
// bottom and only the history clips. Until the first resize lands (m.height ==
// 0) the history renders unclamped — the historical pre-resize behaviour, kept
// for lean embeds and tests that never size.
//
// While the run is live (no user scroll input yet — native scroll is the
// navigation path, ADR-0006 decision 6) the clamp is bottom-anchored: the
// newest output stays visible. The bubbletea/viewport scroll-position/follow
// state (not just the per-width render cache from T03, which landed in issue
// #107) arrives with the T04 live-follow seam (issue #108); decision 6 keeps
// paging/mouse routing off here. Line endings are preserved so the primary
// buffer's native selection/scrollback stay clean (ADR-0006 decision 1).
func (m Model) renderHistoryViewport(content string, bandLines int) string {
	if m.height <= 0 {
		return content
	}
	vh := m.height - bandLines
	if vh <= 0 {
		// The fixed band occupies the whole terminal; no room for history.
		return ""
	}
	return visibleHistory(content, vh)
}

// visibleHistory returns the visible, bottom-anchored slice of the history
// content: the last vh lines, or the whole content when it fits. It is a pure
// function of the rendered history and the available height — no viewport state
// is kept on the model until T03/T04 add the persisted scroll/cache.
func visibleHistory(content string, vh int) string {
	lines := strings.Split(content, "\n")
	// A trailing newline yields one empty final line; drop it so it never counts
	// as a content row.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if len(lines) <= vh {
		// The whole history fits in the viewport; no clipping needed.
		return strings.Join(lines, "\n")
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

// renderHistory renders the scroll region: the agent history that the user
// reads and scrolls. It surfaces the workspace header, every committed message
// (thinking blocks + markdown body), the interleaved tool entries, the skills
// panel, and the busy indicator. It is the only region T02+ makes scrollable
// and height-clamps.
func (m Model) renderHistory(b *strings.Builder) {
	// Surface the project's read-only state (issue #82 AC1): the workspace
	// directory the run operates in, rendered as an informational header above
	// the transcript and never inside the composer the user types into.
	if m.deps.WorkspacePath != "" {
		b.WriteString(statusStyle.Render("workspace: " + m.deps.WorkspacePath))
		b.WriteString("\n")
	}
	for i, msg := range m.messages {
		// Reasoning renders as a distinct, collapsible per-turn block — never
		// merged into the answer. Collapsed it is a one-line hint carrying the
		// token estimate + effort; `tab` expands just that turn's block (issue
		// #85, docs/spec.md §6).
		if msg.role != "you" && msg.reasoning != "" {
			b.WriteString(thinkingHeader(msg.reasoning, m.reasoningEffort))
			if msg.thinkingExpanded {
				b.WriteString(msg.reasoning + "\n")
			}
		}
		md, _ := RenderMarkdown(msg.content, m.composer.Width())
		if msg.role == "you" {
			fmt.Fprintf(b, "%s\n%s\n", headerStyle.Render("you"), md)
		} else {
			fmt.Fprintf(b, "%s\n%s\n", headerStyle.Render("eitri"), md)
		}
		// Interleave the turn's tool-call entries right after its prompting "you"
		// message (issue #84): compact one-liners, collapsed by default, expanded
		// on demand to the full result.
		for _, te := range m.tools {
			if te.anchor == i {
				b.WriteString(renderToolEntry(te, m.showToolResult))
			}
		}
	}
	renderSkillsPanel(b, m.skills)
	if m.busy {
		b.WriteString(statusStyle.Render("… thinking"))
		b.WriteString("\n")
	}
}

// renderBand renders the fixed bottom band: the live status strip (when wired)
// plus the slash-command completion list and the composer, in that order. This
// is the region T02+ pins at the bottom so it never scrolls away on resize.
func (m Model) renderBand(b *strings.Builder) {
	// Live status strip (issue #86), rendered above the composer so model,
	// effort, thinking, turns/max, cost, and the cache gauge stay glanceable.
	if m.telemetry != nil {
		b.WriteString(statusStyle.Render(m.telemetry.render(m.composer.Width())))
		b.WriteString("\n")
	}
	// The slash-command completion list (issue #87 AC1) sits above the composer
	// whenever the input line is a `/...` command, listing the built-in
	// /settings command + matching skills with the current selection marked.
	renderSlashCompletion(b, m.slashPrefix, m.composer.Value(), m.skills, m.slashIdx)
	b.WriteString(m.composer.View())
	if m.savedMsg != "" {
		b.WriteString("\n" + statusStyle.Render(m.savedMsg))
		m.savedMsg = ""
	}
}

// promptView renders the interactive max-turns continuation prompt.
func promptView() string {
	return headerStyle.Render("run paused at the max-turns cap") + "\n" +
		"Continue the run with more turns? (" + statusStyle.Render("y") + "/" + statusStyle.Render("n") + ")"
}

// thinkingHeader renders a turn's collapsible reasoning block header. Collapsed
// it is a one-line hint carrying a token estimate and the reasoning-effort tier
// (issue #85 AC2: "🤔 1.4k tok · medium"); the block renders distinctly from the
// answer so reasoning is recognizable but secondary, and settles back to this
// hint when the turn's answer lands. reasoning is the accumulated thinking text;
// effort is the run's reasoning-effort tier (empty drops the suffix).
func thinkingHeader(reasoning, effort string) string {
	hint := fmt.Sprintf("🤔 %s tok", formatTokens(tokenEstimate(reasoning)))
	if effort != "" {
		hint += " · " + effort
	}
	return statusStyle.Render(hint) + "\n"
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
func renderSlashCompletion(b *strings.Builder, value string, cur string, skills []SkillItem, selected int) {
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
		marker := "  "
		if i == sel {
			marker = "▸ "
		}
		b.WriteString(statusStyle.Render(marker + c))
		b.WriteString("\n")
	}
}

// renderSkillsPanel appends the skills panel to the view: one line per detected
// skill showing its install scope and activation state, so the interactive TUI
// surfaces detected + currently-activated skills (docs/spec.md §9, eitri.md
// §2.3). It renders nothing when no skills were detected.
func renderSkillsPanel(b *strings.Builder, skills []SkillItem) {
	if len(skills) == 0 {
		return
	}
	b.WriteString(statusStyle.Render("skills"))
	b.WriteString("\n")
	for _, it := range skills {
		state := "✕"
		if it.Active {
			state = "✓"
		}
		b.WriteString(statusStyle.Render("  " + it.Name + " [" + it.Scope + "] " + state))
		b.WriteString("\n")
	}
}

// renderToolEntry renders one tool-call entry as a compact, glanceable line —
// `⊕ tool  args` — with the result collapsed by default to a summary, never a
// raw dump into the scroll (issue #84). A file-mutating edit carries a [+N,-M]
// line-delta tag, and a compressed result carries an explicit "+N more" tail
// marker. When expanded (showToolResult), the full inline result is rendered so
// nothing is silently truncated — every collapse has an expand path.
func renderToolEntry(te toolEntry, expanded bool) string {
	var b strings.Builder
	head := "⊕ " + te.name
	if arg := toolArgsHint(te.args); arg != "" {
		head += "  " + arg
	}
	// Line-delta tag for file-edit tools (issue #84 AC3).
	if te.name == "edit" || te.name == "write" {
		head += fmt.Sprintf("  [+%d, −%d]", te.added, te.removed)
	}
	b.WriteString(statusStyle.Render(head))
	b.WriteString("\n")

	if !expanded {
		// Collapsed summary: line count + explicit "+N more" tail marker when
		// the result was compressed (docs/spec.md §5). Never a raw dump.
		if te.lines > 0 || te.dropped > 0 {
			summary := fmt.Sprintf("%d lines", te.lines)
			if te.compressed && te.dropped > 0 {
				summary += fmt.Sprintf(" (+%d more)", te.dropped)
			}
			b.WriteString(statusStyle.Render("  " + summary))
			b.WriteString("\n")
		}
		return b.String()
	}

	// Expanded: render the full inline result.
	if te.result != "" {
		b.WriteString(strings.TrimSuffix(te.result, "\n"))
		b.WriteString("\n")
	}
	return b.String()
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

// statusStyle is a small Lip Gloss style for the in-progress indicator, kept
// package-level so View stays deterministic enough to render-test.
var (
	headerStyle = lipgloss.NewStyle().Bold(true)
	statusStyle = lipgloss.NewStyle().Faint(true)
)

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

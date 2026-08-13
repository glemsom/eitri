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
	// showThinking expands the collapsible reasoning blocks in the log. It
	// defaults false (auto-collapsed after a turn) and toggles on `tab` so the
	// user can watch reasoning on demand (docs/spec.md §6/§9, ticket #17).
	showThinking bool

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

	case answerDeltaMsg:
		// A streamed answer-text delta arrived through the waiting command: grow
		// the in-progress assistant message in place and immediately re-issue
		// the waiter so the reply keeps streaming (issue #83). Deltas arriving
		// after the turn completed (a race with the final delta) are dropped so
		// they never spawn a spurious assistant message.
		if m.stream == nil || !m.busy {
			return m, nil
		}
		m.appendAnswerDelta(msgi.delta)
		return m, streamWait(m.stream)

	case tea.WindowSizeMsg:
		m.composer.SetWidth(msgi.Width - 2)
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
			m.openSettings()
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
				m.openSettings()
				return m, nil
			}
			if name, ok := slashCommand(prompt, m.skills); ok {
				return m.activateSkill(name)
			}
			m.messages = append(m.messages, message{role: "you", content: prompt})
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
			// Toggle the collapsible thinking stream (auto-collapsed by default).
			m.showThinking = !m.showThinking
			return m, nil
		}
		// alt+y toggles expanding tool-call entries to their full result (issue
		// #84): collapsed by default so the transcript stays clean, expanded on
		// demand so nothing is ever silently truncated.
		if msgi.Alt && msgi.Type == tea.KeyRunes && string(msgi.Runes) == "y" {
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
			m.curStream = -1
		} else {
			m.messages = append(m.messages, message{role: "eitri", content: msgi.answer, reasoning: msgi.reasoning})
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

// openSettings seeds the Settings form from the loaded config + discovery.
func (m *Model) openSettings() {
	cfg := m.deps.Config
	if cfg.Provider == "" {
		cfg = config.Default()
	}
	sf := newSettingsForm(cfg, m.deps.Models)
	m.settings = &sf
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

// appendAnswerDelta grows the in-progress assistant message by one streamed
// answer delta (issue #83). It returns no additional command. On the first
// delta of a turn it appends a new assistant message and records its index as
// the current stream target; subsequent deltas extend that same message in
// place so the Markdown render grows token by token.
func (m *Model) appendAnswerDelta(delta string) {
	if delta == "" {
		return
	}
	if m.curStream >= 0 && m.curStream < len(m.messages) && m.messages[m.curStream].streaming {
		m.messages[m.curStream].content += delta
		return
	}
	m.messages = append(m.messages, message{role: "eitri", content: delta, streaming: true})
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
	if m.deps.Skills == nil || m.deps.Skills.Activate == nil {
		m.messages = append(m.messages, message{role: "eitri", content: "⚠ no skill activation available"})
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
		}
	}
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

	var b strings.Builder
	// Review panel open: it takes over the view (Layout B, issue #90), showing
	// the dense changed-file summary + inline diff above the transcript.
	if m.review != nil {
		m.renderReview(&b)
	}
	// Surface the project's read-only state (issue #82 AC1): the workspace
	// directory the run operates in, rendered as an informational header above
	// the transcript and never inside the composer the user types into.
	if m.deps.WorkspacePath != "" {
		b.WriteString(statusStyle.Render("workspace: " + m.deps.WorkspacePath))
		b.WriteString("\n")
	}
	for i, msg := range m.messages {
		// Reasoning renders as a distinct, collapsible stream — never merged
		// into the answer. Auto-collapsed by default; `tab` expands (N17).
		if msg.role != "you" && msg.reasoning != "" {
			b.WriteString(thinkingHeader())
			if m.showThinking {
				b.WriteString(msg.reasoning + "\n")
			}
		}
		md, _ := RenderMarkdown(msg.content, m.composer.Width())
		if msg.role == "you" {
			fmt.Fprintf(&b, "%s\n%s\n", headerStyle.Render("you"), md)
		} else {
			fmt.Fprintf(&b, "%s\n%s\n", headerStyle.Render("eitri"), md)
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
	renderSkillsPanel(&b, m.skills)
	if m.busy {
		b.WriteString(statusStyle.Render("… thinking"))
		b.WriteString("\n")
	}
	// Live status strip (issue #86), rendered above the composer so model,
	// effort, thinking, turns/max, cost, and the cache gauge stay glanceable.
	if m.telemetry != nil {
		b.WriteString(statusStyle.Render(m.telemetry.render(m.composer.Width())))
		b.WriteString("\n")
	}
	// The slash-command completion list (issue #87 AC1) sits above the composer
	// whenever the input line is a `/...` command, listing the built-in
	// /settings command + matching skills with the current selection marked.
	renderSlashCompletion(&b, m.slashPrefix, m.composer.Value(), m.skills, m.slashIdx)
	b.WriteString(m.composer.View())
	if m.savedMsg != "" {
		b.WriteString("\n" + statusStyle.Render(m.savedMsg))
		m.savedMsg = ""
	}
	return b.String()
}

// promptView renders the interactive max-turns continuation prompt.
func promptView() string {
	return headerStyle.Render("run paused at the max-turns cap") + "\n" +
		"Continue the run with more turns? (" + statusStyle.Render("y") + "/" + statusStyle.Render("n") + ")"
}

// thinkingHeader renders the collapsible reasoning-stream header. It labels the
// block distinctly from the answer so reasoning is recognizable but secondary;
// the collapsed state reflects that the block is auto-collapsed after the turn
// (docs/spec.md §9, ticket #17).
func thinkingHeader() string {
	return statusStyle.Render("‹ thinking ›") + "\n"
}

// renderSlashCompletion appends the slash-command completion list to the view
// above the composer (issue #87 AC1): the built-in `/settings` command plus any
// matching detected skills, marking the currently-selected candidate (slashIdx)
// so the user sees exactly what a tab/return would pick. It renders nothing for
// a non-slash line or when there are no candidates, so normal typing is
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

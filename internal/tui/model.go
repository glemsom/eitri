package tui

import (
	"context"
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

	// telemetry is the live status strip state (issue #86); nil disables it.
	telemetry *Telemetry
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
	if m.telemetry != nil {
		return telemetryWait(m.telemetry)
	}
	return nil
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
		switch msgi.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "ctrl+s":
			m.openSettings()
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
			// A slash command activates a detected skill directly (eitri.md §2.3):
			// `/skillname` runs the T8 skill tool and surfaces the result, instead
			// of sending the raw command as a chat prompt.
			if name, ok := slashCommand(prompt, m.skills); ok {
				return m.activateSkill(name)
			}
			m.messages = append(m.messages, message{role: "you", content: prompt})
			m.busy = true
			return m, m.turnCmd(prompt)
		case "tab":
			// Fresh `/` with tab completes the skill command: cycling through
			// matching detected skills. Otherwise tab toggles the thinking stream.
			if m.deps.Skills != nil && m.composer.Value() != "" && strings.HasPrefix(m.composer.Value(), "/") {
				m.completeSkillCommand()
				return m, nil
			}
			// Toggle the collapsible thinking stream (auto-collapsed by default).
			m.showThinking = !m.showThinking
			return m, nil
		}
		// Let the textarea handle editing (cursor, backspace, etc.).
		nm, cmd := m.composer.Update(msg)
		m.composer = nm
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)

	case turnDoneMsg:
		m.busy = false
		if msgi.err != nil {
			m.messages = append(m.messages, message{role: "eitri", content: "⚠ " + msgi.err.Error()})
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

// completeSkillCommand fills the composer with a `/skillname` completion cycling
// through detected skills matching the current partial command.
func (m *Model) completeSkillCommand() {
	cur := m.composer.Value()
	partial := strings.TrimSpace(strings.TrimPrefix(cur, "/"))
	matches := make([]string, 0, len(m.skills))
	for _, it := range m.skills {
		if strings.HasPrefix(it.Name, partial) {
			matches = append(matches, it.Name)
		}
	}
	if len(matches) == 0 {
		return
	}
	// Cycle deterministically: advance one match per press.
	var next string
	for i, n := range matches {
		if n == partial {
			next = matches[(i+1)%len(matches)]
			break
		}
	}
	if next == "" {
		next = matches[0]
	}
	m.composer.SetValue("/" + next)
	m.composer.SetCursor(len("/") + len(next))
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
	// Surface the project's read-only state (issue #82 AC1): the workspace
	// directory the run operates in, rendered as an informational header above
	// the transcript and never inside the composer the user types into.
	if m.deps.WorkspacePath != "" {
		b.WriteString(statusStyle.Render("workspace: " + m.deps.WorkspacePath))
		b.WriteString("\n")
	}
	for _, msg := range m.messages {
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

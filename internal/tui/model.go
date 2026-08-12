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
type Turn func(ctx context.Context, prompt string) (string, error)

// message is one committed line of the conversation log.
type message struct {
	role    string // "you" or "eitri"
	content string
}

type turnDoneMsg struct {
	prompt string
	answer string
	err    error
}

// Dependencies wires a Model to its environment: the conversation Turn, model
// discovery + loaded config for the Settings surface, and a persistence seam.
// Fields are optional; a zero Dependencies yields a plain chat-only model (the
// pre-settings default, kept for tests and lean embeds).
type Dependencies struct {
	// Turn drives one conversation turn (engine). Required for chat.
	Turn Turn
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
func (m Model) Init() tea.Cmd {
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
		case "enter":
			if m.busy {
				return m, nil
			}
			prompt := strings.TrimSpace(m.composer.Value())
			if prompt == "" {
				return m, nil
			}
			m.composer.Reset()
			m.messages = append(m.messages, message{role: "you", content: prompt})
			m.busy = true
			return m, m.turnCmd(prompt)
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
			m.messages = append(m.messages, message{role: "eitri", content: msgi.answer})
		}
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
		// Free-form typing on the paths field.
		if s.field == fieldPaths {
			s.SetPathBuf(s.pathBuf + msgi.String())
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
		answer, err := m.turn(ctx, prompt)
		return turnDoneMsg{prompt: prompt, answer: answer, err: err}
	})
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
	for _, msg := range m.messages {
		md, _ := RenderMarkdown(msg.content, m.composer.Width())
		if msg.role == "you" {
			fmt.Fprintf(&b, "%s\n%s\n", headerStyle.Render("you"), md)
		} else {
			fmt.Fprintf(&b, "%s\n%s\n", headerStyle.Render("eitri"), md)
		}
	}
	if m.busy {
		b.WriteString(statusStyle.Render("… thinking"))
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

// statusStyle is a small Lip Gloss style for the in-progress indicator, kept
// package-level so View stays deterministic enough to render-test.
var (
	headerStyle = lipgloss.NewStyle().Bold(true)
	statusStyle = lipgloss.NewStyle().Faint(true)
)

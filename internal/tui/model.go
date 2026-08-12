package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// Model is the Bubble Tea state backing the TUI. It owns a single textarea
// composer and the conversation log, and drives agent turns over the injected
// Turn seam. It renders into the primary buffer via Bubble Tea's default
// (non-alt-screen) renderer, so native scrollback/selection/search survive.
type Model struct {
	composer textarea.Model
	turn     Turn

	messages []message
	busy     bool
}

// NewModel builds a TUI model wired to the run function.
func NewModel(t Turn) Model {
	tx := textarea.New()
	tx.Placeholder = "Ask Eitri something…"
	tx.Focus()
	tx.CharLimit = 0
	tx.ShowLineNumbers = false

	return Model{composer: tx, turn: t}
}

// Init returns any startup commands. None are needed; input drives everything.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles a UI event and returns the next state plus any commands.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msgi := msg.(type) {
	case tea.WindowSizeMsg:
		m.composer.SetWidth(msgi.Width - 2)
		return m, nil

	case tea.KeyMsg:
		switch msgi.String() {
		case "ctrl+c":
			return m, tea.Quit
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
			return m, turnCmd(m.turn, prompt)
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

// turnCmd reports a turn's completion back to the model.
func turnCmd(t Turn, prompt string) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		answer, err := t(ctx, prompt)
		return turnDoneMsg{prompt: prompt, answer: answer, err: err}
	})
}

// View renders the conversation plus composer. It renders committed messages
// and the composer; works in primary buffer, so nothing is cleared.
func (m Model) View() string {
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
	return b.String()
}

// statusStyle is a small Lip Gloss style for the in-progress indicator, kept
// package-level so View stays deterministic enough to render-test.
var (
	headerStyle = lipgloss.NewStyle().Bold(true)
	statusStyle = lipgloss.NewStyle().Faint(true)
)

package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

// LoginCode is the human-visible device-flow challenge surfaced by `/login`: the verification URL to open and the short user code to enter there.
type LoginCode struct {
	UserCode        string
	VerificationURI string
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

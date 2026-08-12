package app

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/tools"
	"github.com/glemsom/eitri/internal/tui"
)

// runEngineTurn adapts the shared runAgent turn to the tui.Turn seam. It runs
// one agent turn over the same engine, session transcript, and tool registry as
// batch, so a TUI greeting round-trips through the engine exactly like batch
// (docs/spec.md §9, eitri.md §2.6).
func runEngineTurn(e *engine.Engine, cfg config.Config, reg *tools.Registry, sessionKey string) tui.Turn {
	return func(ctx context.Context, prompt string) (string, error) {
		res, err := runAgent(e, cfg, reg, sessionKey, prompt)
		if err != nil {
			return "", err
		}
		return res.Answer, nil
	}
}

// runTUI launches the interactive fullscreen TUI on the shared engine and
// blocks until the user quits. It renders into the primary (normal) buffer:
// Bubble Tea's default renderer does not enter the alt screen, so native
// scrollback, selection, and search survive a session (docs/spec.md §9).
func runTUI(e *engine.Engine, cfg config.Config, reg *tools.Registry, sessionKey string) error {
	return runProgram(tui.NewModel(runEngineTurn(e, cfg, reg, sessionKey)))
}

// runProgram launches a Bubble Tea program. It is a package-level seam so tests
// can exercise the boot path without a real terminal; the production default
// runs the interactive TUI.
var runProgram = func(m tui.Model) error {
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}

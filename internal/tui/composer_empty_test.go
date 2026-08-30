package tui

import (
	"context"
	"testing"
)

func TestModel_emptyComposerKeepsHardwareCursor(t *testing.T) {
	t.Parallel()
	m := resize(t, NewModel(func(context.Context, string, string) (TurnResult, error) {
		return TurnResult{Answer: "ok"}, nil
	}))

	if c := m.View().Cursor; c == nil {
		t.Fatal("empty composer must not detach the hardware cursor")
	}
}

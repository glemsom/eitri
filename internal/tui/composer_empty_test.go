package tui

import (
	"context"
	"strings"
	"testing"
)

func TestModel_emptyComposerShowsAffordance(t *testing.T) {
	t.Parallel()
	m := resize(t, NewModel(func(context.Context, string, string) (TurnResult, error) {
		return TurnResult{Answer: "ok"}, nil
	}))

	content := ansiStrip(view(m))
	if !strings.Contains(content, emptyComposerAffordance) {
		t.Fatalf("empty idle composer missing affordance %q, got:\n%s", emptyComposerAffordance, content)
	}
	if c := m.View().Cursor; c == nil {
		t.Fatal("empty affordance must not detach the hardware cursor")
	}
}

func TestModel_emptyComposerAffordanceHidesForDraftAndCompletion(t *testing.T) {
	t.Parallel()
	m := resize(t, NewModel(func(context.Context, string, string) (TurnResult, error) {
		return TurnResult{Answer: "ok"}, nil
	}))

	withDraft := typeText(t, m, "hello")
	if strings.Contains(ansiStrip(view(withDraft)), emptyComposerAffordance) {
		t.Fatalf("non-empty draft must hide affordance, got:\n%s", ansiStrip(view(withDraft)))
	}

	withSlash := typeText(t, m, "/")
	if strings.Contains(ansiStrip(view(withSlash)), emptyComposerAffordance) {
		t.Fatalf("slash completion must hide affordance, got:\n%s", ansiStrip(view(withSlash)))
	}

	ws := mentionWorkspace(t)
	withMention := mentionModel(t, ws)
	withMention = typeText(t, withMention, "@")
	withMention = feedMentionWalk(t, withMention, ws)
	if strings.Contains(ansiStrip(view(withMention)), emptyComposerAffordance) {
		t.Fatalf("mention completion must hide affordance, got:\n%s", ansiStrip(view(withMention)))
	}
}

func TestModel_emptyComposerAffordanceHidesWhileBusy(t *testing.T) {
	t.Parallel()
	m := resize(t, NewModel(func(context.Context, string, string) (TurnResult, error) {
		return TurnResult{Answer: "ok"}, nil
	}))
	m.tx.busy = true

	if strings.Contains(ansiStrip(view(m)), emptyComposerAffordance) {
		t.Fatalf("busy composer must hide affordance, got:\n%s", ansiStrip(view(m)))
	}
}

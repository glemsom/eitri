package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

// TestModel_greetingRoundTrip drives a greeting through the model over the
// injected Turn seam (the engine stand-in) and asserts the assistant answer is
// appended to the conversation (ticket #34: "a TUI run of a greeting
// round-trips through the engine and renders the answer").
func TestModel_greetingRoundTrip(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		if prompt != "hello" {
			t.Errorf("expected prompt 'hello', got %q", prompt)
		}
		return TurnResult{Answer: "Hello! **glad** to help."}, nil
	})

	// Set a size so the composer has a width.
	m = resize(t, m)

	// Focus + type the prompt, then submit.
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	content := view(m)
	// The prompt and the answer both render in the transcript; no role labels
	// ("you"/"eitri") prefix either side.
	if !strings.Contains(content, "hello") || !strings.Contains(content, "glad") {
		t.Errorf("expected prompt and answer in view, got: %q", content)
	}
	if strings.Contains(content, "you") || strings.Contains(content, "eitri") {
		t.Errorf("role labels must not render in the transcript, got: %q", content)
	}
	// The assistant's markdown answer must render (bold "glad" carries SGR 1).
	if !strings.Contains(content, "glad") {
		t.Errorf("expected assistant answer text in view, got: %q", content)
	}
	if !hasSGRBold(view(m)) {
		t.Errorf("expected markdown bold to render in assistant answer, got: %q", view(m))
	}
	// The conversation content must never itself carry a clear-screen
	// (\x1b[2J) or alt-screen (\x1b[?1049) sequence: those live in the
	// bubbletea program layer (the model's tea.View declares AltScreen), never
	// in the rendered content string.
	if strings.Contains(content, "\x1b[2J") {
		t.Errorf("view carries a clear-screen sequence; must render to primary buffer")
	}
	if strings.Contains(content, "\x1b[?1049") {
		t.Errorf("view carries an alt-screen sequence; must render to primary buffer")
	}
}

// TestModel_errorTurn asserts a failing turn renders a visible error instead of
// silently dropping.
func TestModel_errorTurn(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		return TurnResult{}, errors.New("provider exploded")
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	if !strings.Contains(view(m), "provider") || !strings.Contains(view(m), "exploded") {
		t.Errorf("expected error words in view, got: %q", view(m))
	}
}

// resize installs a window size on the model.

// TestModel_thinkingCollapsible asserts reasoning renders as a distinct,
// auto-collapsed per-turn block: the collapsed hint line is always shown, the
// reasoning body is hidden until `tab` expands the block, and reasoning never
// leaks into the answer (ticket #17 / #85).
func TestModel_thinkingCollapsible(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		return TurnResult{
			Answer:    "plain answer",
			Reasoning: "I reason about it first.",
		}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	// Auto-collapsed after the turn: hint present, body absent by default.
	content := view(m)
	if !strings.Contains(content, "🤔") {
		t.Errorf("expected a thinking hint in content, got: %q", content)
	}
	if strings.Contains(content, "I reason about it first") {
		t.Errorf("reasoning body should be collapsed by default, got: %q", content)
	}

	// Toggling with `tab` expands the reasoning body.
	toggled, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = asModel(t, toggled)
	expanded := view(m)
	if !strings.Contains(expanded, "I reason about it first") {
		t.Errorf("tab should expand the reasoning block, got: %q", expanded)
	}
	// The answer is still rendered, and reasoning appears before it as a distinct
	// stream (never interleaved into the answer body). Glamour word-wraps the
	// answer across ANSI runs, so match on the word rather than the full phrase.
	if !strings.Contains(expanded, "plain") {
		t.Errorf("answer still required in content, got: %q", expanded)
	}
	thinkingIdx := strings.Index(expanded, "I reason about it first")
	answerIdx := strings.Index(expanded, "plain")
	if thinkingIdx == -1 || answerIdx == -1 || thinkingIdx > answerIdx {
		t.Errorf("reasoning block must render as its own stream before the answer, got: %q", expanded)
	}
}

// TestModel_thinkingHintReportsTokensAndEffort asserts the collapsed thinking
// hint is a one-line summary carrying a reason-token estimate and the
// reasoning-effort tier (issue #85 AC2: "🤔 1.4k tok · medium").
func TestModel_thinkingHintReportsTokensAndEffort(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer", Reasoning: strings.Repeat("reasoning words. ", 400)}, nil
		},
		Config: config.Config{ReasoningEffort: "medium"},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	content := view(m)
	// The hint labels the reasoning stream and carries the configured effort.
	if !strings.Contains(content, "🤔") {
		t.Errorf("collapsed state should show the thinking hint, got: %q", content)
	}
	if !strings.Contains(content, "· medium") {
		t.Errorf("hint should carry the reasoning-effort tier, got: %q", content)
	}
	// A token estimate renders (thousands compact as X.Xk). The reasoning body
	// stays hidden.
	if !strings.Contains(content, "tok") {
		t.Errorf("hint should carry a token count, got: %q", content)
	}
	if strings.Contains(content, "reasoning words") {
		t.Errorf("reasoning body must stay collapsed behind the hint, got: %q", content)
	}
}

// TestModel_thinkingAutoCollapsesOnAnswer asserts an expanded per-turn thinking
// block collapses back to its hint when the turn's final answer lands (issue
// #85 AC3: "auto-collapses once the turn's final answer lands").
func TestModel_thinkingAutoCollapsesOnAnswer(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	// First reasoning delta creates the block, then tab expands it (issue #85
	// AC3: the user can watch reasoning on demand).
	m = applyReasoningDelta(t, m, "hidden reasoning")
	toggled, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = asModel(t, toggled)
	if !strings.Contains(view(m), "hidden reasoning") {
		t.Errorf("expanded block should show reasoning before answer lands, got: %q", view(m))
	}
	// The final answer lands; the block auto-collapses.
	nm, _ := m.Update(turnDoneMsg{prompt: "hi", answer: "final answer", reasoning: "hidden reasoning"})
	m = asModel(t, nm)
	if strings.Contains(view(m), "hidden reasoning") {
		t.Errorf("thinking block should auto-collapse when the answer lands, got: %q", view(m))
	}
}

// TestModel_railRendersNoSkillsSection asserts the right context rail renders
// STATS / CONTEXT / MODEL only — no SKILLS section, no skill listing, no
// activation state — even on a wide window where the rail auto-shows with a
// detected skills surface wired (issue #188). Skills still activate via the
// slash-command surface, just not in the rail.
func TestModel_railRendersNoSkillsSection(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn:   func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Skills: &SkillsSurface{Items: []SkillItem{{Name: "my-skill"}}},
		Rail:   NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1"),
	})
	m = resizeTo(t, m, 140, 24)
	content := view(m)
	for _, want := range []string{"STATS", "CONTEXT", "MODEL"} {
		if !strings.Contains(content, want) {
			t.Errorf("rail missing %s section, got: %q", want, content)
		}
	}
	if strings.Contains(content, "SKILLS") {
		t.Errorf("rail must not render a SKILLS section, got: %q", content)
	}
	if strings.Contains(content, "my-skill") {
		t.Errorf("rail must not list detected skills, got: %q", content)
	}
}

// TestModel_slashCommandActivatesSkill drives `/skillname` through the TUI
// slash-command path: the activation seam runs and the activation payload
// renders as an assistant note. This is the TUI side of the engine-seam
// activation flow (ticket #35); the rail shows no ✓/✕ state for it (issue
// #188).
func TestModel_slashCommandActivatesSkill(t *testing.T) {
	var activated string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Skills: &SkillsSurface{
			Items: []SkillItem{{Name: "my-skill"}},
			Activate: func(_ context.Context, name string) (string, error) {
				activated = name
				return `<skill_content name="my-skill">payload</skill_content>`, nil
			},
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "/my-skill")
	m = submitAndWait(t, m)

	if activated != "my-skill" {
		t.Errorf("activation seam called with %q, want \"my-skill\"", activated)
	}
	content := view(m)
	if !strings.Contains(content, "payload") {
		t.Errorf("skill payload should render in content, got: %q", content)
	}
}

// TestModel_slashCompletionTab asserts `/` + tab cycles a skill-name completion
// in the composer.
func TestModel_slashCompletionTab(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Skills: &SkillsSurface{Items: []SkillItem{
			{Name: "alpha"},
			{Name: "beta"},
		}},
	})
	m = resize(t, m)
	m = typeText(t, m, "/a")
	// Tab completes the partial `/a` to `/alpha`.
	toggled, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = asModel(t, toggled)
	if got := m.composer.Value(); got != "/alpha" {
		t.Errorf("tab completion = %q, want \"/alpha\"", got)
	}
}
func resize(t *testing.T, m Model) Model {
	t.Helper()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return asModel(t, nm)
}

// typeText feeds the given runes to the composer in one keypress.
func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyExtended, Text: s})
	return asModel(t, nm)
}

// submitAndWait feeds Enter to run the turn and then the async completion.
// The submit command may be a tea.BatchMsg (turn command batched with the
// stream waiter and/or the spinner tick); each sub-command runs synchronously
// and its message is delivered in order so the completion always lands.
func submitAndWait(t *testing.T, m Model) Model {
	t.Helper()
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("turn command was nil after submit")
	}
	out := asModel(t, nm)
	return runSubmitted(t, out, cmd)
}

// runSubmitted executes a submitted command synchronously, unwrapping a
// tea.BatchMsg into its sub-commands, and delivers each resulting message to
// the model. Waiter commands (streamWait) block until their channel yields —
// callers of submitAndWait must not wire a live stream (stream tests use
// submitBusy instead).
func runSubmitted(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if bm, ok := msg.(tea.BatchMsg); ok {
		for _, c := range bm {
			m = runSubmitted(t, m, c)
		}
		return m
	}
	if msg == nil {
		return m
	}
	nm, next := m.Update(msg)
	m = asModel(t, nm)
	// Thread the returned command through so a follow-up turn (e.g. a skill-
	// args turn queued by a skillDoneMsg handler, issue #239) chains: the next
	// command runs and its message is delivered in order. Plain turns return
	// nil here so this is a no-op for them.
	return runSubmitted(t, m, next)
}

func asModel(t *testing.T, tm tea.Model) Model {
	t.Helper()
	md, ok := tm.(Model)
	if !ok {
		t.Fatalf("tea.Model is %T, want Model", tm)
	}
	return md
}

// view renders the model's current surface as the view's content string
// (bubbletea v2: View returns a tea.View struct whose Content is the rendered
// surface; tests assert on that string).
func view(m Model) string { return m.View().Content }

// TestModel_shiftEnterInsertsNewline asserts Shift+Enter breaks a line in the
// composer instead of submitting: the prompt text must sit on a new line, the
// model must not go busy, and no turn command may be emitted (ticket #57).
func TestModel_shiftEnterInsertsNewline(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		t.Fatalf("Shift+Enter must not submit a turn, got prompt %q", prompt)
		return TurnResult{}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "line one")

	// Feed the key bubbletea maps Shift+Enter to (line feed, \n) on terminals
	// that report Enter and Shift+Enter distinctly.
	newlined, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	m = asModel(t, newlined)

	if got := m.composer.Value(); got != "line one\n" {
		t.Errorf("Shift+Enter should insert a newline, composer = %q", got)
	}
	if m.tx.busy {
		t.Errorf("Shift+Enter must not mark the model busy")
	}
}

// TestModel_workspaceStateSurfaced asserts the TUI surfaces the project's
// read-only state — the workspace path — as a header line above the transcript
// when supplied, so the user always sees which directory they're operating in
// (issue #82 AC1). The line is informational/read-only: opening the model with
// no workspace (the plain chat default) renders no such line.
func TestModel_workspaceStateSurfaced(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn:          func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		WorkspacePath: "/tmp/acme-project",
	})
	m = resize(t, m)

	content := view(m)
	if !strings.Contains(content, "/tmp/acme-project") {
		t.Errorf("expected workspace path surfaced in view (issue #82 AC1), got: %q", content)
	}

	// The workspace path is rendered as read-only header state, never in the
	// composer buffer the user types into.
	if strings.Contains(m.composer.Value(), "/tmp/acme-project") {
		t.Errorf("workspace path must not leak into the composer input, got: %q", m.composer.Value())
	}

	// No workspace supplied (the chat-only default) renders no such header.
	bare := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil })
	bare = resize(t, bare)
	if strings.Contains(view(bare), "workspace:") {
		t.Errorf("expected no workspace header when none is configured (issue #82 AC1)")
	}
}

// TestModel_shiftEnterThenSubmitSendsWholeMultiLine asserts a multi-line prompt
// assembled with Shift+Enter is delivered whole to the engine when the final
// plain Enter submits (ticket #57).
func TestModel_shiftEnterThenSubmitSendsWholeMultiLine(t *testing.T) {
	var got []string
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		got = append(got, prompt)
		return TurnResult{Answer: "ok"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "line one")
	newlined, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	m = asModel(t, newlined)
	m = typeText(t, m, "line two")

	if m.tx.busy {
		t.Fatalf("composing must keep the model idle until submit")
	}

	m = submitAndWait(t, m)
	if len(got) != 1 {
		t.Fatalf("expected one turn, engine saw %d", len(got))
	}
	if got[0] != "line one\nline two" {
		t.Errorf("engine should receive the whole multi-line prompt, got %q", got[0])
	}
}

// TestModel_shiftEnterIgnoredWhileBusy asserts Shift+Enter is a no-op (does not
// touch the composer) while a prior turn is still running (ticket #57).
func TestModel_shiftEnterIgnoredWhileBusy(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		return TurnResult{Answer: "ok"}, nil
	})
	m = resize(t, m)
	// Drive into the busy state (submitting a non-empty prompt) without
	// resolving the turn command.
	m = typeText(t, m, "first")
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected a turn command on submit")
	}
	m = asModel(t, nm)

	newlined, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	m = asModel(t, newlined)
	if got := m.composer.Value(); got != "" {
		t.Errorf("Shift+Enter while busy should not edit the composer, got %q", got)
	}
}

package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
)

// turnHeaderTranscript builds a committed, settled turn whose event log
// interleaves reasoning, a tool call, and an answer — with a known per-turn
// elapsed so the header text is assertable through Seam A (Transcript render).
func turnHeaderTranscript() *Transcript {
	th := themeFor(config.DefaultTheme)
	var log toolLog
	log.SetAnchor(0)
	log.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	log.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2}})
	return &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           100,
		height:          30,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		log:             log,
		messages: []message{
			{role: "you", content: "run it", elapsed: 5 * time.Second},
			{role: "eitri", content: "Done.", elapsed: 5 * time.Second, thinkingRequested: true,
				expansion: expansionWithReasoningForces(true, false),
				events: []TimelineEvent{
					{Kind: EventReasoning, Seq: 0, Delta: "Let me check the repo first."},
					{Kind: EventToolStart, Seq: 1, Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}},
					{Kind: EventToolResult, Seq: 2, Result: &ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2}},
					{Kind: EventAnswer, Seq: 3, Delta: "Done."},
				}},
		},
	}
}

func TestTranscript_turnHeaderCommittedAboveEachRole(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := turnHeaderTranscript()

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	youHeader := "You . 5s"
	eitriHeader := "Eitri + . 5s"
	if !strings.Contains(plain, youHeader) {
		t.Fatalf("committed user turn must open with %q, got:\n%s", youHeader, plain)
	}
	if !strings.Contains(plain, eitriHeader) {
		t.Fatalf("committed answer turn must open with %q, got:\n%s", eitriHeader, plain)
	}
	// The header sits above its role's content in the merged flow.
	youContent := strings.Index(plain, "run it")
	youHead := strings.Index(plain, youHeader)
	if !(youHead >= 0 && youHead < youContent) {
		t.Errorf("You header must precede the user content, got head=%d content=%d:\n%s", youHead, youContent, plain)
	}
	answerIdx := strings.Index(plain, "Done.")
	headIdx := strings.Index(plain, eitriHeader)
	if !(headIdx >= 0 && headIdx < answerIdx) {
		t.Errorf("Eitri header must precede the answer content, got head=%d answer=%d:\n%s", headIdx, answerIdx, plain)
	}
}

func TestTranscript_turnHeaderOneRowSettled(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := turnHeaderTranscript()

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	// Each header is exactly one row and carries no reasoning-token or tool
	// counts — those stay in the reasoning header and the tool entries.
	rows := strings.Split(plain, "\n")
	for _, header := range []string{"You . 5s", "Eitri + . 5s"} {
		seen := 0
		for _, ln := range rows {
			if strings.Contains(ln, header) {
				seen++
				if ln != header {
					t.Errorf("turn header %q must occupy exactly one row, got line %q:\n%s", header, ln, plain)
				}
				if strings.Contains(header, "tok") || strings.Contains(header, "line") {
					t.Errorf("turn header %q must carry no token/tool counts", header)
				}
			}
		}
		if seen != 1 {
			t.Errorf("turn header %q must render exactly once, got %d:\n%s", header, seen, plain)
		}
	}
}

func TestTranscript_liveTurnHeaderTintsToPhaseHue(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	tx := &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           100,
		height:          30,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		busy:            true,
		busyStartedAt:   time.Now().Add(-3 * time.Second),
		messages: []message{
			{role: "you", content: "p"},
			{role: "eitri", content: "It is alpha", reasoning: "Reading.", streaming: true, thinkingRequested: true},
		},
	}
	wireLive(tx, []TimelineEvent{
		{Kind: EventReasoning, Seq: 0, Delta: "Reading."},
		{Kind: EventAnswer, Seq: 1, Delta: "It is alpha"},
	})

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	rendered := hist.String()

	// The streaming answering turn's header is tinted to the phase hue (the
	// agent accent) while it is live.
	header := lineContaining(rendered, "Eitri +")
	if header == "" {
		t.Fatalf("live turn must render an Eitri header, got:\n%s", ansiStrip(rendered))
	}
	if !strings.Contains(header, "38;2;122;162;247") { // default accent #7AA2F7
		t.Errorf("live answering header must be tinted to the phase hue, got: %q", header)
	}
	// The live header keeps the same one-row shape and shows the running elapsed.
	plain := ansiStrip(rendered)
	if !strings.Contains(plain, "Eitri + . 3s") {
		t.Errorf("live header must show the running elapsed, got:\n%s", plain)
	}
	// The committed header (settled on commit) is the faint secondary style and
	// carries no phase color — verified in TestTranscript_turnHeaderCommittedAboveEachRole
	// via the same surface.
}

func TestTranscript_dragSelectCopiesTurnHeader(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := turnHeaderTranscript()
	tx.layout.dirty = true // force the render-to-layout pass underlying plainLines

	lines := tx.plainLines()
	headerLine := -1
	for i, l := range lines {
		if l == "You . 5s" || l == "Eitri + . 5s" {
			headerLine = i
			break
		}
	}
	if headerLine < 0 {
		t.Fatalf("turn header must appear in the drag-select plain rows, got:\n%s", strings.Join(lines, "\n"))
	}

	var s selectionWeaver
	s.start(headerLine, 0)
	s.move(headerLine, len(lines[headerLine])-1)
	got, ok := s.coveredLines(lines)
	if !ok || (got != "You . 5s" && got != "Eitri + . 5s") {
		t.Errorf("drag-select over a turn header must copy the role + elapsed, got %q ok=%v:\n%s", got, ok, strings.Join(lines, "\n"))
	}
	if strings.Contains(got, "│") || strings.Contains(got, "──") {
		t.Errorf("drag-select over a header must carry no role gutter or full-width separator, got %q", got)
	}
}

func TestCopyTranscript_turnHeadersStayOutOfClipboard(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		Config: config.Config{ThinkingEnabled: true},
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	text := m.transcriptText()
	if !strings.Contains(text, "you: hello") || !strings.Contains(text, "eitri: plain answer") {
		t.Fatalf("copy must keep its role-marked plain serializer, got: %q", text)
	}
	// Turn headers live only in the rendered surface — /copy and Ctrl+O are
	// byte-identical to before and never emit them.
	if strings.Contains(text, "You") || strings.Contains(text, "Eitri ⚒") {
		t.Errorf("/copy and Ctrl+O must not include turn headers, got: %q", text)
	}
}

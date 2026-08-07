package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/report"
)

// TestReportTimeline_RendersTurnCards verifies the report timeline sub-template
// renders user and assistant turn cards with their role classes and content.
func TestReportTimeline_RendersTurnCards(t *testing.T) {
	views := []TurnView{
		{
			Turn: report.Turn{
				Turn:      0,
				Role:      "user",
				Timestamp: time.Unix(0, 0),
			},
			ContentHTML: "<p>hello</p>",
		},
		{
			Turn: report.Turn{
				Turn:          1,
				Role:          "assistant",
				Timestamp:     time.Unix(0, 0),
				LLMDurationMs: 1500,
				LLMModel:      "test-model",
				ContextBefore: &report.ContextInfo{TotalTokens: 100, ContextWindow: 1000},
				ToolCalls: []report.ToolCallInfo{
					{Name: "bash", Arguments: map[string]any{"cmd": "ls"}, ResultPreview: "out", DurationMs: 5},
				},
			},
			ContentHTML:   "<p>hi there</p>",
			ReasoningHTML: "<p>thinking</p>",
		},
	}

	var buf bytes.Buffer
	if err := ReportTimeline(views).Render(context.Background(), &buf); err != nil {
		t.Fatalf("ReportTimeline render: %v", err)
	}
	html := buf.String()

	for _, want := range []string{
		`class="turn-card turn-user"`,
		`class="turn-card turn-assistant"`,
		`class="turn-role-label">User`,
		`class="turn-role-label">Assistant`,
		`<p>hello</p>`,
		`<p>hi there</p>`,
		`class="turn-reasoning-content"><p>thinking</p>`,
		`class="turn-llm-meta"`,
		`class="context-bar-segment"`,
		`class="tool-call-name">bash`,
		`&#34;cmd&#34;`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("ReportTimeline output missing %q:\n%s", want, html)
		}
	}
}

// TestReportPage_RendersHeaderSummary verifies the report page and summary
// sub-templates wire together and surface report metadata and termination.
func TestReportPage_RendersHeaderSummary(t *testing.T) {
	rep := &report.SessionReport{
		SessionID: "sess-1",
		Title:     "My Report",
		Model:     "gpt-4",
		Provider:  "test",
		Workspace: "/tmp/ws",
		Termination: &report.TerminationInfo{
			Reason:  "completed",
			Message: "done",
		},
		Summary: report.Summary{
			TotalTurns:           5,
			TotalToolCalls:       9,
			EstimatedTotalTokens: 1500,
		},
	}
	runs := []report.RunInfo{
		{Run: 0, Turns: 5, Termination: report.TerminationInfo{Reason: "completed"}},
	}

	var buf bytes.Buffer
	err := ReportPage(nil, "sess-1", nil, "/tmp/ws", "/report/sess-1", 0, "", false, 0, rep, runs, 0, nil).
		Render(context.Background(), &buf)
	if err != nil {
		t.Fatalf("ReportPage render: %v", err)
	}
	html := buf.String()

	for _, want := range []string{
		`<span class="report-title-text">My Report</span>`,
		`report-meta-label">Model`,
		"gpt-4",
		`report-meta-label">Turns`,
		`termination-chip termination-completed`,
		`class="report-timeline"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("ReportPage output missing %q:\n%s", want, html)
		}
	}
}

// TestReportNotFound verifies the not-found fragment renders.
func TestReportNotFound(t *testing.T) {
	var buf bytes.Buffer
	if err := ReportNotFound().Render(context.Background(), &buf); err != nil {
		t.Fatalf("ReportNotFound render: %v", err)
	}
	if !strings.Contains(buf.String(), "Session data not found") {
		t.Errorf("ReportNotFound missing message: %s", buf.String())
	}
}

package tui

import tea "charm.land/bubbletea/v2"

func projectStart(tx *Transcript, prompt string, thinking bool) {
	tx.Project(TranscriptOutcome{Start: &TranscriptStart{Prompt: prompt, ThinkingEnabled: thinking}})
}

func projectDone(tx *Transcript, msg turnDoneMsg) (bool, error) {
	return tx.Project(TranscriptOutcome{Complete: &TranscriptCompletion{
		Answer: msg.answer, Reasoning: msg.reasoning, Err: msg.err, Stopped: msg.stopped,
	}})
}

func projectStream(tx *Transcript, kind StreamKind, delta string) {
	tx.Project(TranscriptOutcome{Stream: &StreamUpdate{Kind: kind, Delta: delta}})
}

func projectTool(tx *Transcript, update ToolUpdate) {
	tx.Project(TranscriptOutcome{Tool: &update})
}

func beginTurn(s *TurnSession, tx *Transcript, prompt, payload string) tea.Cmd {
	projectStart(tx, prompt, s.ThinkingEnabled())
	return s.Dispatch(prompt, payload)
}

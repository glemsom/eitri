package tui

import "strings"

const completionCapRows = 8

// completionMenu owns interaction and rendering shared by composer completion
// surfaces. Candidate discovery and draft replacement remain with each surface.
type completionMenu struct {
	open   bool
	cands  []string
	view   []string
	offset int
	idx    int
}

func (m *completionMenu) isOpen() bool { return m.open }

func (m *completionMenu) Open(cands []string) {
	m.open = true
	m.cands = cands
	m.idx = 0
	m.offset = 0
	m.recomputeView()
}

func (m *completionMenu) SetCandidates(cands []string) {
	m.cands = cands
	if len(m.cands) > 0 && m.idx >= len(m.cands) {
		m.idx = len(m.cands) - 1
	}
	m.recomputeView()
}

func (m *completionMenu) Candidates() []string { return m.cands }

func (m *completionMenu) SelectedCandidate() string {
	if len(m.cands) == 0 {
		return ""
	}
	return m.cands[m.idx]
}

func (m *completionMenu) Move(delta int) {
	if !m.open || len(m.cands) == 0 {
		return
	}
	m.idx += delta
	if m.idx < 0 {
		m.idx = len(m.cands) - 1
	} else if m.idx >= len(m.cands) {
		m.idx = 0
	}
	m.recomputeView()
}

func (m *completionMenu) Accept() (string, bool) {
	if !m.open || len(m.cands) == 0 {
		return "", false
	}
	candidate := m.cands[m.idx]
	m.Dismiss()
	return candidate, true
}

func (m *completionMenu) Dismiss() {
	m.open = false
	m.cands = nil
	m.view = nil
	m.idx = 0
	m.offset = 0
}

func (m *completionMenu) CandidateCount() int { return len(m.view) }

func (m *completionMenu) RenderCompletion(b *strings.Builder, th Theme) {
	if !m.open {
		return
	}
	for i, candidate := range m.view {
		if m.offset+i == m.idx {
			b.WriteString(th.slashSelectStyle.Render(g("▸ ", "> ") + candidate))
		} else {
			b.WriteString(th.statusStyle.Render("  " + candidate))
		}
		b.WriteByte('\n')
	}
}

func (m *completionMenu) recomputeView() {
	if !m.open || len(m.cands) == 0 {
		m.view = nil
		m.offset = 0
		return
	}
	if m.idx < m.offset {
		m.offset = m.idx
	}
	if m.idx >= m.offset+completionCapRows {
		m.offset = m.idx - completionCapRows + 1
	}
	end := m.offset + completionCapRows
	if end > len(m.cands) {
		end = len(m.cands)
	}
	m.view = m.cands[m.offset:end]
}

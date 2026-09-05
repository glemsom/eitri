// Package session provides the on-disk run trail.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/glemsom/eitri/internal/provider"
)

const messagesName = "messages.jsonl"

type messageLog struct {
	mu   sync.Mutex
	dir  string
	file *os.File
}

func newMessageLog(dir string) *messageLog {
	return &messageLog{dir: dir}
}

func (m *messageLog) LogRequest(rec provider.RequestLog) {
	m.append(rec)
}

func (m *messageLog) LogResponse(rec provider.ResponseLog) {
	m.append(rec)
}

// append serializes rec as one JSON line and appends it, opening the file on first write. Write failures are swallowed: the debug trail must never take down a live run.
func (m *messageLog) append(rec any) {
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file == nil {
		f, err := os.OpenFile(filepath.Join(m.dir, messagesName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		m.file = f
	}
	_, _ = m.file.Write(append(line, '\n'))
}

func (s *Session) closeMessageLog() error {
	if s.messages == nil || s.messages.file == nil {
		return nil
	}
	err := s.messages.file.Close()
	s.messages.file = nil
	if err != nil {
		return fmt.Errorf("close %s: %w", messagesName, err)
	}
	return nil
}

// Package session — messages.go: the message-layer debug transcript. Every provider request/response cycle is appended as one JSON line to messages.jsonl in the session dir, giving a full, token-efficient ground-truth record of what the model saw and produced.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/glemsom/eitri/internal/provider"
)

// messagesName is the message-layer JSONL transcript inside a session dir.
const messagesName = "messages.jsonl"

// messageLog appends provider.MessageLogSink records to the session's messages.jsonl.
type messageLog struct {
	mu   sync.Mutex
	dir  string
	file *os.File
}

// newMessageLog creates the lazy-writer for messages.jsonl in dir.
func newMessageLog(dir string) *messageLog {
	return &messageLog{dir: dir}
}

// LogRequest implements provider.MessageLogSink.
func (m *messageLog) LogRequest(rec provider.RequestLog) {
	m.append(rec)
}

// LogResponse implements provider.MessageLogSink.
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

// closeMessageLog flushes and closes the JSONL file.
func (s *Session) closeMessageLog() error {
	if s.messages == nil {
		return nil
	}
	err := s.messages.file.Close()
	s.messages.file = nil
	if err != nil {
		return fmt.Errorf("close %s: %w", messagesName, err)
	}
	return nil
}

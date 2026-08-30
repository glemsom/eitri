// Package session provides the on-disk session trail: every run gets a GUID transcript directory under the data dir (sessions/<GUID>/), and debug mode attaches a pluggable HTTP trace sink for deep-dive provider debugging.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/glemsom/eitri/internal/provider"
)

const transcriptName = "transcript.md"

// TraceSink records full HTTP traffic to/from the provider.
type TraceSink interface {
	TraceRequest(body []byte)
	TraceResponse(body []byte)
}

type Session struct {
	dir      string
	data     *os.File
	trace    *fileTrace
	messages *messageLog
}

// New creates a session under dataDir/sessions/<GUID>, GUID-named so runs are unique and auditable. debug enables the HTTP trace sink.
func New(dataDir string, debug bool) (*Session, error) {
	guid, err := NewGUID()
	if err != nil {
		return nil, fmt.Errorf("generate session GUID: %w", err)
	}
	return NewWithGUID(dataDir, guid, debug)
}

// NewWithGUID creates a session under dataDir/sessions/guid. It is used by `/new`,
// where the TUI has already minted and displayed the fresh live key.
func NewWithGUID(dataDir, guid string, debug bool) (*Session, error) {
	dir := filepath.Join(dataDir, "sessions", guid)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create session dir %s: %w", dir, err)
	}

	s := &Session{dir: dir, messages: newMessageLog(dir)}
	if debug {
		s.trace = &fileTrace{dir: dir}
	}
	return s, nil
}

// MessageLogSink returns the message-layer JSONL sink for this session (always non-nil).
func (s *Session) MessageLogSink() provider.MessageLogSink {
	return s.messages
}

func (s *Session) GUID() string {
	return filepath.Base(s.dir)
}

func (s *Session) Dir() string {
	return s.dir
}

func (s *Session) TempDir() string {
	return filepath.Join(s.dir, "tmp")
}

// WriteTranscript appends a line to the session transcript file, creating it on first write.
func (s *Session) WriteTranscript(line []byte) error {
	if s.data == nil {
		f, err := os.OpenFile(filepath.Join(s.dir, transcriptName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("open transcript: %w", err)
		}
		s.data = f
	}
	if _, err := s.data.Write(line); err != nil {
		return fmt.Errorf("write transcript: %w", err)
	}
	return nil
}

// TraceSink returns the HTTP trace sink when debug mode is enabled, else nil.
func (s *Session) TraceSink() TraceSink {
	if s.trace == nil {
		return nil
	}
	return s.trace
}

func (s *Session) Close() error {
	if s.data != nil {
		_ = s.data.Close()
		s.data = nil
	}
	return s.closeMessageLog()
}

// fileTrace writes HTTP request/response bodies to sibling files in the session dir, one per direction.
type fileTrace struct {
	dir string
}

func (f *fileTrace) TraceRequest(body []byte) {
	appendFile(filepath.Join(f.dir, "trace-request.http"), body)
}

func (f *fileTrace) TraceResponse(body []byte) {
	appendFile(filepath.Join(f.dir, "trace-response.http"), body)
}

func appendFile(path string, body []byte) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(body)
	_, _ = f.Write([]byte{'\n'})
}

func NewGUID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

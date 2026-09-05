package provider

import (
	"context"
	"errors"
	"io"
	"time"
)

type RequestLog struct {
	Time     time.Time `json:"ts"`
	Dir      string    `json:"dir"` // always "req"
	Model    string    `json:"model,omitempty"`
	Messages []Message `json:"messages"`
	Tools    []string  `json:"tools,omitempty"`
}

type ResponseLog struct {
	Time             time.Time  `json:"ts"`
	Dir              string     `json:"dir"` // always "resp"
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	FinishReason     string     `json:"finish_reason,omitempty"`
	Usage            *Usage     `json:"usage,omitempty"`
	Error            string     `json:"error,omitempty"`
}

// MessageLogSink receives one record per provider request/response cycle, in order.
type MessageLogSink interface {
	LogRequest(rec RequestLog)
	LogResponse(rec ResponseLog)
}

func toolNames(tools []Tool) []string {
	if len(tools) == 0 {
		return nil
	}
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Function.Name
	}
	return names
}

// loggingStream wraps a Stream and emits a ResponseLog when the turn completes (done chunk) or fails.
type loggingStream struct {
	inner Stream
	sink  MessageLogSink

	content   string
	reasoning string
	usage     *Usage
	finish    string
	toolCalls []ToolCall
	errorText string
	emitted   bool
}

func (l *loggingStream) Next() (Chunk, error) {
	c, err := l.inner.Next()
	if err == nil {
		l.content += c.Content
		l.reasoning += c.ReasoningContent
		if c.Usage != nil {
			l.usage = c.Usage
		}
		if c.Done {
			l.finish = c.FinishReason
			l.toolCalls = c.ToolCalls
			l.emit()
		}
		return c, nil
	}
	if errors.Is(err, io.EOF) {
		// A clean EOF without a done chunk is still a completed turn on some wires; record it as such.
		if !l.emitted {
			l.finish = "eof"
			l.emit()
		}
		return c, err
	}
	l.errorText = err.Error()
	l.emit()
	return c, err
}

// emit pushes exactly one ResponseLog for this stream; idempotent per stream lifecycle.
func (l *loggingStream) emit() {
	if !l.emitted {
		l.sink.LogResponse(ResponseLog{
			Time: time.Now(), Dir: "resp",
			Content: l.content, ReasoningContent: l.reasoning,
			ToolCalls: l.toolCalls, FinishReason: l.finish,
			Usage: l.usage, Error: l.errorText,
		})
		l.emitted = true
	}
}

// LoggingProvider decorates p so every request/response cycle is mirrored to sink.
type LoggingProvider struct {
	inner Provider
	sink  MessageLogSink
}

// NewLoggingProvider wraps p with message-layer logging to sink.
func NewLoggingProvider(p Provider, sink MessageLogSink) *LoggingProvider {
	return &LoggingProvider{inner: p, sink: sink}
}

func (lp *LoggingProvider) SetSink(sink MessageLogSink) { lp.sink = sink }

func (lp *LoggingProvider) SupportedGenerationControls(ctx context.Context) ([]GenerationControl, error) {
	gp, ok := lp.inner.(GenerationControlProvider)
	if !ok {
		return nil, nil
	}
	return gp.SupportedGenerationControls(ctx)
}

func (lp *LoggingProvider) Stream(ctx context.Context, req Request) (Stream, error) {
	lp.sink.LogRequest(RequestLog{
		Time: time.Now(), Dir: "req",
		Model: req.Model, Messages: req.Messages,
		Tools: toolNames(req.Tools),
	})
	s, err := lp.inner.Stream(ctx, req)
	if err != nil {
		lp.sink.LogResponse(ResponseLog{Time: time.Now(), Dir: "resp", Error: err.Error()})
		return nil, err
	}
	return &loggingStream{inner: s, sink: lp.sink}, nil
}

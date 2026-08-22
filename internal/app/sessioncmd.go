// Package app — sessioncmd.go implements the `eitri session` debug subcommands: list, show, grep. They let an AI agent (or human) navigate the message-layer transcripts under sessions/ without loading whole files into context.
package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/glemsom/eitri/internal/provider"
)

// sessionCycle is one request/response pair from a messages.jsonl transcript.
type sessionCycle struct {
	Turn int // 1-based cycle index
	Req  *provider.RequestLog
	Resp *provider.ResponseLog
}

// readCycles parses messages.jsonl at path into ordered request/response cycles.
func readCycles(path string) ([]sessionCycle, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var cycles []sessionCycle
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	cur := sessionCycle{Turn: 1}
	flush := func() {
		if cur.Req != nil || cur.Resp != nil {
			cycles = append(cycles, cur)
			cur = sessionCycle{Turn: len(cycles) + 1}
		}
	}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Dir string `json:"dir"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		switch probe.Dir {
		case "req":
			flush()
			var r provider.RequestLog
			if json.Unmarshal(line, &r) == nil {
				rp := r
				cur.Req = &rp
			}
		case "resp":
			var r provider.ResponseLog
			if json.Unmarshal(line, &r) == nil {
				rp := r
				cur.Resp = &rp
				flush()
			}
		}
	}
	flush()
	return cycles, sc.Err()
}

// truncate shortens s to n runes with an ellipsis marker.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ListSessions prints one line per session dir: GUID, last activity, cycle count.
func ListSessions(dataDir string, out io.Writer) error {
	root := filepath.Join(dataDir, "sessions")
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("no sessions under %s: %w", root, err)
	}
	type row struct {
		guid    string
		modTime time.Time
		cycles  int
		model   string
	}
	var rows []row
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name(), "messages.jsonl")
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		cycles, err := readCycles(path)
		if err != nil || len(cycles) == 0 {
			continue
		}
		rows = append(rows, row{guid: e.Name(), modTime: fi.ModTime(), cycles: len(cycles), model: cycles[0].Req.Model})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].modTime.After(rows[j].modTime) })
	for _, r := range rows {
		fmt.Fprintf(out, "%s\t%s\t%d cycles\t%s\n", r.guid, r.modTime.Format(time.RFC3339), r.cycles, r.model)
	}
	return nil
}

// ShowSession prints a compact per-cycle summary of a session; with turn > 0 it prints only that cycle's full JSON records.
func ShowSession(dataDir, guid string, turn int, out io.Writer) error {
	cycles, err := readCycles(filepath.Join(dataDir, "sessions", guid, "messages.jsonl"))
	if err != nil {
		return fmt.Errorf("session %s unreadable: %w", guid, err)
	}
	if len(cycles) == 0 {
		return fmt.Errorf("session %s has no message records", guid)
	}
	for _, c := range cycles {
		if turn > 0 && c.Turn != turn {
			continue
		}
		if turn > 0 {
			writeCycleJSON(c, out)
			continue
		}
		var b strings.Builder
		if c.Req != nil {
			fmt.Fprintf(&b, "req model=%s msgs=%d", c.Req.Model, len(c.Req.Messages))
			if len(c.Req.Tools) > 0 {
				fmt.Fprintf(&b, " tools=%s", strings.Join(c.Req.Tools, ","))
			}
		}
		if c.Resp != nil {
			if b.Len() > 0 {
				b.WriteString(" | ")
			}
			fmt.Fprintf(&b, "resp finish=%s", c.Resp.FinishReason)
			if len(c.Resp.ToolCalls) > 0 {
				names := make([]string, len(c.Resp.ToolCalls))
				for i, tc := range c.Resp.ToolCalls {
					names[i] = tc.Name
				}
				fmt.Fprintf(&b, " calls=%s", strings.Join(names, ","))
			}
			if c.Resp.Usage != nil {
				fmt.Fprintf(&b, " tokens(in=%d,out=%d)", c.Resp.Usage.PromptTokens, c.Resp.Usage.CompletionTokens)
			}
			if c.Resp.Error != "" {
				fmt.Fprintf(&b, " ERROR=%s", truncate(c.Resp.Error, 120))
			}
			if c.Resp.Content != "" && c.Resp.FinishReason != "" {
				fmt.Fprintf(&b, "\n    └ %s", truncate(strings.ReplaceAll(c.Resp.Content, "\n", " "), 160))
			}
		}
		fmt.Fprintf(out, "[%d] %s\n", c.Turn, b.String())
	}
	return nil
}

// writeCycleJSON emits both records of one cycle as pretty-printed JSON for drill-down.
func writeCycleJSON(c sessionCycle, out io.Writer) {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if c.Req != nil {
		_ = enc.Encode(c.Req)
	}
	if c.Resp != nil {
		_ = enc.Encode(c.Resp)
	}
}

// GrepSession prints one compact line per cycle whose message-layer content matches substr, with a snippet around the first hit. Empty guid searches all sessions.
func GrepSession(dataDir, pattern, guid string, out io.Writer) error {
	root := filepath.Join(dataDir, "sessions")
	dirs := []string{filepath.Join(root, guid)}
	all := guid == "" || guid == "all"
	if all {
		entries, err := os.ReadDir(root)
		if err != nil {
			return fmt.Errorf("no sessions under %s: %w", root, err)
		}
		dirs = dirs[:0]
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(root, e.Name()))
			}
		}
		sort.Strings(dirs)
	}
	for _, dir := range dirs {
		cycles, err := readCycles(filepath.Join(dir, "messages.jsonl"))
		if err != nil {
			continue
		}
		tag := filepath.Base(dir)
		for _, c := range cycles {
			var hits []string
			if c.Req != nil {
				for i, m := range c.Req.Messages {
					for _, field := range []struct{ label, text string }{
						{"content", m.Content}, {"reasoning", m.ReasoningContent}, {"tool_call_id", m.ToolCallID},
					} {
						if s := snippet(field.text, pattern); s != "" {
							hits = append(hits, fmt.Sprintf("req.msg[%d].%s: %s", i, field.label, s))
						}
					}
					for _, tc := range m.ToolCalls {
						for _, field := range []struct{ label, text string }{{"tool", tc.Name}, {"args", tc.Arguments}} {
							if s := snippet(field.text, pattern); s != "" {
								hits = append(hits, fmt.Sprintf("req.msg[%d].%s: %s", i, field.label, s))
							}
						}
					}
				}
			}
			if c.Resp != nil {
				for _, field := range []struct{ label, text string }{
					{"content", c.Resp.Content}, {"reasoning", c.Resp.ReasoningContent}, {"error", c.Resp.Error},
				} {
					if s := snippet(field.text, pattern); s != "" {
						hits = append(hits, fmt.Sprintf("resp.%s: %s", field.label, s))
					}
				}
				for _, tc := range c.Resp.ToolCalls {
					for _, field := range []struct{ label, text string }{{"tool", tc.Name}, {"args", tc.Arguments}} {
						if s := snippet(field.text, pattern); s != "" {
							hits = append(hits, fmt.Sprintf("resp.%s: %s", field.label, s))
						}
					}
				}
			}
			for _, h := range hits {
				fmt.Fprintf(out, "%s:%d %s\n", tag, c.Turn, h)
			}
		}
	}
	return nil
}

// snippet returns up to ~120 chars of s centered on the first occurrence of pattern, or "" when absent.
func snippet(s, pattern string) string {
	idx := strings.Index(s, pattern)
	if idx < 0 {
		return ""
	}
	start := idx - 40
	if start < 0 {
		start = 0
	}
	end := start + 120
	if end > len(s) {
		end = len(s)
	}
	out := strings.ReplaceAll(s[start:end], "\n", " ")
	if start > 0 {
		out = "…" + out
	}
	if end < len(s) {
		out += "…"
	}
	return out
}

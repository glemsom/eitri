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
func ShowSession(dataDir, guid string, turn int, noReasoning bool, out io.Writer) error {
	cycles, err := readCycles(filepath.Join(dataDir, "sessions", guid, "messages.jsonl"))
	if err != nil {
		return fmt.Errorf("session %s unreadable: %w", guid, err)
	}
	if len(cycles) == 0 {
		return fmt.Errorf("session %s has no message records", guid)
	}
	if noReasoning {
		stripReasoning(cycles)
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

// stripReasoning drops chain-of-thought text in place: response reasoning_content and per-message reasoning_content on every cycle.
func stripReasoning(cycles []sessionCycle) {
	for i := range cycles {
		if c := cycles[i].Resp; c != nil {
			c.ReasoningContent = ""
		}
		if r := cycles[i].Req; r != nil {
			for j := range r.Messages {
				r.Messages[j].ReasoningContent = ""
			}
		}
	}
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

// TalkOptions controls what TalkSession renders.
type TalkOptions struct {
	FromTurn  int    // 1-based; 0 = from the start
	ToTurn    int    // inclusive; 0 = through the last cycle
	Role      string // "", "user", "assistant", "tool", or "system"
	Reasoning bool   // include chain-of-thought blocks
	AllCycles bool   // print every message of every request (default dedupes shared history)
}

// TalkSession prints a session's conversation as plain text, one block per message: `[N] role:` followed by the full untruncated content. Request history shared with the previous cycle is skipped unless AllCycles is set.
func TalkSession(dataDir, guid string, opts TalkOptions, out io.Writer) error {
	cycles, err := readCycles(filepath.Join(dataDir, "sessions", guid, "messages.jsonl"))
	if err != nil {
		return fmt.Errorf("session %s unreadable: %w", guid, err)
	}
	if len(cycles) == 0 {
		return fmt.Errorf("session %s has no message records", guid)
	}
	if !opts.Reasoning {
		stripReasoning(cycles)
	}
	var prevReqs [][]provider.Message
	for _, c := range cycles {
		n := c.Turn
		if opts.FromTurn > 0 && n < opts.FromTurn {
			continue
		}
		if opts.ToTurn > 0 && n > opts.ToTurn {
			break
		}
		if c.Req != nil {
			start := 0
			if !opts.AllCycles && len(prevReqs) > 0 {
				start = lenSharedPrefix(prevReqs[len(prevReqs)-1], c.Req.Messages)
			}
			for _, m := range c.Req.Messages[start:] {
				writeTalkMessage(n, string(m.Role), m, out, opts.Role)
			}
			prevReqs = append(prevReqs, c.Req.Messages)
		}
		if c.Resp != nil {
			if opts.Role == "" || opts.Role == "assistant" {
				fmt.Fprintf(out, "[%d] assistant:\n%s\n", n, indent(c.Resp.Content))
				if opts.Reasoning && c.Resp.ReasoningContent != "" {
					fmt.Fprintf(out, "[%d] assistant(reasoning):\n%s\n", n, indent(c.Resp.ReasoningContent))
				}
			}
			if (opts.Role == "" || opts.Role == "tool") && len(c.Resp.ToolCalls) > 0 {
				for _, tc := range c.Resp.ToolCalls {
					fmt.Fprintf(out, "[%d] assistant→tool %s(%s)\n", n, tc.Name, tc.Arguments)
				}
			}
			if c.Resp.Error != "" {
				fmt.Fprintf(out, "[%d] ERROR: %s\n", n, c.Resp.Error)
			}
		}
	}
	return nil
}

// lenSharedPrefix returns how many leading messages of cur are identical to prev's tail-aligned history, so Talk can skip already-printed context.
func lenSharedPrefix(prev, cur []provider.Message) int {
	// Requests resend the whole conversation, so prev is a prefix of cur in the common case.
	if len(cur) < len(prev) {
		return 0
	}
	for i := range prev {
		if prev[i].Role != cur[i].Role || prev[i].Content != cur[i].Content {
			return 0
		}
	}
	return len(prev)
}

// writeTalkMessage renders one request-side message if it passes the role filter.
func writeTalkMessage(turn int, role string, m provider.Message, out io.Writer, roleFilter string) {
	if roleFilter != "" && role != roleFilter {
		return
	}
	label := role
	if m.ToolCallID != "" {
		label = fmt.Sprintf("%s(%s)", role, truncate(m.ToolCallID, 16))
	}
	fmt.Fprintf(out, "[%d] %s:\n%s\n", turn, label, indent(m.Content))
	if m.ReasoningContent != "" {
		fmt.Fprintf(out, "[%d] %s(reasoning):\n%s\n", turn, label, indent(m.ReasoningContent))
	}
	for _, tc := range m.ToolCalls {
		fmt.Fprintf(out, "[%d] %s→tool %s(%s)\n", turn, label, tc.Name, tc.Arguments)
	}
}

// indent indents every line of s by four spaces so message bodies sit under their `[N] role:` header; empty bodies render as an empty line.
func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}

// GrepSession prints one compact line per cycle whose message-layer content matches substr — snippets around each hit, or full field text when full is set. Empty guid searches all sessions.
func GrepSession(dataDir, pattern, guid string, full bool, out io.Writer) error {
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
		var hits []string
		hit := func(label, text string) {
			if !strings.Contains(text, pattern) {
				return
			}
			if full {
				hits = append(hits, fmt.Sprintf("%s:\n%s", label, indent(text)))
			} else if s := snippet(text, pattern); s != "" {
				hits = append(hits, fmt.Sprintf("%s: %s", label, s))
			}
		}
		for _, c := range cycles {
			hits = hits[:0]
			if c.Req != nil {
				for i, m := range c.Req.Messages {
					hit(fmt.Sprintf("req.msg[%d].content", i), m.Content)
					hit(fmt.Sprintf("req.msg[%d].reasoning", i), m.ReasoningContent)
					hit(fmt.Sprintf("req.msg[%d].tool_call_id", i), m.ToolCallID)
					for _, tc := range m.ToolCalls {
						hit(fmt.Sprintf("req.msg[%d].tool", i), tc.Name)
						hit(fmt.Sprintf("req.msg[%d].args", i), tc.Arguments)
					}
				}
			}
			if c.Resp != nil {
				hit("resp.content", c.Resp.Content)
				hit("resp.reasoning", c.Resp.ReasoningContent)
				hit("resp.error", c.Resp.Error)
				for _, tc := range c.Resp.ToolCalls {
					hit("resp.tool", tc.Name)
					hit("resp.args", tc.Arguments)
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

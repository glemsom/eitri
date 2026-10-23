package app

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/session"
)

type turnToolCall struct {
	Turn      int
	Name      string
	Args      string
	Result    string
	HasResult bool
	Err       bool
}

type sessionAgg struct {
	GUID             string
	ModTime          time.Time
	Model            string
	Turns            int
	PromptTokens     int
	CompletionTokens int
	CacheHitTokens   int
	CacheMissTokens  int
	CacheWriteTokens int
	Errors           int
	FirstTS          time.Time
	LastTS           time.Time
	Finish           map[string]int
	Outcome          string
	Calls            []turnToolCall
}

// aggregateSession folds a session's turns into one sessionAgg. Tool results
// are answered in the *next* turn's request messages, keyed by call id, so it
// indexes every tool message first and then joins the response-side calls.
func aggregateSession(guid string, mod time.Time, turns []sessionTurn, ends []provider.RunEndLog) sessionAgg {
	agg := sessionAgg{GUID: guid, ModTime: mod, Turns: len(turns), Finish: map[string]int{}}
	if len(ends) > 0 {
		agg.Outcome = ends[len(ends)-1].Reason
	}
	results := map[string]string{}
	for _, c := range turns {
		if c.Req == nil {
			continue
		}
		for _, m := range c.Req.Messages {
			if m.Role == provider.RoleTool && m.ToolCallID != "" {
				results[m.ToolCallID] = m.Content
			}
		}
	}
	for _, c := range turns {
		if c.Req != nil {
			if agg.Model == "" {
				agg.Model = c.Req.Model
			}
			if agg.FirstTS.IsZero() || c.Req.Time.Before(agg.FirstTS) {
				agg.FirstTS = c.Req.Time
			}
		}
		if c.Resp == nil {
			continue
		}
		if c.Resp.Time.After(agg.LastTS) {
			agg.LastTS = c.Resp.Time
		}
		if u := c.Resp.Usage; u != nil {
			agg.PromptTokens += u.PromptTokens
			agg.CompletionTokens += u.CompletionTokens
			agg.CacheHitTokens += u.PromptCacheHitTokens
			agg.CacheMissTokens += u.PromptCacheMissTokens
			agg.CacheWriteTokens += u.PromptCacheWriteTokens
		}
		if c.Resp.Error != "" {
			agg.Errors++
		}
		if c.Resp.FinishReason != "" {
			agg.Finish[c.Resp.FinishReason]++
		}
		for _, tc := range c.Resp.ToolCalls {
			call := turnToolCall{Turn: c.Turn, Name: tc.Name, Args: tc.Arguments}
			if r, ok := results[tc.ID]; ok {
				call.Result, call.HasResult = r, true
				call.Err = toolResultError(r)
			}
			agg.Calls = append(agg.Calls, call)
		}
	}
	return agg
}

// toolResultError reports whether a tool result text is the engine's failure
// form. The engine prefixes genuine tool failures with "error executing tool:"
// and rejections with "invalid tool arguments:" or an INVALID_JSON marker.
func toolResultError(text string) bool {
	return strings.HasPrefix(text, "error executing tool:") ||
		strings.HasPrefix(text, "invalid tool arguments:") ||
		strings.HasPrefix(text, `{"INVALID_JSON"`)
}

// readAllSessions aggregates every session under dataDir/sessions, newest first
// (by transcript mtime, matching `session list`). Sessions without a
// messages.jsonl are skipped.
func readAllSessions(dataDir string) ([]sessionAgg, error) {
	root := filepath.Join(dataDir, "sessions")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("no sessions under %s: %w", root, err)
	}
	var aggs []sessionAgg
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name(), "messages.jsonl")
		fi, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("session %s unreadable: %w", e.Name(), err)
		}
		turns, ends, err := readTurns(path)
		if err != nil {
			return nil, fmt.Errorf("session %s unreadable: %w", e.Name(), err)
		}
		if len(turns) == 0 {
			continue
		}
		aggs = append(aggs, aggregateSession(e.Name(), fi.ModTime(), turns, ends))
	}
	sort.Slice(aggs, func(i, j int) bool { return aggs[i].ModTime.After(aggs[j].ModTime) })
	return aggs, nil
}

func readOneSession(dataDir, guid string) (sessionAgg, error) {
	if err := session.ValidateGUID(guid); err != nil {
		return sessionAgg{}, err
	}
	path := filepath.Join(dataDir, "sessions", guid, "messages.jsonl")
	fi, err := os.Stat(path)
	if err != nil {
		return sessionAgg{}, fmt.Errorf("session %s unreadable: %w", guid, err)
	}
	turns, ends, err := readTurns(path)
	if err != nil {
		return sessionAgg{}, fmt.Errorf("session %s unreadable: %w", guid, err)
	}
	return aggregateSession(guid, fi.ModTime(), turns, ends), nil
}

// collectSessions resolves the guid operand shared by stats and tools: "all"
// or "" means every session, otherwise the one named.
func collectSessions(dataDir, guid string) ([]sessionAgg, bool, error) {
	if guid == "" || guid == "all" {
		aggs, err := readAllSessions(dataDir)
		return aggs, true, err
	}
	agg, err := readOneSession(dataDir, guid)
	if err != nil {
		return nil, false, err
	}
	return []sessionAgg{agg}, false, nil
}

func cacheRatio(hit, miss int) string {
	if hit+miss == 0 {
		return ""
	}
	return fmt.Sprintf("%d%%", int(math.Round(100*float64(hit)/float64(hit+miss))))
}

func spanString(a sessionAgg) string {
	if a.FirstTS.IsZero() || !a.LastTS.After(a.FirstTS) {
		return ""
	}
	return a.LastTS.Sub(a.FirstTS).Round(time.Second).String()
}

// toolCounts rolls the tool calls up per name, most-called first then by name.
func toolCounts(calls []turnToolCall) []toolCountJSON {
	byName := map[string]*toolCountJSON{}
	var order []string
	for _, c := range calls {
		s, ok := byName[c.Name]
		if !ok {
			s = &toolCountJSON{Name: c.Name}
			byName[c.Name] = s
			order = append(order, c.Name)
		}
		s.Calls++
		if c.Err {
			s.Errors++
		}
	}
	out := make([]toolCountJSON, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func countErrors(calls []turnToolCall) int {
	n := 0
	for _, c := range calls {
		if c.Err {
			n++
		}
	}
	return n
}

// StatsSession prints aggregate statistics: one row per session when all is
// true, a full breakdown otherwise. format is "text" or "json".
func StatsSession(dataDir, guid, format string, out io.Writer) error {
	aggs, all, err := collectSessions(dataDir, guid)
	if err != nil {
		return err
	}
	if format == "json" {
		return writeStatsJSON(aggs, all, out)
	}
	return writeStatsText(aggs, all, out)
}

func writeStatsText(aggs []sessionAgg, all bool, out io.Writer) error {
	if len(aggs) == 0 {
		_, err := io.WriteString(out, "no sessions found\n")
		return err
	}
	var b strings.Builder
	if all {
		for _, a := range aggs {
			fmt.Fprintf(&b, "%s\t%d turns\tin=%d out=%d", a.GUID, a.Turns, a.PromptTokens, a.CompletionTokens)
			if r := cacheRatio(a.CacheHitTokens, a.CacheMissTokens); r != "" {
				fmt.Fprintf(&b, "\tcache=%s", r)
			}
			fmt.Fprintf(&b, "\tcalls=%d", len(a.Calls))
			if a.Errors > 0 {
				fmt.Fprintf(&b, "\terrors=%d", a.Errors)
			}
			if s := spanString(a); s != "" {
				fmt.Fprintf(&b, "\t%s", s)
			}
			if a.Model != "" {
				fmt.Fprintf(&b, "\t%s", a.Model)
			}
			if a.Outcome != "" {
				fmt.Fprintf(&b, "\toutcome=%s", a.Outcome)
			}
			b.WriteByte('\n')
		}
	} else {
		writeStatsDetail(&b, aggs[0])
	}
	_, err := io.WriteString(out, b.String())
	return err
}

func writeStatsDetail(b *strings.Builder, a sessionAgg) {
	if a.Turns == 0 {
		fmt.Fprintf(b, "session %s has no records\n", a.GUID)
		return
	}
	fmt.Fprintf(b, "session %s\n", a.GUID)
	head := fmt.Sprintf("model %s   turns %d", a.Model, a.Turns)
	if s := spanString(a); s != "" {
		head += "   span " + s
	}
	fmt.Fprintf(b, "%s\n", head)
	if a.Outcome != "" {
		fmt.Fprintf(b, "outcome %s\n", a.Outcome)
	}
	tokens := fmt.Sprintf("tokens in=%d out=%d", a.PromptTokens, a.CompletionTokens)
	if a.CacheHitTokens+a.CacheMissTokens+a.CacheWriteTokens > 0 {
		tokens += fmt.Sprintf(" cache_hit=%d cache_miss=%d cache_write=%d", a.CacheHitTokens, a.CacheMissTokens, a.CacheWriteTokens)
		if r := cacheRatio(a.CacheHitTokens, a.CacheMissTokens); r != "" {
			tokens += " hit=" + r
		}
	}
	fmt.Fprintf(b, "%s\n", tokens)
	fmt.Fprintf(b, "errors %d\n", a.Errors)
	fmt.Fprintf(b, "tool calls %d (%d errors)\n", len(a.Calls), countErrors(a.Calls))
	if counts := toolCounts(a.Calls); len(counts) > 0 {
		parts := make([]string, len(counts))
		for i, tc := range counts {
			if tc.Errors > 0 {
				parts[i] = fmt.Sprintf("%s=%d (%d errors)", tc.Name, tc.Calls, tc.Errors)
			} else {
				parts[i] = fmt.Sprintf("%s=%d", tc.Name, tc.Calls)
			}
		}
		fmt.Fprintf(b, "tools %s\n", strings.Join(parts, " "))
	}
	if len(a.Finish) > 0 {
		reasons := make([]string, 0, len(a.Finish))
		for r := range a.Finish {
			reasons = append(reasons, r)
		}
		sort.Strings(reasons)
		parts := make([]string, len(reasons))
		for i, r := range reasons {
			parts[i] = fmt.Sprintf("%s=%d", r, a.Finish[r])
		}
		fmt.Fprintf(b, "finish %s\n", strings.Join(parts, " "))
	}
}

type toolCountJSON struct {
	Name   string `json:"name"`
	Calls  int    `json:"calls"`
	Errors int    `json:"errors"`
}

type sessionStatsJSON struct {
	GUID             string          `json:"guid"`
	Model            string          `json:"model,omitempty"`
	Turns            int             `json:"turns"`
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	CacheHitTokens   int             `json:"cache_hit_tokens,omitempty"`
	CacheMissTokens  int             `json:"cache_miss_tokens,omitempty"`
	CacheWriteTokens int             `json:"cache_write_tokens,omitempty"`
	Errors           int             `json:"errors"`
	ToolCalls        int             `json:"tool_calls"`
	ToolErrors       int             `json:"tool_errors"`
	FirstTS          string          `json:"first_ts,omitempty"`
	LastTS           string          `json:"last_ts,omitempty"`
	SpanSeconds      float64         `json:"span_seconds"`
	FinishReasons    map[string]int  `json:"finish_reasons,omitempty"`
	Outcome          string          `json:"outcome,omitempty"`
	Tools            []toolCountJSON `json:"tools,omitempty"`
}

func toStatsJSON(a sessionAgg) sessionStatsJSON {
	s := sessionStatsJSON{
		GUID: a.GUID, Model: a.Model, Turns: a.Turns,
		PromptTokens: a.PromptTokens, CompletionTokens: a.CompletionTokens,
		CacheHitTokens: a.CacheHitTokens, CacheMissTokens: a.CacheMissTokens,
		CacheWriteTokens: a.CacheWriteTokens,
		Errors:           a.Errors, ToolCalls: len(a.Calls), ToolErrors: countErrors(a.Calls),
		FinishReasons: a.Finish,
		Outcome:       a.Outcome,
		Tools:         toolCounts(a.Calls),
	}
	if !a.FirstTS.IsZero() {
		s.FirstTS = a.FirstTS.Format(time.RFC3339)
	}
	if !a.LastTS.IsZero() {
		s.LastTS = a.LastTS.Format(time.RFC3339)
	}
	if a.LastTS.After(a.FirstTS) {
		s.SpanSeconds = a.LastTS.Sub(a.FirstTS).Seconds()
	}
	return s
}

func writeStatsJSON(aggs []sessionAgg, all bool, out io.Writer) error {
	enc := json.NewEncoder(out)
	if all {
		rows := make([]sessionStatsJSON, len(aggs))
		for i, a := range aggs {
			rows[i] = toStatsJSON(a)
		}
		return enc.Encode(struct {
			Sessions []sessionStatsJSON `json:"sessions"`
		}{Sessions: rows})
	}
	var row sessionStatsJSON
	if len(aggs) > 0 {
		row = toStatsJSON(aggs[0])
	}
	return enc.Encode(row)
}

// ToolsSession lists tool calls, joined to their results, filtered by tool
// name and turn when given. format is "text" or "json".
func ToolsSession(dataDir, guid, name string, turn int, format string, out io.Writer) error {
	aggs, all, err := collectSessions(dataDir, guid)
	if err != nil {
		return err
	}
	if format == "json" {
		return writeToolsJSON(aggs, all, name, turn, out)
	}
	return writeToolsText(aggs, all, name, turn, out)
}

func filterCalls(calls []turnToolCall, name string, turn int) []turnToolCall {
	out := make([]turnToolCall, 0, len(calls))
	for _, c := range calls {
		if name != "" && c.Name != name {
			continue
		}
		if turn > 0 && c.Turn != turn {
			continue
		}
		out = append(out, c)
	}
	return out
}

func callStatus(c turnToolCall) string {
	switch {
	case !c.HasResult:
		return "no-result"
	case c.Err:
		return "error"
	default:
		return "ok"
	}
}

func writeToolsText(aggs []sessionAgg, all bool, name string, turn int, out io.Writer) error {
	var b strings.Builder
	total := 0
	for _, a := range aggs {
		calls := filterCalls(a.Calls, name, turn)
		if len(calls) == 0 {
			continue
		}
		total += len(calls)
		for _, c := range calls {
			prefix := ""
			if all {
				prefix = a.GUID + "\t"
			}
			fmt.Fprintf(&b, "%s[%d] %s %s %dB  %s\n",
				prefix, c.Turn, c.Name, callStatus(c), len(c.Result), truncate(strings.ReplaceAll(c.Args, "\n", " "), 80))
		}
		if counts := toolCounts(calls); len(counts) > 0 {
			parts := make([]string, len(counts))
			for i, tc := range counts {
				parts[i] = fmt.Sprintf("%s=%d", tc.Name, tc.Calls)
				if tc.Errors > 0 {
					parts[i] += fmt.Sprintf("(%d errors)", tc.Errors)
				}
			}
			tag := ""
			if all {
				tag = a.GUID + " "
			}
			fmt.Fprintf(&b, "%s── %d calls (%d errors): %s\n", tag, len(calls), countErrors(calls), strings.Join(parts, " "))
		}
	}
	if total == 0 {
		b.WriteString("no tool calls matched\n")
	}
	_, err := io.WriteString(out, b.String())
	return err
}

type toolCallJSON struct {
	GUID        string `json:"guid,omitempty"`
	Turn        int    `json:"turn"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Args        string `json:"args,omitempty"`
	ResultBytes int    `json:"result_bytes"`
}

func writeToolsJSON(aggs []sessionAgg, all bool, name string, turn int, out io.Writer) error {
	calls := make([]toolCallJSON, 0)
	var flat []turnToolCall
	for _, a := range aggs {
		for _, c := range filterCalls(a.Calls, name, turn) {
			row := toolCallJSON{Turn: c.Turn, Name: c.Name, Status: callStatus(c), Args: c.Args, ResultBytes: len(c.Result)}
			if all {
				row.GUID = a.GUID
			}
			calls = append(calls, row)
			flat = append(flat, c)
		}
	}
	enc := json.NewEncoder(out)
	return enc.Encode(struct {
		Calls   []toolCallJSON  `json:"calls"`
		Summary []toolCountJSON `json:"summary,omitempty"`
	}{Calls: calls, Summary: toolCounts(flat)})
}

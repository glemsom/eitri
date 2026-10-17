package provider

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestAnthropicBuildShapesMessagesBody(t *testing.T) {
	t.Parallel()
	req := Request{
		Model:           "qwen3.8-flash",
		ThinkingEnabled: false,
		Messages: []Message{
			{Role: RoleSystem, Content: "head"},
			{Role: RoleSystem, Content: "workspace"},
			{Role: RoleUser, Content: "hello"},
			{Role: RoleAssistant, Content: "hi", ReasoningContent: "ponder", ToolCalls: []ToolCall{
				{ID: "toolu_1", Type: "function", Name: "read", Arguments: `{"path":"a.txt"}`},
			}},
			{Role: RoleTool, ToolCallID: "toolu_1", Content: "file: abc"},
		},
		Tools: []Tool{
			{Type: "function", Function: ToolFunction{Name: "read", Description: "read a file", Parameters: map[string]any{"type": "object"}}},
		},
	}
	data, err := NewAnthropicDialect().Build(req)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body["model"] != "qwen3.8-flash" {
		t.Errorf("model = %v", body["model"])
	}
	if mt := body["max_tokens"]; int(mt.(float64)) != anthropicMaxTokensDefault {
		t.Errorf("max_tokens = %v, want default %d", mt, anthropicMaxTokensDefault)
	}
	if body["stream"] != true {
		t.Errorf("stream = %v, want true", body["stream"])
	}
	// thinking disabled because the turn has thinking off
	if tk, ok := body["thinking"].(map[string]any); !ok || tk["type"] != "disabled" {
		t.Errorf("thinking = %v, want {type:disabled}", body["thinking"])
	}
	// system blocks hoisted to the top-level system field
	sys, _ := body["system"].([]any)
	if len(sys) != 2 {
		t.Fatalf("system len = %d, want 2", len(sys))
	}
	for i, blk := range sys {
		b := blk.(map[string]any)
		if b["type"] != "text" || b["text"] != []string{"head", "workspace"}[i] {
			t.Errorf("system block %d = %v", i, b)
		}
	}
	// conversational timeline: user, assistant(tool_use), user(tool_result)
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages len = %d, want 3", len(msgs))
	}
	assistant := msgs[1].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Errorf("assistant role = %v", assistant["role"])
	}
	content := assistant["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("assistant content len = %d, want 2", len(content))
	}
	if c := content[0].(map[string]any); c["type"] != "text" || c["text"] != "hi" {
		t.Errorf("assistant block[0] = %v", c)
	}
	tu := content[1].(map[string]any)
	if tu["type"] != "tool_use" || tu["id"] != "toolu_1" || tu["name"] != "read" {
		t.Errorf("assistant tool_use = %v", tu)
	}
	if tu["input"].(map[string]any)["path"] != "a.txt" {
		t.Errorf("tool_use input = %v", tu["input"])
	}
	// tool result rides as a user message
	tr := msgs[2].(map[string]any)
	if tr["role"] != "user" {
		t.Errorf("tool result role = %v", tr["role"])
	}
	tb := tr["content"].([]any)[0].(map[string]any)
	if tb["type"] != "tool_result" || tb["tool_use_id"] != "toolu_1" || tb["content"] != "file: abc" {
		t.Errorf("tool_result = %v", tb)
	}
	// tools manifest
	tools := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "read" || tool["input_schema"].(map[string]any)["type"] != "object" {
		t.Errorf("tool = %v", tool)
	}
}

func TestAnthropicDialectBuildOmitsCacheControlWithoutCapability(t *testing.T) {
	t.Parallel()
	marker := &CacheControl{Type: "ephemeral", TTL: "1h"}
	body, err := NewAnthropicDialect().Build(Request{
		Messages: []Message{{Role: RoleSystem, Content: "prefix", CacheControl: marker}},
		Tools:    []Tool{{Type: "function", Function: ToolFunction{Name: "bash"}, CacheControl: marker}},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if strings.Contains(string(body), "cache_control") {
		t.Errorf("request leaked cache_control without capability: %s", body)
	}
}

func TestAnthropicBuildOmitsUnsignedThinkingFromCanonicalHistory(t *testing.T) {
	t.Parallel()
	data, err := NewAnthropicDialect().Build(Request{Messages: []Message{{
		Role:             RoleAssistant,
		ReasoningContent: "private reasoning emitted by an earlier stream",
		Content:          "visible answer",
	}}})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	content := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("assistant content = %#v, want only replayable content", content)
	}
	block := content[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "visible answer" {
		t.Errorf("assistant content = %#v, want text-only visible answer", content)
	}
}

func TestAnthropicBuildReplaysSignedThinkingBeforeToolUse(t *testing.T) {
	t.Parallel()
	data, err := NewAnthropicDialect().Build(Request{Messages: []Message{{
		Role:              RoleAssistant,
		ReasoningContent:  "inspect the workspace",
		ThinkingSignature: "opaque-anthropic-signature",
		ToolCalls: []ToolCall{{
			ID: "toolu_1", Type: "function", Name: "bash", Arguments: `{"command":"pwd"}`,
		}},
	}}})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	content := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("assistant content = %#v, want signed thinking then tool_use", content)
	}
	thinking := content[0].(map[string]any)
	if thinking["type"] != "thinking" || thinking["thinking"] != "inspect the workspace" || thinking["signature"] != "opaque-anthropic-signature" {
		t.Errorf("thinking block = %#v, want preserved signed thinking", thinking)
	}
	if tool := content[1].(map[string]any); tool["type"] != "tool_use" || tool["id"] != "toolu_1" {
		t.Errorf("tool block = %#v, want tool_use after thinking", tool)
	}
}

func TestAnthropicBuildMaxTokensRespectsRequest(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		requested int
		want      int
	}{
		{0, anthropicMaxTokensDefault}, // unset -> the required default
		{4096, 4096},
	} {
		data, err := NewAnthropicDialect().Build(Request{Model: "qwen3.8-flash", MaxOutputTokens: tc.requested})
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if mt := int(body["max_tokens"].(float64)); mt != tc.want {
			t.Errorf("Request MaxOutputTokens=%d: max_tokens = %d, want %d", tc.requested, mt, tc.want)
		}
	}
}

func TestAnthropicStreamParsesThinkingToolUseAndUsage(t *testing.T) {
	t.Parallel()
	sse := `event: ping
data: {"type":"ping"}

event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"qwen3.8-flash","content":[],"usage":{"input_tokens":40,"output_tokens":0,"cache_creation_input_tokens":10,"cache_read_input_tokens":30}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" think"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_opaque"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"a.txt\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":22}}

event: message_stop
data: {"type":"message_stop"}
`
	s := NewAnthropicDialect().Stream(strings.NewReader(sse))
	var reasoning string
	var done Chunk
	for {
		c, err := s.Next()
		if errors.Is(err, io.EOF) {
			t.Fatalf("stream ended without Done chunk")
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		reasoning += c.ReasoningContent
		if c.Done {
			done = c
			break
		}
	}
	if reasoning != "let me think" {
		t.Errorf("reasoning = %q, want %q", reasoning, "let me think")
	}
	if done.ThinkingSignature != "sig_opaque" {
		t.Errorf("thinking signature = %q, want opaque SSE signature", done.ThinkingSignature)
	}
	if done.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", done.FinishReason)
	}
	if len(done.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(done.ToolCalls))
	}
	tc := done.ToolCalls[0]
	if tc.ID != "toolu_1" || tc.Name != "read_file" || tc.Arguments != `{"path":"a.txt"}` {
		t.Errorf("tool call = %+v", tc)
	}
	if done.Usage == nil {
		t.Fatal("usage nil, want telemetry")
	}
	if done.Usage.PromptTokens != 40 || done.Usage.CompletionTokens != 22 {
		t.Errorf("usage = %+v", done.Usage)
	}
	if done.Usage.PromptCacheHitTokens != 30 || done.Usage.PromptCacheMissTokens != 10 {
		t.Errorf("usage cache = %+v", done.Usage)
	}
}

func TestAnthropicStreamTextOnlyEndTurn(t *testing.T) {
	t.Parallel()
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"qwen3.8-flash","content":[],"usage":{"input_tokens":5,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}

event: message_stop
data: {"type":"message_stop"}
`
	answer, usage, err := consume(NewAnthropicDialect().Stream(strings.NewReader(sse)))
	if err != nil {
		t.Fatalf("consume error = %v", err)
	}
	if answer != "hello" {
		t.Errorf("answer = %q, want hello", answer)
	}
	if usage == nil || usage.PromptTokens != 5 || usage.CompletionTokens != 3 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestOpenCodeAnthropicModel(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"qwen3.6-plus":      true,
		"qwen3.7-max":       true,
		"qwen3.7-plus":      true,
		"qwen3.8-max":       true,
		"qwen3.8-flash":     true,
		"minimax-m3":        true,
		"minimax-m2.7":      true,
		"minimax-m2.5":      true,
		"union-alpha":       false,
		"qwen3.9-preview":   false,
		"deepseek-v4-flash": false,
		"glm-5.3":           false,
		"kimi-k3":           false,
		"hy3":               false,
	}
	for model, want := range cases {
		if got := openCodeAnthropicModel(model); got != want {
			t.Errorf("openCodeAnthropicModel(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestAnthropicStopReason(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"end_turn":      "stop",
		"stop_sequence": "stop",
		"max_tokens":    "length",
		"tool_use":      "tool_calls",
		"":              "stop",
	}
	for in, want := range cases {
		if got := anthropicStopReason(in); got != want {
			t.Errorf("anthropicStopReason(%q) = %q, want %q", in, got, want)
		}
	}
}

// buildAnthropicBody builds the Messages wire body for req.
func buildAnthropicBody(t *testing.T, req Request) string {
	t.Helper()
	body, err := NewAnthropicDialect().Build(req)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return string(body)
}

// The static prefix is what the caller says is byte-identical across runs, so a
// breakpoint belongs on its last message; the per-run directive after it must
// stay unmarked, or its volatile content lands in every cache hash.
func TestAnthropicDialectStampsStaticPrefixEndAndTail(t *testing.T) {
	t.Parallel()
	body := buildAnthropicBody(t, Request{
		Model:           "some-model",
		StaticPrefixLen: 3,
		Capabilities:    ProviderCapabilities(ProviderCapabilityCacheControl),
		Messages: []Message{
			{Role: RoleSystem, Content: "persona head"},
			{Role: RoleSystem, Content: "skill index"},
			{Role: RoleSystem, Content: "repo instructions"},
			{Role: RoleSystem, Content: "per-run workspace directive"},
			{Role: RoleUser, Content: "u1"},
			{Role: RoleAssistant, Content: "a1"},
			{Role: RoleUser, Content: "u2"},
		},
	})
	if got, want := anthropicCacheBlocks(t, body), []string{"system[2]", "messages[2].content[0]"}; !equalStrings(got, want) {
		t.Fatalf("cache_control blocks = %v, want %v", got, want)
	}
}

// Without a declared prefix the leading run of system messages stands in for it.
func TestAnthropicDialectFallsBackToLeadingSystemMessages(t *testing.T) {
	t.Parallel()
	body := buildAnthropicBody(t, Request{
		Capabilities: ProviderCapabilities(ProviderCapabilityCacheControl),
		Messages: []Message{
			{Role: RoleSystem, Content: "s1"},
			{Role: RoleSystem, Content: "s2"},
			{Role: RoleUser, Content: "u1"},
		},
	})
	if got, want := anthropicCacheBlocks(t, body), []string{"system[1]", "messages[0].content[0]"}; !equalStrings(got, want) {
		t.Fatalf("cache_control blocks = %v, want %v", got, want)
	}
}

// A prefix covering the whole request leaves one breakpoint, not two on one block.
func TestAnthropicDialectStampsOnceWhenTailIsThePrefixEnd(t *testing.T) {
	t.Parallel()
	body := buildAnthropicBody(t, Request{
		Capabilities:    ProviderCapabilities(ProviderCapabilityCacheControl),
		StaticPrefixLen: 1,
		Messages:        []Message{{Role: RoleSystem, Content: "s1"}},
	})
	if got, want := anthropicCacheBlocks(t, body), []string{"system[0]"}; !equalStrings(got, want) {
		t.Fatalf("cache_control blocks = %v, want %v", got, want)
	}
}

// A declared prefix longer than the message list is a caller bug, not a crash:
// skip it and still mark the tail.
func TestAnthropicDialectIgnoresOutOfRangeStaticPrefix(t *testing.T) {
	t.Parallel()
	body := buildAnthropicBody(t, Request{
		Capabilities:    ProviderCapabilities(ProviderCapabilityCacheControl),
		StaticPrefixLen: 9,
		Messages: []Message{
			{Role: RoleSystem, Content: "s1"},
			{Role: RoleUser, Content: "u1"},
		},
	})
	if got, want := anthropicCacheBlocks(t, body), []string{"messages[0].content[0]"}; !equalStrings(got, want) {
		t.Fatalf("cache_control blocks = %v, want %v", got, want)
	}
}

// The last tool definition carries the breakpoint that makes the tools + system
// prefix one cached span; earlier tools stay bare so only one span is written.
func TestAnthropicDialectStampsLastToolOnly(t *testing.T) {
	t.Parallel()
	body := buildAnthropicBody(t, Request{
		Capabilities: ProviderCapabilities(ProviderCapabilityCacheControl),
		Messages:     []Message{{Role: RoleUser, Content: "u1"}},
		Tools: []Tool{
			{Type: "function", Function: ToolFunction{Name: "bash", Parameters: map[string]any{"type": "object"}}},
			{Type: "function", Function: ToolFunction{Name: "read_file", Parameters: map[string]any{"type": "object"}}},
		},
		MaxOutputTokens: 64,
	})
	got := anthropicCacheBlocks(t, body)
	if want := []string{"messages[0].content[0]", "tools[1]"}; !equalStrings(got, want) {
		t.Fatalf("cache_control blocks = %v, want %v", got, want)
	}
}

// Anthropic caps a request at four breakpoints, counting any the caller already
// placed: the tool manifest is only stamped while the budget allows.
func TestAnthropicDialectRespectsCacheBreakpointBudget(t *testing.T) {
	t.Parallel()
	marker := &CacheControl{Type: "ephemeral"}
	for _, tc := range []struct {
		name   string
		marked []int
		want   []string
	}{
		{
			name:   "caller markers on head and tail leave room for the tool manifest",
			marked: []int{0, 3},
			want:   []string{"system[0]", "messages[2].content[0]", "tools[0]"},
		},
		{
			name:   "four caller markers leave no room for the tool manifest",
			marked: []int{0, 1, 2, 3},
			want:   []string{"system[0]", "messages[0].content[0]", "messages[1].content[0]", "messages[2].content[0]"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := []Message{
				{Role: RoleSystem, Content: "s1"},
				{Role: RoleUser, Content: "u1"},
				{Role: RoleUser, Content: "u2"},
				{Role: RoleUser, Content: "u3"},
			}
			for _, index := range tc.marked {
				messages[index].CacheControl = marker
			}
			body := buildAnthropicBody(t, Request{
				Capabilities:    ProviderCapabilities(ProviderCapabilityCacheControl),
				StaticPrefixLen: 1,
				Messages:        messages,
				Tools:           []Tool{{Type: "function", Function: ToolFunction{Name: "bash"}}},
			})
			got := anthropicCacheBlocks(t, body)
			if !equalStrings(got, tc.want) {
				t.Fatalf("cache_control blocks = %v, want %v", got, tc.want)
			}
			if len(got) > anthropicMaxCacheBreakpoints {
				t.Fatalf("%d breakpoints exceeds the Messages API cap of %d", len(got), anthropicMaxCacheBreakpoints)
			}
		})
	}
}

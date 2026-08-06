package runstate

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/tokenizer"
)

func TestWriter_ThinkingDelta(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	// Subscribe before broadcasting so we catch live events.
	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	content := "Reasoning about the problem step by step..."
	w.ThinkingDelta(content)
	// Batching: flush so the queued thinking content is delivered.
	state.Flush()

	select {
	case evt := <-ch:
		if evt.Type != "thinking_delta" {
			t.Errorf("event type = %q, want %q", evt.Type, "thinking_delta")
		}
		if evt.Content != content {
			t.Errorf("event content = %q, want %q", evt.Content, content)
		}
	default:
		t.Fatal("no event received after ThinkingDelta")
	}
}

func TestWriter_ThinkingDelta_MultipleDeltas(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	deltas := []string{
		"First reasoning step...",
		"Second reasoning step...",
		"Third reasoning step...",
	}

	for _, d := range deltas {
		w.ThinkingDelta(d)
	}
	// Batching: flush so the queued thinking content is delivered.
	state.Flush()

	// Batching coalesces consecutive thinking deltas into a single event with
	// the concatenated content, delivered in order.
	select {
	case evt := <-ch:
		if evt.Type != "thinking_delta" {
			t.Errorf("event type = %q, want %q", evt.Type, "thinking_delta")
		}
		if want := "First reasoning step...Second reasoning step...Third reasoning step..."; evt.Content != want {
			t.Errorf("content = %q, want %q", evt.Content, want)
		}
	default:
		t.Fatal("no event received")
	}
	select {
	case evt := <-ch:
		t.Errorf("unexpected extra event: %+v", evt)
	default:
	}
}

func TestWriter_ThinkingDelta_SubscriberAfterBroadcastGetsHistory(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	content := "Late joiner reasoning..."
	w.ThinkingDelta(content)

	// Late subscriber should get the event replayed from history.
	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	select {
	case evt := <-ch:
		if evt.Type != "thinking_delta" {
			t.Errorf("event type = %q, want %q", evt.Type, "thinking_delta")
		}
		if evt.Content != content {
			t.Errorf("event content = %q, want %q", evt.Content, content)
		}
	default:
		t.Fatal("no event received after Subscribe (history replay)")
	}
}

func TestWriter_ThinkingDelta_AccumulatesInBuffer(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	// Initially empty
	if got := state.ReasoningBufferString(); got != "" {
		t.Errorf("initial reasoning buffer = %q, want empty", got)
	}

	deltas := []string{
		"Thinking step one...",
		"Thinking step two...",
		"Thinking step three...",
	}

	var expected string
	for _, d := range deltas {
		w.ThinkingDelta(d)
		expected += d
	}

	if got := state.ReasoningBufferString(); got != expected {
		t.Errorf("reasoning buffer after 3 deltas = %q, want %q", got, expected)
	}
}

func TestReasoningBuffer_AppendAndRetrieve(t *testing.T) {
	t.Parallel()

	state := New()

	// Test AppendReasoningBuffer directly
	state.AppendReasoningBuffer("part1")
	state.AppendReasoningBuffer("part2")
	state.AppendReasoningBuffer("part3")

	want := "part1part2part3"
	if got := state.ReasoningBufferString(); got != want {
		t.Errorf("ReasoningBufferString = %q, want %q", got, want)
	}
}

func TestReasoningBuffer_DoesNotAffectTextBuffer(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	w.Token("Hello ")
	w.ThinkingDelta("(thinking)")
	w.Token("world")

	// Text buffer should only have tokens
	if got := state.BufferString(); got != "Hello world" {
		t.Errorf("BufferString = %q, want %q", got, "Hello world")
	}

	// Reasoning buffer should only have thinking content
	if got := state.ReasoningBufferString(); got != "(thinking)" {
		t.Errorf("ReasoningBufferString = %q, want %q", got, "(thinking)")
	}
}

func TestState_SubscriberCount_StartsZero(t *testing.T) {
	t.Parallel()

	state := New()
	if got := state.SubscriberCount(); got != 0 {
		t.Errorf("SubscriberCount() = %d, want 0", got)
	}
}

func TestState_ReplayCount_StartsZero(t *testing.T) {
	t.Parallel()

	state := New()
	if got := state.ReplayCount(); got != 0 {
		t.Errorf("ReplayCount() = %d, want 0", got)
	}
}

func TestState_Subscribe_IncrementsSubscriberCount(t *testing.T) {
	t.Parallel()

	state := New()

	id1, _, ok := state.Subscribe()
	if !ok {
		t.Fatal("first Subscribe returned ok=false")
	}
	if id1 != 0 {
		t.Errorf("first subscriber id = %d, want 0", id1)
	}
	if got := state.SubscriberCount(); got != 1 {
		t.Errorf("SubscriberCount after first Subscribe = %d, want 1", got)
	}

	id2, _, ok := state.Subscribe()
	if !ok {
		t.Fatal("second Subscribe returned ok=false")
	}
	if id2 != 1 {
		t.Errorf("second subscriber id = %d, want 1", id2)
	}
	if got := state.SubscriberCount(); got != 2 {
		t.Errorf("SubscriberCount after second Subscribe = %d, want 2", got)
	}
}

func TestState_Subscribe_DoesNotIncrementSubscriberCountWhenStreamsClosed(t *testing.T) {
	t.Parallel()

	state := New()
	state.BroadcastDone("mid-1", nil)

	// BroadcastDone appends a done event to history, so Subscribe returns ok=true
	// (history available) but does NOT create a new subscriber.
	_, _, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe after BroadcastDone should return ok=true (history available)")
	}
	if got := state.SubscriberCount(); got != 0 {
		t.Errorf("SubscriberCount after Subscribe to closed stream = %d, want 0", got)
	}
}

func TestState_ReplayCount_IncrementsOnHistoryReplay(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	// Broadcast some events to build history
	w.Token("hello")

	// First subscribe — should replay history
	_, _, ok := state.Subscribe()
	if !ok {
		t.Fatal("first Subscribe returned ok=false")
	}
	if got := state.ReplayCount(); got != 1 {
		t.Errorf("ReplayCount after first Subscribe with history = %d, want 1", got)
	}

	// Second subscribe — should replay history again
	_, _, ok = state.Subscribe()
	if !ok {
		t.Fatal("second Subscribe returned ok=false")
	}
	if got := state.ReplayCount(); got != 2 {
		t.Errorf("ReplayCount after second Subscribe with history = %d, want 2", got)
	}
}

func TestState_ReplayCount_DoesNotIncrementWhenHistoryEmpty(t *testing.T) {
	t.Parallel()

	state := New()

	_, _, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}
	if got := state.ReplayCount(); got != 0 {
		t.Errorf("ReplayCount with empty history = %d, want 0", got)
	}
}

func TestState_Counters_ResetOnNewRun(t *testing.T) {
	t.Parallel()

	// Simulate: run completes, new run starts with fresh State
	state1 := New()
	state1.Subscribe()
	state1.Subscribe()
	w := NewWriter(state1)
	w.Token("test")
	state1.Subscribe()

	// Three Subscribe calls: two before history, one after (all create subscribers)
	if got := state1.SubscriberCount(); got != 3 {
		t.Errorf("state1 SubscriberCount = %d, want 3", got)
	}
	if got := state1.ReplayCount(); got != 1 {
		t.Errorf("state1 ReplayCount = %d, want 1", got)
	}

	// New state = fresh run
	state2 := New()
	if got := state2.SubscriberCount(); got != 0 {
		t.Errorf("state2 SubscriberCount = %d, want 0", got)
	}
	if got := state2.ReplayCount(); got != 0 {
		t.Errorf("state2 ReplayCount = %d, want 0", got)
	}
}

func TestState_Broadcast_SetsTimestampBeforeHistory(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	w.Token("hello")

	history := state.History()
	if len(history) == 0 {
		t.Fatal("expected at least 1 history entry after Broadcast")
	}
	for i, evt := range history {
		if evt.Timestamp.IsZero() {
			t.Errorf("history[%d] has zero Timestamp (set after append)", i)
		}
	}
}

func TestState_BroadcastDone_SetsTimestampBeforeHistory(t *testing.T) {
	t.Parallel()

	state := New()
	state.BroadcastDone("mid-1", nil)

	history := state.History()
	if len(history) == 0 {
		t.Fatal("expected at least 1 history entry after BroadcastDone")
	}
	for i, evt := range history {
		if evt.Timestamp.IsZero() {
			t.Errorf("history[%d] has zero Timestamp in BroadcastDone", i)
		}
	}
}

func TestState_BroadcastError_SetsTimestampBeforeHistory(t *testing.T) {
	t.Parallel()

	state := New()
	state.BroadcastError("test error")

	history := state.History()
	if len(history) == 0 {
		t.Fatal("expected at least 1 history entry after BroadcastError")
	}
	for i, evt := range history {
		if evt.Timestamp.IsZero() {
			t.Errorf("history[%d] has zero Timestamp in BroadcastError", i)
		}
	}
}

// ---------------------------------------------------------------------------
// Writer: constructor / accessor
// ---------------------------------------------------------------------------

func TestWriter_NewWriter(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)
	if w == nil {
		t.Fatal("NewWriter returned nil")
	}
	if w.State() != state {
		t.Error("NewWriter did not store the provided state")
	}
}

func TestWriter_State(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)
	if got := w.State(); got != state {
		t.Errorf("State() = %p, want %p", got, state)
	}
}

// ---------------------------------------------------------------------------
// Writer: Token
// ---------------------------------------------------------------------------

func TestWriter_Token_BroadcastsEvent(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.Token("Hello, world!")
	// Batching: flush so the queued token content is delivered.
	state.Flush()

	select {
	case evt := <-ch:
		if evt.Type != "token" {
			t.Errorf("event type = %q, want %q", evt.Type, "token")
		}
		if evt.Content != "Hello, world!" {
			t.Errorf("event content = %q, want %q", evt.Content, "Hello, world!")
		}
	default:
		t.Fatal("no event received after Token")
	}
}

func TestWriter_Token_AppendsBuffer(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	if got := state.BufferString(); got != "" {
		t.Fatalf("initial buffer = %q, want empty", got)
	}

	w.Token("Hello ")
	w.Token("world!")

	if got := state.BufferString(); got != "Hello world!" {
		t.Errorf("buffer after two tokens = %q, want %q", got, "Hello world!")
	}
}

func TestWriter_Token_HistoryReplayed(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	w.Token("early token")

	// Late subscriber gets history
	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	select {
	case evt := <-ch:
		if evt.Type != "token" {
			t.Errorf("event type = %q, want %q", evt.Type, "token")
		}
		if evt.Content != "early token" {
			t.Errorf("event content = %q, want %q", evt.Content, "early token")
		}
	default:
		t.Fatal("no history event received after Subscribe")
	}
}

// ---------------------------------------------------------------------------
// Writer: token batching
// ---------------------------------------------------------------------------

func TestWriter_Token_Batching_CoalescesIntoOneEvent(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.Token("Hello ")
	w.Token("world")
	w.Token("!")

	// Batching: flush so the queued content is delivered as one event.
	state.Flush()

	select {
	case evt := <-ch:
		if evt.Type != "token" {
			t.Errorf("event type = %q, want %q", evt.Type, "token")
		}
		if evt.Content != "Hello world!" {
			t.Errorf("event content = %q, want %q", evt.Content, "Hello world!")
		}
	default:
		t.Fatal("no event received after Token")
	}
	select {
	case evt := <-ch:
		t.Errorf("unexpected extra event: %+v", evt)
	default:
	}
}

func TestWriter_Token_Batching_FlushesOnInterval(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.Token("streamed content")

	// No explicit Flush: the flush interval timer must deliver the content.
	deadline := time.After(time.Second)
	select {
	case evt := <-ch:
		if evt.Type != "token" {
			t.Errorf("event type = %q, want %q", evt.Type, "token")
		}
		if evt.Content != "streamed content" {
			t.Errorf("event content = %q, want %q", evt.Content, "streamed content")
		}
	case <-deadline:
		t.Fatal("token not delivered by flush interval")
	}
}

func TestWriter_Token_Batching_FlushesBeforeNonTokenEvent(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.Token("partial text")
	w.ToolCall("bash", nil)

	// Batched token content must be delivered before the tool_call event.
	first := <-ch
	if first.Type != "token" {
		t.Errorf("first event type = %q, want %q (batched token flushed before tool_call)", first.Type, "token")
	}
	if first.Content != "partial text" {
		t.Errorf("first event content = %q, want %q", first.Content, "partial text")
	}
	second := <-ch
	if second.Type != "tool_call" {
		t.Errorf("second event type = %q, want %q", second.Type, "tool_call")
	}
}

func TestWriter_Token_Batching_TypeSwitchSeparatesThinkingAndContent(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.ThinkingDelta("reasoning...")
	w.Token("answer.")
	state.Flush()

	first := <-ch
	if first.Type != "thinking_delta" {
		t.Errorf("first event type = %q, want %q", first.Type, "thinking_delta")
	}
	if first.Content != "reasoning..." {
		t.Errorf("first event content = %q, want %q", first.Content, "reasoning...")
	}
	second := <-ch
	if second.Type != "token" {
		t.Errorf("second event type = %q, want %q", second.Type, "token")
	}
	if second.Content != "answer." {
		t.Errorf("second event content = %q, want %q", second.Content, "answer.")
	}
}

func TestWriter_Token_Batching_FlushesOnClose(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.Token("final text")
	w.Done("msg-1", nil)

	// The done event flushes pending batched content; subscribers must see
	// the final token before the done event.
	first := <-ch
	if first.Type != "token" {
		t.Errorf("first event type = %q, want %q", first.Type, "token")
	}
	if first.Content != "final text" {
		t.Errorf("first event content = %q, want %q", first.Content, "final text")
	}
	second := <-ch
	if second.Type != "done" {
		t.Errorf("second event type = %q, want %q", second.Type, "done")
	}
	if _, open := <-ch; open {
		t.Error("channel still open after done")
	}
}

func TestState_Batch_OrderingWithInterleavedEvents(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.Token("a")
	w.Token("b")
	w.ToolCall("bash", nil)
	w.Token("c")
	w.Token("d")
	w.ToolCall("read", nil)
	w.Token("e")
	w.Done("msg-1", nil)

	wantTypes := []string{"token", "tool_call", "token", "tool_call", "token", "done"}
	var got []string
	for evt := range ch {
		got = append(got, evt.Type)
	}
	if len(got) != len(wantTypes) {
		t.Fatalf("event types = %v, want %v", got, wantTypes)
	}
	for i := range wantTypes {
		if got[i] != wantTypes[i] {
			t.Errorf("event %d type = %q, want %q (batched tokens must flush before interleaving events)", i, got[i], wantTypes[i])
		}
	}
}

// TestState_Batch_OrderingUnderConcurrentTimer exercises the race between the
// flush-interval timer and interleaving broadcasts: token content written
// before a tool_call must never arrive at subscribers after that tool_call,
// even when the timer splits batches concurrently.
func TestState_Batch_OrderingUnderConcurrentTimer(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	const (
		iterations = 25
		perIter    = 8
	)
	go func() {
		defer w.Done("msg", nil)
		for i := 0; i < iterations; i++ {
			for j := 0; j < perIter; j++ {
				w.Token(fmt.Sprintf("i%02dj%d ", i, j))
				// Pace writes so the 50ms flush timer fires mid-batch.
				time.Sleep(2 * time.Millisecond)
			}
			w.ToolCall("bash", map[string]any{"i": i})
		}
	}()

	// parts tracks, per iteration, how many token parts have been received.
	parts := make([]int, iterations)
	deadline := time.After(30 * time.Second)
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				t.Fatal("stream closed before done event")
			}
			switch evt.Type {
			case "token":
				re := regexp.MustCompile(`i(\d+)j(\d+)`)
				for _, m := range re.FindAllStringSubmatch(evt.Content, -1) {
					var i, j int
					fmt.Sscanf(m[1], "%d", &i)
					fmt.Sscanf(m[2], "%d", &j)
					if i < 0 || i >= iterations {
						t.Fatalf("unexpected iteration index %d in %q", i, evt.Content)
					}
					parts[i]++
				}
			case "tool_call":
				var i int
				if data, ok := evt.Args.(map[string]any); ok {
					if fi, ok := data["i"].(float64); ok {
						i = int(fi)
					}
				}
				for k := 0; k <= i; k++ {
					if parts[k] < perIter {
						t.Fatalf("tool_call for iteration %d arrived before all %d tokens of iteration %d (got %d)", i, perIter, k, parts[k])
					}
				}
			case "done":
				for k := 0; k < iterations; k++ {
					if parts[k] != perIter {
						t.Fatalf("done arrived with iteration %d at %d/%d parts", k, parts[k], perIter)
					}
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for stream")
		}
	}
}

// ---------------------------------------------------------------------------
// State: bounded history
// ---------------------------------------------------------------------------

func TestState_History_BoundedByEventCount(t *testing.T) {
	t.Parallel()

	state := New()
	for i := 0; i < maxHistoryEvents+100; i++ {
		state.Broadcast(SSEEvent{Type: "tool_call", Tool: fmt.Sprintf("tool-%d", i)})
	}

	history := state.History()
	if len(history) > maxHistoryEvents {
		t.Errorf("history length = %d, want <= %d", len(history), maxHistoryEvents)
	}
	// The tail (newest events) must be retained, in order.
	if len(history) > 0 {
		last := history[len(history)-1]
		want := "tool-" + fmt.Sprint(maxHistoryEvents+99)
		if last.Tool != want {
			t.Errorf("last history event tool = %q, want %q", last.Tool, want)
		}
	}
}

func TestState_History_BoundedByBytes(t *testing.T) {
	t.Parallel()

	state := New()
	big := strings.Repeat("x", 8192)
	for i := 0; i < 300; i++ {
		state.Broadcast(SSEEvent{Type: "token", Content: big})
	}

	history := state.History()
	total := 0
	for _, evt := range history {
		if evt.Type == "token" {
			total += len(evt.Content)
		}
	}
	if total > maxHistoryBytes {
		t.Errorf("history token content bytes = %d, want <= %d", total, maxHistoryBytes)
	}
	if total == 300*len(big) {
		t.Errorf("history retained all %d token events; expected the byte budget to trim old content", len(history))
	}
}

func TestState_Subscribe_LateSubscriberGetsRecentTailInOrder(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	// Broadcast enough content to exceed the history byte budget so the full
	// run can no longer fit — a mid-run subscriber must get only the tail.
	const totalEvents = 300
	for i := 0; i < totalEvents; i++ {
		w.Token(fmt.Sprintf("token-%03d-", i) + strings.Repeat("y", 4096))
		state.Flush()
	}

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	// Read a handful of replayed events and assert they arrive in order with
	// no gaps (contiguous tail of the original sequence).
	var received []string
	deadline := time.After(2 * time.Second)
	for len(received) < 8 {
		select {
		case evt := <-ch:
			if evt.Type != "token" {
				t.Errorf("replayed event type = %q, want %q", evt.Type, "token")
			}
			received = append(received, evt.Content)
		case <-deadline:
			t.Fatalf("timed out waiting for replayed events, got %d", len(received))
		}
	}

	var prev int
	for i, content := range received {
		var n int
		if _, err := fmt.Sscanf(content, "token-%d-", &n); err != nil {
			t.Fatalf("unexpected content %q", content)
		}
		if i > 0 && n != prev+1 {
			t.Errorf("replayed sequence gap: event %d = %d, previous = %d", i, n, prev)
		}
		prev = n
	}

	// The subscriber must not receive the whole run — the byte-bounded history
	// drops the oldest content so the tail fits.
	history := state.History()
	if len(history) >= totalEvents {
		t.Errorf("history retained %d of %d events; expected bounding to trim the oldest", len(history), totalEvents)
	}
	if got, want := len(history[0].Content), len("token-000-")+4096; got != want {
		t.Errorf("replayed event content length = %d, want %d", got, want)
	}
}

// ---------------------------------------------------------------------------
// Writer: ToolCall
// ---------------------------------------------------------------------------

func TestWriter_ToolCall_BroadcastsEvent(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.ToolCall("bash", map[string]string{"cmd": "ls"})

	select {
	case evt := <-ch:
		if evt.Type != "tool_call" {
			t.Errorf("event type = %q, want %q", evt.Type, "tool_call")
		}
		if evt.Kind != RenderKindToolCard {
			t.Errorf("event kind = %q, want %q", evt.Kind, RenderKindToolCard)
		}
		if evt.Tool != "bash" {
			t.Errorf("event tool = %q, want %q", evt.Tool, "bash")
		}
		// Args is a map[string]string when passed as such (Go preserves the concrete type)
		switch args := evt.Args.(type) {
		case map[string]string:
			if args["cmd"] != "ls" {
				t.Errorf("event args[\"cmd\"] = %v, want %q", args["cmd"], "ls")
			}
		case map[string]interface{}:
			if args["cmd"] != "ls" {
				t.Errorf("event args[\"cmd\"] = %v, want %q", args["cmd"], "ls")
			}
		default:
			t.Fatalf("event args type = %T, want map[string]string or map[string]interface{}", evt.Args)
		}
	default:
		t.Fatal("no event received after ToolCall")
	}
}

func TestWriter_ToolCall_NilArgs(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.ToolCall("read", nil)

	select {
	case evt := <-ch:
		if evt.Type != "tool_call" {
			t.Errorf("event type = %q, want %q", evt.Type, "tool_call")
		}
		if evt.Kind != RenderKindToolCard {
			t.Errorf("event kind = %q, want %q", evt.Kind, RenderKindToolCard)
		}
		if evt.Tool != "read" {
			t.Errorf("event tool = %q, want %q", evt.Tool, "read")
		}
		if evt.Args != nil {
			t.Errorf("event args = %v, want nil", evt.Args)
		}
	default:
		t.Fatal("no event received after ToolCall")
	}
}

// ---------------------------------------------------------------------------
// Writer: ToolResult
// ---------------------------------------------------------------------------

func TestWriter_ToolResult_StringOutput(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.ToolResult("bash", "file1.txt\nfile2.txt")

	select {
	case evt := <-ch:
		if evt.Type != "tool_result" {
			t.Errorf("event type = %q, want %q", evt.Type, "tool_result")
		}
		if evt.Kind != RenderKindToolCard {
			t.Errorf("event kind = %q, want %q", evt.Kind, RenderKindToolCard)
		}
		if evt.Tool != "bash" {
			t.Errorf("event tool = %q, want %q", evt.Tool, "bash")
		}
		if evt.Output != "file1.txt\nfile2.txt" {
			t.Errorf("event output = %q, want %q", evt.Output, "file1.txt\nfile2.txt")
		}
	default:
		t.Fatal("no event received after ToolResult")
	}
}

func TestWriter_ToolResult_JSONOutput(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.ToolResult("read", map[string]int{"lines": 42, "bytes": 1024})

	select {
	case evt := <-ch:
		if evt.Type != "tool_result" {
			t.Errorf("event type = %q, want %q", evt.Type, "tool_result")
		}
		if evt.Kind != RenderKindToolCard {
			t.Errorf("event kind = %q, want %q", evt.Kind, RenderKindToolCard)
		}
		if evt.Tool != "read" {
			t.Errorf("event tool = %q, want %q", evt.Tool, "read")
		}
		// Non-string output should be JSON-marshaled into Output
		outputStr, ok := evt.Output.(string)
		if !ok {
			t.Fatalf("event Output type = %T, want string (JSON)", evt.Output)
		}
		if outputStr != `{"bytes":1024,"lines":42}` {
			t.Errorf("event output JSON = %q, want %q", outputStr, `{"bytes":1024,"lines":42}`)
		}
	default:
		t.Fatal("no event received after ToolResult")
	}
}

func TestWriter_ToolResult_EmptyOutput(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.ToolResult("bash", "")

	select {
	case evt := <-ch:
		if evt.Type != "tool_result" {
			t.Errorf("event type = %q, want %q", evt.Type, "tool_result")
		}
		if evt.Output != "" {
			t.Errorf("event output = %q, want empty", evt.Output)
		}
	default:
		t.Fatal("no event received after ToolResult")
	}
}

// ---------------------------------------------------------------------------
// Writer: Done
// ---------------------------------------------------------------------------

func TestWriter_Done_BroadcastsEvent(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	usage := &TokenUsage{TotalTokens: 100, PromptTokens: 60, CompletionTokens: 40}
	w.Done("msg-1", usage)

	select {
	case evt := <-ch:
		if evt.Type != "done" {
			t.Errorf("event type = %q, want %q", evt.Type, "done")
		}
		if evt.Kind != RenderKindMarkdown {
			t.Errorf("event kind = %q, want %q", evt.Kind, RenderKindMarkdown)
		}
		if evt.MessageID != "msg-1" {
			t.Errorf("event message_id = %q, want %q", evt.MessageID, "msg-1")
		}
		if evt.Usage != usage {
			t.Errorf("event usage = %v, want %v", evt.Usage, usage)
		}
	default:
		t.Fatal("no event received after Done")
	}
}

func TestWriter_Done_ClosesStream(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.Done("msg-1", nil)

	// Channel should eventually be closed after receiving done event.
	// Drain the done event first.
	<-ch

	// After draining, the channel should be closed.
	if _, open := <-ch; open {
		t.Error("subscriber channel still open after Done")
	}
}

func TestWriter_Done_SetsClosed(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	if state.Closed() {
		t.Error("state closed before Done")
	}

	w.Done("msg-1", nil)

	if !state.Closed() {
		t.Error("state not closed after Done")
	}
}

func TestWriter_Done_NilUsage(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.Done("msg-2", nil)

	select {
	case evt := <-ch:
		if evt.Type != "done" {
			t.Errorf("event type = %q, want %q", evt.Type, "done")
		}
		if evt.MessageID != "msg-2" {
			t.Errorf("event message_id = %q, want %q", evt.MessageID, "msg-2")
		}
		if evt.Usage != nil {
			t.Errorf("event usage = %v, want nil", evt.Usage)
		}
	default:
		t.Fatal("no event received after Done with nil usage")
	}
}

// ---------------------------------------------------------------------------
// Writer: Component
// ---------------------------------------------------------------------------

func TestWriter_Component_BroadcastsEvent(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	data := map[string]string{"type": "quick_replies", "options": "[\"Yes\",\"No\"]"}
	w.Component(data)

	select {
	case evt := <-ch:
		if evt.Type != "component" {
			t.Errorf("event type = %q, want %q", evt.Type, "component")
		}
		if evt.Kind != RenderKindComponent {
			t.Errorf("event kind = %q, want %q", evt.Kind, RenderKindComponent)
		}
		if evt.Data == nil {
			t.Fatal("event Data is nil")
		}
		gotData, ok := evt.Data.(map[string]string)
		if !ok {
			t.Fatalf("event Data type = %T, want map[string]string", evt.Data)
		}
		if gotData["type"] != "quick_replies" {
			t.Errorf("event Data[\"type\"] = %q, want %q", gotData["type"], "quick_replies")
		}
	default:
		t.Fatal("no event received after Component")
	}
}

func TestWriter_Component_NilData(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.Component(nil)

	select {
	case evt := <-ch:
		if evt.Type != "component" {
			t.Errorf("event type = %q, want %q", evt.Type, "component")
		}
		if evt.Kind != RenderKindComponent {
			t.Errorf("event kind = %q, want %q", evt.Kind, RenderKindComponent)
		}
		if evt.Data != nil {
			t.Errorf("event Data = %v, want nil", evt.Data)
		}
	default:
		t.Fatal("no event received after Component with nil data")
	}
}

// ---------------------------------------------------------------------------
// Writer: ContextUpdate
// ---------------------------------------------------------------------------

func TestWriter_ContextUpdate_BroadcastsEvent(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	update := &ContextUpdate{
		TotalTokens:      500,
		ContextWindow:    128000,
		PromptTokens:     300,
		CompletionTokens: 200,
		SystemTokens:     100,
		HistoryTokens:    200,
		SkillTokens:      50,
	}
	w.ContextUpdate(update)

	select {
	case evt := <-ch:
		if evt.Type != "context_update" {
			t.Errorf("event type = %q, want %q", evt.Type, "context_update")
		}
		if evt.Data == nil {
			t.Fatal("event Data is nil")
		}
		gotUpdate, ok := evt.Data.(*ContextUpdate)
		if !ok {
			t.Fatalf("event Data type = %T, want *ContextUpdate", evt.Data)
		}
		if gotUpdate.TotalTokens != 500 {
			t.Errorf("TotalTokens = %d, want 500", gotUpdate.TotalTokens)
		}
		if gotUpdate.ContextWindow != 128000 {
			t.Errorf("ContextWindow = %d, want 128000", gotUpdate.ContextWindow)
		}
	default:
		t.Fatal("no event received after ContextUpdate")
	}
}

// ---------------------------------------------------------------------------
// Writer: SkillActivated
// ---------------------------------------------------------------------------

func TestWriter_SkillActivated_BroadcastsEvent(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.SkillActivated("bash")

	select {
	case evt := <-ch:
		if evt.Type != "skill_activated" {
			t.Errorf("event type = %q, want %q", evt.Type, "skill_activated")
		}
		if evt.Tool != "bash" {
			t.Errorf("event tool = %q, want %q", evt.Tool, "bash")
		}
	default:
		t.Fatal("no event received after SkillActivated")
	}
}

func TestWriter_SkillActivated_EmptyName(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.SkillActivated("")

	select {
	case evt := <-ch:
		if evt.Type != "skill_activated" {
			t.Errorf("event type = %q, want %q", evt.Type, "skill_activated")
		}
		if evt.Tool != "" {
			t.Errorf("event tool = %q, want empty", evt.Tool)
		}
	default:
		t.Fatal("no event received after SkillActivated with empty name")
	}
}

// ---------------------------------------------------------------------------
// Writer: Error
// ---------------------------------------------------------------------------

func TestWriter_Error_BroadcastsEvent(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.Error("something went wrong")

	select {
	case evt := <-ch:
		if evt.Type != "error" {
			t.Errorf("event type = %q, want %q", evt.Type, "error")
		}
		if evt.Kind != RenderKindError {
			t.Errorf("event kind = %q, want %q", evt.Kind, RenderKindError)
		}
		if evt.Message != "something went wrong" {
			t.Errorf("event message = %q, want %q", evt.Message, "something went wrong")
		}
	default:
		t.Fatal("no event received after Error")
	}
}

func TestWriter_Error_ClosesStream(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.Error("test error")

	// Drain the error event
	<-ch

	// Channel should be closed
	if _, open := <-ch; open {
		t.Error("subscriber channel still open after Error")
	}
}

func TestWriter_Error_SetsClosed(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	if state.Closed() {
		t.Error("state closed before Error")
	}

	w.Error("test error")

	if !state.Closed() {
		t.Error("state not closed after Error")
	}
}

// ---------------------------------------------------------------------------
// State: Closed
// ---------------------------------------------------------------------------

func TestState_Closed_InitiallyFalse(t *testing.T) {
	t.Parallel()

	state := New()
	if state.Closed() {
		t.Error("new State should not be closed")
	}
}

func TestState_Closed_TrueAfterBroadcastDone(t *testing.T) {
	t.Parallel()

	state := New()
	state.BroadcastDone("mid-1", nil)
	if !state.Closed() {
		t.Error("State should be closed after BroadcastDone")
	}
}

func TestState_Closed_TrueAfterBroadcastError(t *testing.T) {
	t.Parallel()

	state := New()
	state.BroadcastError("error")
	if !state.Closed() {
		t.Error("State should be closed after BroadcastError")
	}
}

func TestState_Closed_TrueAfterBroadcastClosed(t *testing.T) {
	t.Parallel()

	state := New()
	state.BroadcastClosed("shutting down")
	if !state.Closed() {
		t.Error("State should be closed after BroadcastClosed")
	}
}

func TestState_Closed_StaysClosedAfterMultipleCalls(t *testing.T) {
	t.Parallel()

	state := New()
	state.BroadcastDone("mid-1", nil)
	// Calling again should not panic and state stays closed
	state.BroadcastDone("mid-2", nil)
	if !state.Closed() {
		t.Error("State should remain closed after second BroadcastDone")
	}
}

// ---------------------------------------------------------------------------
// State: Unsubscribe
// ---------------------------------------------------------------------------

func TestState_Unsubscribe_RemovesSubscriber(t *testing.T) {
	t.Parallel()

	state := New()
	id, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	state.Unsubscribe(id)

	// After unsubscribe, the subscriber channel should be closed
	if _, open := <-ch; open {
		t.Error("subscriber channel still open after Unsubscribe")
	}
}

func TestState_Unsubscribe_UnknownID(t *testing.T) {
	t.Parallel()

	// Should not panic
	state := New()
	state.Unsubscribe(999)
	state.Unsubscribe(0)
	// No assertions needed beyond not panicking
}

func TestState_Unsubscribe_DoesNotAffectOtherSubscribers(t *testing.T) {
	t.Parallel()

	state := New()
	id1, ch1, ok := state.Subscribe()
	if !ok {
		t.Fatal("first Subscribe returned ok=false")
	}
	_, ch2, ok := state.Subscribe()
	if !ok {
		t.Fatal("second Subscribe returned ok=false")
	}

	state.Unsubscribe(id1)

	// ch1 should be closed
	if _, open := <-ch1; open {
		t.Error("unsubscribed channel still open")
	}

	// ch2 should still be open
	w := NewWriter(state)
	w.Token("hello")
	// Batching: flush so the queued token content is delivered.
	state.Flush()

	select {
	case evt := <-ch2:
		if evt.Type != "token" {
			t.Errorf("event type = %q, want %q", evt.Type, "token")
		}
	default:
		t.Fatal("other subscriber did not receive event after Unsubscribe")
	}
}

// ---------------------------------------------------------------------------
// State: BroadcastClosed
// ---------------------------------------------------------------------------

func TestState_BroadcastClosed_BroadcastsEvent(t *testing.T) {
	t.Parallel()

	state := New()
	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	state.BroadcastClosed("server shutting down")

	select {
	case evt := <-ch:
		if evt.Type != "closed" {
			t.Errorf("event type = %q, want %q", evt.Type, "closed")
		}
		if evt.Message != "server shutting down" {
			t.Errorf("event message = %q, want %q", evt.Message, "server shutting down")
		}
	default:
		t.Fatal("no event received after BroadcastClosed")
	}
}

func TestState_BroadcastClosed_ClosesStream(t *testing.T) {
	t.Parallel()

	state := New()
	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	state.BroadcastClosed("done")

	// Drain the closed event
	<-ch

	if _, open := <-ch; open {
		t.Error("subscriber channel still open after BroadcastClosed")
	}
}

func TestState_BroadcastClosed_EmptyMessage(t *testing.T) {
	t.Parallel()

	state := New()
	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	state.BroadcastClosed("")

	select {
	case evt := <-ch:
		if evt.Type != "closed" {
			t.Errorf("event type = %q, want %q", evt.Type, "closed")
		}
		if evt.Message != "" {
			t.Errorf("event message = %q, want empty", evt.Message)
		}
	default:
		t.Fatal("no event received after BroadcastClosed with empty message")
	}
}

func TestState_BroadcastClosed_NoOpWhenAlreadyClosed(t *testing.T) {
	t.Parallel()

	state := New()
	state.BroadcastDone("mid-1", nil)
	// Should not panic
	state.BroadcastClosed("already closed")
}

// ---------------------------------------------------------------------------
// State: closeStreams (via subscriber close behavior after close)
// ---------------------------------------------------------------------------

func TestState_SubscribersRemovedAfterCloseStreams(t *testing.T) {
	t.Parallel()

	state := New()
	id, _, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	state.BroadcastDone("mid-1", nil)

	// Unsubscribe of already-removed subscriber should not panic
	state.Unsubscribe(id)
}

func TestState_SubscribersCannotSubscribeAfterClose(t *testing.T) {
	t.Parallel()

	state := New()
	state.BroadcastDone("mid-1", nil)

	// Subscribe after close returns false for active (history might be available)
	_, ch, ok := state.Subscribe()
	if ok {
		// ok=true means history was available (there is a done event in history)
		// Channel should be closed immediately
		<-ch // drain done event
		if _, open := <-ch; open {
			t.Error("channel from Subscribe after close should be closed after draining history")
		}
	}
}

// ---------------------------------------------------------------------------
// EstimateUsage
// ---------------------------------------------------------------------------

func TestEstimateUsage_EmptyText(t *testing.T) {
	t.Parallel()

	result := EstimateUsage("", nil, "")
	if result == nil {
		t.Fatal("EstimateUsage returned nil")
	}
	// For empty text: len=0 -> 0/4=0, clamped to 1 total, 1/3=0 clamped to 1 completion, prompt=0
	if result.TotalTokens != 1 {
		t.Errorf("TotalTokens = %d, want 1", result.TotalTokens)
	}
	if result.PromptTokens != 0 {
		t.Errorf("PromptTokens = %d, want 0", result.PromptTokens)
	}
	if result.CompletionTokens != 1 {
		t.Errorf("CompletionTokens = %d, want 1", result.CompletionTokens)
	}
}

func TestEstimateUsage_ShortText(t *testing.T) {
	t.Parallel()

	result := EstimateUsage("Hello", nil, "")
	if result == nil {
		t.Fatal("EstimateUsage returned nil")
	}
	if result.TotalTokens != 1 {
		t.Errorf("TotalTokens = %d, want 1 (len=5 -> 5/4=1)", result.TotalTokens)
	}
}

func TestEstimateUsage_LongText(t *testing.T) {
	t.Parallel()

	// 400 chars -> ~100 tokens
	text := make([]byte, 400)
	for i := range text {
		text[i] = 'a'
	}
	result := EstimateUsage(string(text), nil, "")
	if result == nil {
		t.Fatal("EstimateUsage returned nil")
	}
	if result.TotalTokens != 100 {
		t.Errorf("TotalTokens = %d, want 100 (400 chars / 4)", result.TotalTokens)
	}
	// Prompt: ~2/3 of total, completion: ~1/3
	if result.TotalTokens != result.PromptTokens+result.CompletionTokens {
		t.Errorf("TotalTokens(%d) != PromptTokens(%d) + CompletionTokens(%d)",
			result.TotalTokens, result.PromptTokens, result.CompletionTokens)
	}
}

func TestEstimateUsage_TokenBreakdown(t *testing.T) {
	t.Parallel()

	// 4000 chars -> ~1000 tokens
	text := make([]byte, 4000)
	for i := range text {
		text[i] = 'a'
	}
	result := EstimateUsage(string(text), nil, "")
	if result.TotalTokens != 1000 {
		t.Errorf("TotalTokens = %d, want 1000", result.TotalTokens)
	}
	// completion = total / 3 = 333 (rounded), prompt = total - completion = 667
	expectedCompletion := 1000 / 3 // 333
	if result.CompletionTokens != expectedCompletion {
		t.Errorf("CompletionTokens = %d, want %d", result.CompletionTokens, expectedCompletion)
	}
	if result.PromptTokens != 1000-expectedCompletion {
		t.Errorf("PromptTokens = %d, want %d", result.PromptTokens, 1000-expectedCompletion)
	}
}

func TestEstimateUsage_CalibratedStore(t *testing.T) {
	t.Parallel()

	store := tokenizer.NewCalibrationStore()
	model := "calibrated-model"
	// Feed calibration data that should produce CPT=5.0
	// With EMA α=0.3: starts at 4.0, one observation at 5.0 → 0.3*5 + 0.7*4 = 4.3
	store.Update(model, 5.0)
	expectedCPT := store.Lookup(model) // should be 4.3

	// 430 chars -> 430/4.3 = 100 tokens
	text := make([]byte, 430)
	for i := range text {
		text[i] = 'a'
	}

	result := EstimateUsage(string(text), store, model)
	if result == nil {
		t.Fatal("EstimateUsage returned nil")
	}
	if result.TotalTokens != 100 {
		t.Errorf("TotalTokens = %d, want 100 (430 chars / CPT %.2f)", result.TotalTokens, expectedCPT)
	}
	if result.TotalTokens != result.PromptTokens+result.CompletionTokens {
		t.Errorf("TotalTokens(%d) != PromptTokens(%d) + CompletionTokens(%d)",
			result.TotalTokens, result.PromptTokens, result.CompletionTokens)
	}
}

func TestEstimateUsage_CalibratedStoreDifferentModel(t *testing.T) {
	t.Parallel()

	store := tokenizer.NewCalibrationStore()
	// Calibrate model A with different CPT
	store.Update("model-a", 5.0)
	// model-b should still use default 4.0

	text := make([]byte, 400)
	for i := range text {
		text[i] = 'a'
	}

	// model-a uses calibrated CPT (~4.3), so 400/4.3 = 93 tokens
	resultA := EstimateUsage(string(text), store, "model-a")
	// model-b uses default CPT 4.0, so 400/4.0 = 100 tokens
	resultB := EstimateUsage(string(text), store, "model-b")

	if resultA.TotalTokens == resultB.TotalTokens {
		t.Errorf("Calibrated model-a (%d) should differ from uncalibrated model-b (%d)",
			resultA.TotalTokens, resultB.TotalTokens)
	}
}

// ---------------------------------------------------------------------------
// Writer: Event ordering — multiple event types in sequence
// ---------------------------------------------------------------------------

func TestWriter_MultipleEvents_Sequence(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	w.SkillActivated("bash")
	w.Token("Let me check")
	w.ToolCall("bash", map[string]string{"cmd": "ls"})
	w.ToolResult("bash", "file1.txt")
	w.Token("\n\nDone!")
	w.Done("msg-1", nil)

	// Read events in order
	checks := []struct {
		wantType string
		wantTool string
	}{
		{"skill_activated", "bash"},
		{"token", ""},
		{"tool_call", "bash"},
		{"tool_result", "bash"},
		{"token", ""},
		{"done", ""},
	}

	for i, check := range checks {
		select {
		case evt := <-ch:
			if evt.Type != check.wantType {
				t.Errorf("event %d: type = %q, want %q", i, evt.Type, check.wantType)
			}
			if check.wantTool != "" && evt.Tool != check.wantTool {
				t.Errorf("event %d: tool = %q, want %q", i, evt.Tool, check.wantTool)
			}
		default:
			t.Fatalf("event %d: no event received", i)
		}
	}

	// After Done, channel should be closed
	if _, open := <-ch; open {
		t.Error("channel still open after Done at end of sequence")
	}
}

// ---------------------------------------------------------------------------
// State: Broadcast is no-op after close
// ---------------------------------------------------------------------------

func TestState_Broadcast_NoOpAfterClose(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	// Close the state
	w.Done("msg-1", nil)

	// Drain the done event
	<-ch

	// Broadcast after close should not reach subscribers
	w.Token("should not appear")

	// Channel should be closed
	if _, open := <-ch; open {
		// If there's a token event, drain and fail
		<-ch
		t.Error("received event after state was closed")
	}
}

func TestState_Broadcast_AfterDone_HistoryStillAppended(t *testing.T) {
	t.Parallel()

	state := New()
	state.BroadcastDone("msg-1", nil)

	// Try broadcasting after close (should be no-op)
	state.Broadcast(SSEEvent{Type: "token", Content: "late"})

	// History should still contain only the done event (no late events)
	history := state.History()
	for _, evt := range history {
		if evt.Type == "token" {
			t.Error("Broadcast after close should not append to history")
		}
	}
}

// ---------------------------------------------------------------------------
// State: History not modified after close
// ---------------------------------------------------------------------------

func TestState_History_FrozenAfterClose(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	w.Token("before")
	w.ToolCall("bash", nil)
	state.BroadcastDone("msg-1", nil)

	// Broadcast after close should be no-op
	state.Broadcast(SSEEvent{Type: "token", Content: "after"})

	history := state.History()
	if len(history) != 3 {
		t.Fatalf("history length = %d, want 3 (token + tool_call + done)", len(history))
	}
	if history[2].Type != "done" {
		t.Errorf("last history entry type = %q, want %q", history[2].Type, "done")
	}
}

// ---------------------------------------------------------------------------
// Writer: ToolResult with complex JSON object
// ---------------------------------------------------------------------------

func TestWriter_ToolResult_StructOutput(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	type fileInfo struct {
		Name string `json:"name"`
		Size int    `json:"size"`
	}
	output := fileInfo{Name: "test.go", Size: 1024}
	w.ToolResult("read", output)

	select {
	case evt := <-ch:
		if evt.Type != "tool_result" {
			t.Errorf("event type = %q, want %q", evt.Type, "tool_result")
		}
		outputStr, ok := evt.Output.(string)
		if !ok {
			t.Fatalf("event Output type = %T, want string", evt.Output)
		}
		if outputStr != `{"name":"test.go","size":1024}` {
			t.Errorf("event output JSON = %q, want %q", outputStr, `{"name":"test.go","size":1024}`)
		}
	default:
		t.Fatal("no event received after ToolResult with struct")
	}
}

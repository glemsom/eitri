package session_test

import (
	"sync"
	"testing"

	"github.com/glemsom/eitri/internal/session"
)

func TestCreateAndGet(t *testing.T) {
	mgr := session.NewManager(10)

	sess, err := mgr.Create("browser-1")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if sess.ID == "" {
		t.Error("session ID should not be empty")
	}
	if sess.BrowserID != "browser-1" {
		t.Errorf("BrowserID = %q, want %q", sess.BrowserID, "browser-1")
	}
	if sess.Title != "Session 1" {
		t.Errorf("Title = %q, want %q", sess.Title, "Session 1")
	}
	if sess.Status != session.StatusIdle {
		t.Errorf("Status = %q, want %q", sess.Status, session.StatusIdle)
	}

	// Get by ID
	got := mgr.Get(sess.ID)
	if got == nil {
		t.Fatal("Get returned nil for existing session")
	}
	if got.ID != sess.ID {
		t.Errorf("Get returned session with ID %q, want %q", got.ID, sess.ID)
	}
}

func TestGetValidated(t *testing.T) {
	mgr := session.NewManager(10)

	sess, err := mgr.Create("browser-1")
	if err != nil {
		t.Fatal(err)
	}

	// Correct ownership
	got, ok := mgr.GetValidated(sess.ID, "browser-1")
	if !ok {
		t.Error("GetValidated should return true for matching browser")
	}
	if got == nil || got.ID != sess.ID {
		t.Error("GetValidated returned wrong session")
	}

	// Wrong browser
	_, ok = mgr.GetValidated(sess.ID, "browser-2")
	if ok {
		t.Error("GetValidated should return false for mismatched browser")
	}

	// Non-existent
	_, ok = mgr.GetValidated("nonexistent", "browser-1")
	if ok {
		t.Error("GetValidated should return false for missing session")
	}
}

func TestDelete(t *testing.T) {
	mgr := session.NewManager(10)

	sess, _ := mgr.Create("browser-1")
	deleted := mgr.Delete(sess.ID)
	if deleted == nil {
		t.Fatal("Delete returned nil for existing session")
	}
	if deleted.ID != sess.ID {
		t.Errorf("Delete returned wrong session")
	}

	// Should no longer exist
	if got := mgr.Get(sess.ID); got != nil {
		t.Error("session should be deleted")
	}

	// Delete non-existent
	if d := mgr.Delete("nonexistent"); d != nil {
		t.Error("Delete should return nil for non-existent")
	}
}

func TestListByBrowser(t *testing.T) {
	mgr := session.NewManager(10)

	s1, _ := mgr.Create("browser-1")
	s2, _ := mgr.Create("browser-1")
	mgr.Create("browser-2")

	sessions := mgr.ListByBrowser("browser-1")
	if len(sessions) != 2 {
		t.Errorf("ListByBrowser returned %d sessions, want 2", len(sessions))
	}

	if sessions[0].ID != s1.ID {
		t.Errorf("First session ID = %q, want %q", sessions[0].ID, s1.ID)
	}
	if sessions[1].ID != s2.ID {
		t.Errorf("Second session ID = %q, want %q", sessions[1].ID, s2.ID)
	}
}

func TestLastActive(t *testing.T) {
	mgr := session.NewManager(10)

	// No sessions
	if last := mgr.LastActive("browser-1"); last != nil {
		t.Error("LastActive should be nil for empty browser")
	}

	s1, _ := mgr.Create("browser-1")
	last := mgr.LastActive("browser-1")
	if last == nil || last.ID != s1.ID {
		t.Error("LastActive should return the only session")
	}

	s2, _ := mgr.Create("browser-1")
	last = mgr.LastActive("browser-1")
	if last == nil || last.ID != s2.ID {
		t.Error("LastActive should return most recently created session")
	}
}

func TestSessionCap(t *testing.T) {
	mgr := session.NewManager(2)

	s1, err := mgr.Create("browser-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = s1

	s2, err := mgr.Create("browser-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = s2

	// Third should fail
	_, err = mgr.Create("browser-1")
	if err == nil {
		t.Error("Expected error for exceeding session cap")
	}

	if mgr.Count() != 2 {
		t.Errorf("Count = %d, want 2", mgr.Count())
	}
}

func TestSessionCapAcrossBrowsers(t *testing.T) {
	mgr := session.NewManager(2)

	mgr.Create("browser-1")
	mgr.Create("browser-2")

	// Global cap hit
	_, err := mgr.Create("browser-3")
	if err == nil {
		t.Error("Expected error for exceeding global session cap")
	}
}

func TestBrowserCount(t *testing.T) {
	mgr := session.NewManager(10)

	mgr.Create("browser-1")
	mgr.Create("browser-1")
	mgr.Create("browser-2")

	if c := mgr.BrowserCount("browser-1"); c != 2 {
		t.Errorf("BrowserCount browser-1 = %d, want 2", c)
	}
	if c := mgr.BrowserCount("browser-2"); c != 1 {
		t.Errorf("BrowserCount browser-2 = %d, want 1", c)
	}
	if c := mgr.BrowserCount("browser-3"); c != 0 {
		t.Errorf("BrowserCount browser-3 = %d, want 0", c)
	}
}

func TestDeleteFreesCapSlot(t *testing.T) {
	mgr := session.NewManager(2)

	s1, _ := mgr.Create("browser-1")
	mgr.Create("browser-1")

	// Cap should be reached
	if _, err := mgr.Create("browser-1"); err == nil {
		t.Fatal("expected cap error")
	}

	// Delete first session
	mgr.Delete(s1.ID)

	// Now should be able to create again
	s3, err := mgr.Create("browser-1")
	if err != nil {
		t.Fatalf("Create after delete should succeed: %v", err)
	}
	if s3.Title != "Session 3" {
		t.Errorf("Title = %q, want %q", s3.Title, "Session 3")
	}
}

func TestUpdateTitle(t *testing.T) {
	mgr := session.NewManager(10)

	sess, _ := mgr.Create("browser-1")
	mgr.UpdateTitle(sess.ID, "New Title")

	got := mgr.Get(sess.ID)
	if got.Title != "New Title" {
		t.Errorf("Title = %q, want %q", got.Title, "New Title")
	}
}

func TestUpdateStatus(t *testing.T) {
	mgr := session.NewManager(10)

	sess, _ := mgr.Create("browser-1")
	mgr.UpdateStatus(sess.ID, session.StatusRunning)

	got := mgr.Get(sess.ID)
	if got.Status != session.StatusRunning {
		t.Errorf("Status = %q, want %q", got.Status, session.StatusRunning)
	}
}

func TestAppendMessage(t *testing.T) {
	mgr := session.NewManager(10)

	sess, _ := mgr.Create("browser-1")
	msg := session.Message{Role: "user", Content: "hello"}
	mgr.AppendMessage(sess.ID, msg)

	got := mgr.Get(sess.ID)
	if len(got.Messages) != 1 {
		t.Errorf("Messages count = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].Content != "hello" {
		t.Errorf("Message content = %q, want %q", got.Messages[0].Content, "hello")
	}
	if got.Title != "hello" {
		t.Errorf("Title = %q, want %q", got.Title, "hello")
	}
}

func TestAppendMessage_FirstUserMessageSetsTruncatedTitle(t *testing.T) {
	mgr := session.NewManager(10)

	sess, _ := mgr.Create("browser-1")
	mgr.AppendMessage(sess.ID, session.Message{Role: "user", Content: "   Fix   flaky session tab title behavior across browser tabs and runs   "})

	got := mgr.Get(sess.ID)
	if got.Title != "Fix flaky session tab title be…" {
		t.Errorf("Title = %q, want %q", got.Title, "Fix flaky session tab title be…")
	}
}

func TestAppendMessage_EveryUserMessageUpdatesTitle(t *testing.T) {
	mgr := session.NewManager(10)

	sess, _ := mgr.Create("browser-1")
	mgr.AppendMessage(sess.ID, session.Message{Role: "user", Content: "first question"})
	mgr.AppendMessage(sess.ID, session.Message{Role: "assistant", Content: "answer"})
	mgr.AppendMessage(sess.ID, session.Message{Role: "user", Content: "second question should rename"})

	got := mgr.Get(sess.ID)
	if got.Title != "second question should rename" {
		t.Errorf("Title = %q, want %q", got.Title, "second question should rename")
	}
}

func TestAppendMessage_BlankFirstUserMessageKeepsPlaceholderTitle(t *testing.T) {
	mgr := session.NewManager(10)

	sess, _ := mgr.Create("browser-1")
	mgr.AppendMessage(sess.ID, session.Message{Role: "user", Content: "   \n\t  "})

	got := mgr.Get(sess.ID)
	if got.Title != "Session 1" {
		t.Errorf("Title = %q, want %q", got.Title, "Session 1")
	}
}

func TestAppendMessage_BlankFirstUserMessageDoesNotBlockLaterRename(t *testing.T) {
	mgr := session.NewManager(10)

	sess, _ := mgr.Create("browser-1")
	mgr.AppendMessage(sess.ID, session.Message{Role: "user", Content: "   \n\t  "})
	mgr.AppendMessage(sess.ID, session.Message{Role: "user", Content: "real first question"})

	got := mgr.Get(sess.ID)
	if got.Title != "real first question" {
		t.Errorf("Title = %q, want %q", got.Title, "real first question")
	}
}

func TestDefaultMaxSessions(t *testing.T) {
	mgr := session.NewManager(0) // should use default
	if mgr.Count() != 0 {
		t.Error("new manager should have 0 sessions")
	}

	// Create up to default cap
	for i := 0; i < 10; i++ {
		_, err := mgr.Create("browser-1")
		if err != nil {
			t.Fatalf("Create %d failed: %v", i, err)
		}
	}

	// 11th should fail
	if _, err := mgr.Create("browser-1"); err == nil {
		t.Error("expected cap error")
	}
}

func TestActivateSkill(t *testing.T) {
	mgr := session.NewManager(10)

	sess, _ := mgr.Create("browser-1")

	// Activate first skill
	ok := mgr.ActivateSkill(sess.ID, "code-review")
	if !ok {
		t.Error("ActivateSkill returned false for new activation")
	}

	got := mgr.Get(sess.ID)
	if len(got.ActiveSkills) != 1 || got.ActiveSkills[0] != "code-review" {
		t.Errorf("ActiveSkills = %v, want [code-review]", got.ActiveSkills)
	}

	// Activate second skill
	ok = mgr.ActivateSkill(sess.ID, "debug")
	if !ok {
		t.Error("ActivateSkill returned false for second activation")
	}

	got = mgr.Get(sess.ID)
	if len(got.ActiveSkills) != 2 {
		t.Errorf("ActiveSkills length = %d, want 2", len(got.ActiveSkills))
	}

	// Dedup: activate same skill again
	ok = mgr.ActivateSkill(sess.ID, "code-review")
	if ok {
		t.Error("ActivateSkill should return false for duplicate")
	}

	got = mgr.Get(sess.ID)
	if len(got.ActiveSkills) != 2 {
		t.Errorf("ActiveSkills length after dedup = %d, want 2", len(got.ActiveSkills))
	}
}

func TestDeactivateSkill(t *testing.T) {
	mgr := session.NewManager(10)

	sess, _ := mgr.Create("browser-1")
	mgr.ActivateSkill(sess.ID, "code-review")
	mgr.ActivateSkill(sess.ID, "debug")

	mgr.DeactivateSkill(sess.ID, "code-review")
	got := mgr.Get(sess.ID)
	if len(got.ActiveSkills) != 1 || got.ActiveSkills[0] != "debug" {
		t.Errorf("ActiveSkills after deactivate = %v, want [debug]", got.ActiveSkills)
	}

	// Deactivate non-existent
	mgr.DeactivateSkill(sess.ID, "nonexistent")
	got = mgr.Get(sess.ID)
	if len(got.ActiveSkills) != 1 {
		t.Errorf("ActiveSkills after deactivate nonexistent = %v", got.ActiveSkills)
	}
}

func TestActiveSkills(t *testing.T) {
	mgr := session.NewManager(10)

	sess, _ := mgr.Create("browser-1")

	// Empty initially
	skills := mgr.ActiveSkills(sess.ID)
	if len(skills) != 0 {
		t.Errorf("initial ActiveSkills = %v, want empty", skills)
	}

	// After activation
	mgr.ActivateSkill(sess.ID, "code-review")
	skills = mgr.ActiveSkills(sess.ID)
	if len(skills) != 1 || skills[0] != "code-review" {
		t.Errorf("ActiveSkills = %v, want [code-review]", skills)
	}

	// Non-existent session
	if skills := mgr.ActiveSkills("nonexistent"); skills != nil {
		t.Errorf("ActiveSkills for nonexistent session = %v, want nil", skills)
	}
}

func TestAppendComponent(t *testing.T) {
	mgr := session.NewManager(10)

	sess, _ := mgr.Create("browser-1")

	// No assistant message yet — append should be no-op
	comp := session.ComponentData{Name: "diff_card", Data: map[string]interface{}{"key": "val"}}
	err := mgr.AppendComponent(sess.ID, comp)
	if err != nil {
		t.Fatalf("AppendComponent on empty session should not error: %v", err)
	}

	got := mgr.Get(sess.ID)
	if len(got.Messages) != 0 {
		t.Errorf("Messages count = %d, want 0 (no-op)", len(got.Messages))
	}

	// Add assistant message, then append component
	assistantMsg := session.Message{Role: "assistant", Content: "Here is the diff"}
	mgr.AppendMessage(sess.ID, assistantMsg)

	err = mgr.AppendComponent(sess.ID, session.ComponentData{Name: "diff_card", Data: map[string]interface{}{"diff": "+new code"}})
	if err != nil {
		t.Fatalf("AppendComponent failed: %v", err)
	}

	got = mgr.Get(sess.ID)
	if len(got.Messages) != 1 {
		t.Fatalf("Messages count = %d, want 1", len(got.Messages))
	}
	if len(got.Messages[0].Components) != 1 {
		t.Fatalf("Components count = %d, want 1", len(got.Messages[0].Components))
	}
	if got.Messages[0].Components[0].Name != "diff_card" {
		t.Errorf("Component name = %q, want %q", got.Messages[0].Components[0].Name, "diff_card")
	}
	if got.Messages[0].Components[0].Data["diff"] != "+new code" {
		t.Errorf("Component data diff = %v, want %v", got.Messages[0].Components[0].Data["diff"], "+new code")
	}

	// Append second component to same message
	err = mgr.AppendComponent(sess.ID, session.ComponentData{Name: "mermaid", Data: map[string]interface{}{"chart": "graph"}})
	if err != nil {
		t.Fatalf("AppendComponent second failed: %v", err)
	}

	got = mgr.Get(sess.ID)
	if len(got.Messages[0].Components) != 2 {
		t.Fatalf("Components count after second append = %d, want 2", len(got.Messages[0].Components))
	}
}

func TestAppendComponent_NoAssistantMessage(t *testing.T) {
	mgr := session.NewManager(10)

	sess, _ := mgr.Create("browser-1")

	// Only user message — no assistant message yet
	mgr.AppendMessage(sess.ID, session.Message{Role: "user", Content: "hello"})

	err := mgr.AppendComponent(sess.ID, session.ComponentData{Name: "diff_card", Data: nil})
	if err != nil {
		t.Fatalf("AppendComponent without assistant message should not error: %v", err)
	}

	got := mgr.Get(sess.ID)
	// AppendComponent now creates an assistant message when the last message is not assistant
	if len(got.Messages) != 2 {
		t.Fatalf("Messages count = %d, want 2 (user + auto-created assistant with component)", len(got.Messages))
	}
	// The user message should have 0 components
	if len(got.Messages[0].Components) != 0 {
		t.Errorf("User message components should be 0, got %d", len(got.Messages[0].Components))
	}
	// The auto-created assistant message should have the component
	if got.Messages[1].Role != "assistant" {
		t.Errorf("second message role = %q, want %q", got.Messages[1].Role, "assistant")
	}
	if len(got.Messages[1].Components) != 1 {
		t.Errorf("Assistant message components count = %d, want 1", len(got.Messages[1].Components))
	}
}

func TestAppendComponent_NonexistentSession(t *testing.T) {
	mgr := session.NewManager(10)

	err := mgr.AppendComponent("nonexistent", session.ComponentData{Name: "test", Data: nil})
	if err != nil {
		t.Errorf("AppendComponent for nonexistent session should not error, got: %v", err)
	}
}

func TestAppendComponent_ConcurrentSafety(t *testing.T) {
	mgr := session.NewManager(10)

	sess, _ := mgr.Create("browser-1")
	mgr.AppendMessage(sess.ID, session.Message{Role: "assistant", Content: "answer"})

	// Run concurrent appends to check for races
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mgr.AppendComponent(sess.ID, session.ComponentData{Name: "comp", Data: map[string]interface{}{"i": i}})
		}(i)
	}
	wg.Wait()

	got := mgr.Get(sess.ID)
	if len(got.Messages[0].Components) != 20 {
		t.Errorf("Components count after concurrent appends = %d, want 20", len(got.Messages[0].Components))
	}
}

func TestSetQuickReplies(t *testing.T) {
	mgr := session.NewManager(10)
	sess, _ := mgr.Create("browser-1")

	// No assistant message yet
	err := mgr.SetQuickReplies(sess.ID, []string{"yes", "no"})
	if err != nil {
		t.Fatalf("SetQuickReplies should not error: %v", err)
	}

	// Should have created an assistant message
	got := mgr.Get(sess.ID)
	if len(got.Messages) != 1 {
		t.Fatalf("Messages count = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].Role != "assistant" {
		t.Errorf("message role = %q, want %q", got.Messages[0].Role, "assistant")
	}
	if len(got.Messages[0].QuickReplies) != 2 || got.Messages[0].QuickReplies[0] != "yes" || got.Messages[0].QuickReplies[1] != "no" {
		t.Errorf("QuickReplies = %v, want [yes no]", got.Messages[0].QuickReplies)
	}

	// Overwrite existing quick replies
	err = mgr.SetQuickReplies(sess.ID, []string{"maybe"})
	if err != nil {
		t.Fatalf("SetQuickReplies overwrite should not error: %v", err)
	}
	got = mgr.Get(sess.ID)
	if len(got.Messages) != 1 {
		t.Fatalf("Messages count after overwrite = %d, want 1", len(got.Messages))
	}
	if len(got.Messages[0].QuickReplies) != 1 || got.Messages[0].QuickReplies[0] != "maybe" {
		t.Errorf("QuickReplies after overwrite = %v, want [maybe]", got.Messages[0].QuickReplies)
	}

	// Nonexistent session
	err = mgr.SetQuickReplies("nonexistent", []string{"x"})
	if err != nil {
		t.Errorf("SetQuickReplies for nonexistent session should not error, got: %v", err)
	}

	// Set on existing assistant message preserves quick replies on that message
	mgr2 := session.NewManager(10)
	sess2, _ := mgr2.Create("browser-2")
	mgr2.AppendMessage(sess2.ID, session.Message{Role: "user", Content: "hello"})
	mgr2.AppendMessage(sess2.ID, session.Message{Role: "assistant", Content: "world"})
	err = mgr2.SetQuickReplies(sess2.ID, []string{"a", "b"})
	if err != nil {
		t.Fatalf("SetQuickReplies on existing assistant should not error: %v", err)
	}
	got2 := mgr2.Get(sess2.ID)
	if len(got2.Messages) != 2 {
		t.Fatalf("Messages count = %d, want 2", len(got2.Messages))
	}
	if got2.Messages[1].Content != "world" {
		t.Errorf("Assistant content changed from %q to %q", "world", got2.Messages[1].Content)
	}
	if len(got2.Messages[1].QuickReplies) != 2 || got2.Messages[1].QuickReplies[0] != "a" {
		t.Errorf("Assistant QuickReplies = %v, want [a b]", got2.Messages[1].QuickReplies)
	}
}

func TestSetLastReasoningContent(t *testing.T) {
	t.Parallel()

	mgr := session.NewManager(10)
	sess, _ := mgr.Create("browser-1")

	// No assistant message yet → no-op
	mgr.SetLastReasoningContent(sess.ID, "should not appear")
	sess = mgr.Get(sess.ID)
	if len(sess.Messages) != 0 {
		t.Errorf("Messages count after no-op = %d, want 0", len(sess.Messages))
	}

	// Add user message first
	mgr.AppendMessage(sess.ID, session.Message{Role: "user", Content: "hello"})

	// No assistant message yet → still no-op
	mgr.SetLastReasoningContent(sess.ID, "should not appear 2")
	sess = mgr.Get(sess.ID)
	if len(sess.Messages) != 1 {
		t.Errorf("Messages count after second no-op = %d, want 1", len(sess.Messages))
	}

	// Add assistant message
	mgr.AppendMessage(sess.ID, session.Message{Role: "assistant", Content: "answer"})

	// Set reasoning content
	mgr.SetLastReasoningContent(sess.ID, "reasoning text")
	sess = mgr.Get(sess.ID)
	if len(sess.Messages) != 2 {
		t.Fatalf("Messages count = %d, want 2", len(sess.Messages))
	}
	if sess.Messages[1].ReasoningContent != "reasoning text" {
		t.Errorf("ReasoningContent = %q, want %q", sess.Messages[1].ReasoningContent, "reasoning text")
	}
	if sess.Messages[1].Content != "answer" {
		t.Errorf("Content changed from %q to %q", "answer", sess.Messages[1].Content)
	}

	// Overwrite reasoning content
	mgr.SetLastReasoningContent(sess.ID, "new reasoning")
	sess = mgr.Get(sess.ID)
	if sess.Messages[1].ReasoningContent != "new reasoning" {
		t.Errorf("ReasoningContent after overwrite = %q, want %q", sess.Messages[1].ReasoningContent, "new reasoning")
	}

	// Nonexistent session
	mgr.SetLastReasoningContent("nonexistent", "x")
}

func TestAppendLastReasoningContent(t *testing.T) {
	t.Parallel()

	mgr := session.NewManager(10)
	sess, _ := mgr.Create("browser-1")

	// No assistant message yet → no-op
	mgr.AppendLastReasoningContent(sess.ID, "should not appear")
	sess = mgr.Get(sess.ID)
	if len(sess.Messages) != 0 {
		t.Errorf("Messages count after no-op = %d, want 0", len(sess.Messages))
	}

	// Add assistant message
	mgr.AppendMessage(sess.ID, session.Message{Role: "assistant", Content: "answer"})

	// Append reasoning content (simulating streaming)
	mgr.AppendLastReasoningContent(sess.ID, "part1")
	mgr.AppendLastReasoningContent(sess.ID, "part2")
	mgr.AppendLastReasoningContent(sess.ID, "part3")

	sess = mgr.Get(sess.ID)
	if len(sess.Messages) != 1 {
		t.Fatalf("Messages count = %d, want 1", len(sess.Messages))
	}
	want := "part1part2part3"
	if sess.Messages[0].ReasoningContent != want {
		t.Errorf("ReasoningContent after appends = %q, want %q", sess.Messages[0].ReasoningContent, want)
	}

	// Nonexistent session
	mgr.AppendLastReasoningContent("nonexistent", "x")
}

func TestReasoningContentInAppendMessage(t *testing.T) {
	t.Parallel()

	mgr := session.NewManager(10)
	sess, _ := mgr.Create("browser-1")

	// Create message with reasoning content directly
	mgr.AppendMessage(sess.ID, session.Message{
		Role:             "assistant",
		Content:          "final answer",
		ReasoningContent: "my reasoning",
	})

	sess = mgr.Get(sess.ID)
	if sess.Messages[0].ReasoningContent != "my reasoning" {
		t.Errorf("ReasoningContent = %q, want %q", sess.Messages[0].ReasoningContent, "my reasoning")
	}
	if sess.Messages[0].Content != "final answer" {
		t.Errorf("Content = %q, want %q", sess.Messages[0].Content, "final answer")
	}
}

package session_test

import (
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

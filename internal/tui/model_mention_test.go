package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// mentionWorkspace builds a temp workspace with a known tree for dropdown tests.
func mentionWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "main.go"), "package main\n")
	mustWriteFile(t, filepath.Join(dir, "readme.md"), "# hi\n")
	mustMakeDir(t, filepath.Join(dir, "src"))
	mustWriteFile(t, filepath.Join(dir, "src", "util.go"), "x\n")
	return dir
}

func mentionModel(t *testing.T, workspace string) Model {
	t.Helper()
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, _ string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		WorkspacePath: workspace,
	})
	return resize(t, m)
}

// feedMentionWalk simulates the async workspace walk completing and delivering its
// manifest into the mention dropdown, mirroring the real mentionWalkMsg path.
func feedMentionWalk(t *testing.T, m Model, workspace string) Model {
	t.Helper()
	nm, _ := m.Update(mentionWalkMsg{paths: walkWorkspace(workspace)})
	return asModel(t, nm)
}

func TestModel_mentionOpenListsRootCandidates(t *testing.T) {
	t.Parallel()
	ws := mentionWorkspace(t)
	m := mentionModel(t, ws)
	m = typeText(t, m, "@")
	m = feedMentionWalk(t, m, ws)
	if !m.mention.isOpen() {
		t.Fatalf("typing @ at a word boundary should open the mention dropdown")
	}
	content := view(m)
	for _, want := range []string{"main.go", "readme.md", "src/"} {
		if !strings.Contains(content, want) {
			t.Errorf("dropdown missing %q, got: %q", want, content)
		}
	}
}

func TestModel_mentionCompletionRendersWorkspaceMentionsPopover(t *testing.T) {
	t.Parallel()
	ws := mentionWorkspace(t)
	m := mentionModel(t, ws)
	m = typeText(t, m, "@")
	m = feedMentionWalk(t, m, ws)

	content := ansiStrip(view(m))
	mentions := strings.Index(content, "Workspace mentions")
	ask := strings.Index(content, "Ask Eitri")
	hints := strings.Index(content, "navigate")
	if mentions == -1 {
		t.Fatalf("mention completion missing Workspace mentions popover, got:\n%s", content)
	}
	if ask == -1 || mentions > ask {
		t.Fatalf("Workspace mentions popover must render above Ask Eitri panel, got:\n%s", content)
	}
	if hints == -1 || hints < ask {
		t.Fatalf("completion hints must render below composer panel, got:\n%s", content)
	}
	if !strings.Contains(content, "▸ main.go") || !strings.Contains(content, "  readme.md") {
		t.Fatalf("mention candidates missing selected/non-selected rows, got:\n%s", content)
	}
}

func TestModel_mentionNavigateAndSelect(t *testing.T) {
	t.Parallel()
	ws := mentionWorkspace(t)
	m := mentionModel(t, ws)
	m = typeText(t, m, "@")
	m = feedMentionWalk(t, m, ws)
	first := m.mention.SelectedCandidate()
	m = keypress(t, m, "down")
	second := m.mention.SelectedCandidate()
	if first == second {
		t.Errorf("arrow down should change selection, both %q", first)
	}
	if !strings.Contains(view(m), "▸ "+second) {
		t.Errorf("view should highlight selected candidate %q, got: %q", second, view(m))
	}
	// select the highlighted candidate: the @partial is replaced by the bare path
	m = keypress(t, m, "enter")
	if got := m.composer.Value(); got != second {
		t.Errorf("after select draft = %q, want bare path %q", got, second)
	}
	if m.mention.isOpen() {
		t.Error("mention dropdown should close after selection")
	}
}

func TestModel_mentionAcceptsWithTab(t *testing.T) {
	t.Parallel()
	ws := mentionWorkspace(t)
	m := mentionModel(t, ws)
	m = typeText(t, m, "@read")
	m = feedMentionWalk(t, m, ws)
	m = keypress(t, m, "tab")
	if got := m.composer.Value(); got != "readme.md" {
		t.Fatalf("tab completion = %q, want readme.md", got)
	}
	if m.mention.isOpen() {
		t.Fatal("tab completion should close mention dropdown")
	}
}

func TestModel_mentionSelectPreservesRest(t *testing.T) {
	t.Parallel()
	ws := mentionWorkspace(t)
	m := mentionModel(t, ws)
	m = typeText(t, m, "go @src")
	m = feedMentionWalk(t, m, ws)
	if !m.mention.isOpen() {
		t.Fatalf("caret on @src should keep the dropdown open")
	}
	// pick the src/ folder candidate: it descends instead of closing
	if got := m.mention.SelectedCandidate(); got != "src/" {
		t.Fatalf("expected src/ candidate, got %q", got)
	}
	m = keypress(t, m, "enter")
	if got := m.composer.Value(); got != "go @src/" {
		t.Errorf("after folder select draft = %q, want @src/ with dropdown kept open", got)
	}
	if !m.mention.isOpen() {
		t.Error("selecting a folder must keep the mention dropdown open")
	}
}

func TestModel_mentionDescendsIntoFolderToFullPath(t *testing.T) {
	t.Parallel()
	ws := mentionWorkspace(t)
	m := mentionModel(t, ws)
	m = typeText(t, m, "@")
	m = feedMentionWalk(t, m, ws)
	if !m.mention.isOpen() {
		t.Fatalf("dropdown should open on @")
	}
	// move the selection to the src/ folder and descend into it
	for m.mention.SelectedCandidate() != "src/" {
		m = keypress(t, m, "down")
	}
	m = keypress(t, m, "enter")
	if got := m.composer.Value(); got != "@src/" {
		t.Fatalf("after descend draft = %q, want @src/", got)
	}
	// the dropdown now lists the folder's children
	if got := m.mention.SelectedCandidate(); got != "src/util.go" {
		t.Fatalf("child candidate = %q, want src/util.go", got)
	}
	// selecting the file completes the full path and closes the dropdown
	m = keypress(t, m, "enter")
	if got := m.composer.Value(); got != "src/util.go" {
		t.Errorf("after file select draft = %q, want src/util.go", got)
	}
	if m.mention.isOpen() {
		t.Error("selecting a file must close the mention dropdown")
	}
}

func TestModel_mentionEscDismisses(t *testing.T) {
	t.Parallel()
	m := mentionModel(t, mentionWorkspace(t))
	m = typeText(t, m, "@")
	if !m.mention.isOpen() {
		t.Fatalf("dropdown should open on @")
	}
	m = keypress(t, m, "esc")
	if m.mention.isOpen() {
		t.Error("esc should dismiss the mention dropdown")
	}
	if got := m.composer.Value(); got != "@" {
		t.Errorf("esc must not alter the draft, got %q", got)
	}
}

func TestModel_mentionCaretOffTokenDismisses(t *testing.T) {
	t.Parallel()
	m := mentionModel(t, mentionWorkspace(t))
	m = typeText(t, m, "@")
	if !m.mention.isOpen() {
		t.Fatalf("dropdown should open on @")
	}
	m = keypress(t, m, "left") // move the caret before the @ token
	if m.mention.isOpen() {
		t.Error("moving the caret off the @ token should dismiss the dropdown")
	}
}

func TestModel_mentionMidWordStaysLiteral(t *testing.T) {
	t.Parallel()
	m := mentionModel(t, mentionWorkspace(t))
	m = typeText(t, m, "foo@bar.com")
	if m.mention.isOpen() {
		t.Error("a mid-word @ (email) must not open the dropdown")
	}
}

func TestModel_mentionReFiltersInPlaceOnEdit(t *testing.T) {
	t.Parallel()
	ws := mentionWorkspace(t)
	m := mentionModel(t, ws)
	m = typeText(t, m, "@src/")
	m = feedMentionWalk(t, m, ws)
	if got := m.mention.SelectedCandidate(); got != "src/util.go" {
		t.Errorf("descendant candidate = %q, want src/util.go", got)
	}
	if !strings.Contains(view(m), "▸ src/util.go") {
		t.Errorf("view should highlight src/util.go, got: %q", view(m))
	}
}

func TestMentionSelect_PreservesOtherMentions(t *testing.T) {
	t.Parallel()
	mn := NewMention("/tmp/ws")
	value := "see @a and @b"
	mn.start = 4 // the first '@'
	mn.partial = "a"
	mn.open = true
	mn.cands = []string{"alpha.txt"}
	mn.idx = 0
	next, ok := mn.Select(value)
	if !ok {
		t.Fatalf("Select on an open dropdown must replace the partial")
	}
	if next != "see alpha.txt and @b" {
		t.Errorf("Select = %q, want only the first mention replaced and @b preserved", next)
	}
}

package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/skillindex"
)

// TestWorkspaceDirectiveNamesWritablePaths guards the per-run statement of what
// the run may write. Without it the model learns the boundary only by writing
// outside it and reading EROFS, which costs turns on every infrastructure tool
// that keeps state under $HOME.
func TestWorkspaceDirectiveNamesWritablePaths(t *testing.T) {
	t.Parallel()
	writable := []string{"/srv/work", "/home/u/.eitri/sessions/abc/tmp", "/home/u/.kube"}
	got := workspaceDirective("/srv/work", writable)

	if !strings.Contains(got, "## Working directory") || !strings.Contains(got, "/srv/work") {
		t.Fatalf("directive lost the working-directory statement: %q", got)
	}
	if !strings.Contains(got, "## Write permissions") {
		t.Fatalf("directive missing the write-permissions section: %q", got)
	}
	for _, p := range writable {
		if !strings.Contains(got, p) {
			t.Errorf("directive omits writable path %q: %q", p, got)
		}
	}
	if !strings.Contains(got, "read-only") {
		t.Errorf("directive must state that every other path is read-only: %q", got)
	}
}

// TestWorkspaceDirectiveOmitsWriteSectionWhenUnconfined guards the
// unsandboxed (--yolo-unsafe) case: with no writable subset to report the run
// must not claim one, and the bash tool description is what already states the
// absence of a sandbox.
func TestWorkspaceDirectiveOmitsWriteSectionWhenUnconfined(t *testing.T) {
	t.Parallel()
	got := workspaceDirective("/srv/work", nil)

	if strings.Contains(got, "## Write permissions") || strings.Contains(got, "read-only") {
		t.Fatalf("unconfined run must not claim a write boundary: %q", got)
	}
	if !strings.Contains(got, "## Working directory") {
		t.Fatalf("directive lost the working-directory statement: %q", got)
	}
}

// TestWorkspaceDirectiveStatesWhereTheShellStarts guards the cwd statement. This
// directive is the only model-facing text placing a bash call's shell, and the
// tool schema names no working directory, so without it the model hedges with
// `cd <workspace> && …` on every command — a redundant absolute path replayed
// for the rest of the session. The sentence must state shell state (where it
// starts), not merely how relative paths resolve, and must survive both the
// confined and unconfined forms.
func TestWorkspaceDirectiveStatesWhereTheShellStarts(t *testing.T) {
	t.Parallel()
	for name, writable := range map[string][]string{
		"confined":   {"/srv/work", "/srv/work/tmp"},
		"unconfined": nil,
	} {
		got := workspaceDirective("/srv/work", writable)
		folded := strings.ToLower(got)
		for _, want := range []string{"fresh shell", "started there", "cd"} {
			if !strings.Contains(folded, want) {
				t.Errorf("%s directive must state that every bash call is a fresh shell already started in the workspace, and that no cd is needed; missing %q: %q", name, want, got)
			}
		}
	}
}

// TestRunAgentOrdersPerRunDirectiveAfterTheStaticSystemLayer guards the cache
// layout: a provider cache write covers everything up to its breakpoint, so the
// per-run directive — whose text carries the session temp path and the resolved
// writable set — must follow the byte-stable messages, never precede them. The
// declared StaticPrefixLen is what the dialect puts the breakpoint on, so a
// mis-ordered system layer would silently widen every cache hash with
// per-session state.
func TestRunAgentOrdersPerRunDirectiveAfterTheStaticSystemLayer(t *testing.T) {
	t.Parallel()
	c := &skillIndexCaptureHandler{}
	e := New(provider.NewScripted(c.stream), nil)

	idx := skillindex.OpenTag + "<skill><name>review</name></skill>" + skillindex.CloseTag
	repo := "# AGENTS.md\n\nfollow them\n"
	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:            "deepseek-v4-flash",
		Prompt:           "hi",
		SessionKey:       "sess-abc",
		Workspace:        "/srv/work",
		WritablePaths:    []string{"/srv/work"},
		SkillIndex:       &idx,
		RepoInstructions: &repo,
	}, AgentOptions{MaxTurns: 1}); err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}
	if len(c.requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(c.requests))
	}
	msgs := c.requests[0].Messages
	if len(msgs) != 5 {
		t.Fatalf("got %d messages, want 5 (head, skill index, repo instructions, workspace directive, user)", len(msgs))
	}
	if msgs[0].Content != SystemPromptContent() {
		t.Errorf("messages[0] is not the persona head: %.40q", msgs[0].Content)
	}
	if msgs[1].Content != idx {
		t.Errorf("messages[1] is not the skill index: %.40q", msgs[1].Content)
	}
	if !strings.Contains(msgs[2].Content, "## Repository instructions (AGENTS.md)") {
		t.Errorf("messages[2] is not the repo instructions: %.40q", msgs[2].Content)
	}
	if !strings.Contains(msgs[3].Content, "## Working directory") {
		t.Errorf("messages[3] is not the per-run workspace directive: %.40q", msgs[3].Content)
	}
	if msgs[4].Role != provider.RoleUser || msgs[4].Content != "hi" {
		t.Errorf("messages[4] not the user prompt: role=%s content=%q", msgs[4].Role, msgs[4].Content)
	}
	if got, want := c.requests[0].StaticPrefixLen, 3; got != want {
		t.Errorf("StaticPrefixLen = %d, want %d (the workspace directive is per-run state)", got, want)
	}
}

package engine

import (
	"context"
	"reflect"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

func TestPartitionMessagesSharesStableHeadAcrossCompactionAndPersistence(t *testing.T) {
	persona := provider.Message{Role: provider.RoleSystem, Content: SystemPromptContent()}
	workspace := provider.Message{Role: provider.RoleSystem, Content: "## Working directory\n/workspace"}
	skills := provider.Message{Role: provider.RoleSystem, Content: "<available_skills>index</available_skills>"}
	repo := provider.Message{Role: provider.RoleSystem, Content: "## Repository instructions (AGENTS.md)\nfollow them"}
	activation := provider.Message{Role: provider.RoleUser, Content: "<skill_content name=\"tdd\">test first</skill_content>\n\nrequest"}
	conversation := []provider.Message{{Role: provider.RoleAssistant, Content: "answer"}, {Role: provider.RoleUser, Content: "next"}}
	messages := append([]provider.Message{persona, workspace, skills, repo, activation}, conversation...)

	got := partitionMessages(messages)
	if want := []provider.Message{persona, workspace, skills, repo}; !reflect.DeepEqual(got.StableHead, want) {
		t.Fatalf("StableHead = %#v, want %#v", got.StableHead, want)
	}
	if want := []provider.Message{activation}; !reflect.DeepEqual(got.Transient, want) {
		t.Fatalf("Transient = %#v, want %#v", got.Transient, want)
	}
	if !reflect.DeepEqual(got.History, conversation) {
		t.Fatalf("History = %#v, want %#v", got.History, conversation)
	}
	if want := append([]provider.Message{activation}, conversation...); !reflect.DeepEqual(got.PersistedHistory(), want) {
		t.Fatalf("PersistedHistory = %#v, want %#v", got.PersistedHistory(), want)
	}
}

func TestCompactionProtectsTransientSkillActivationAfterConversationHistory(t *testing.T) {
	e := New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(provider.Chunk{Done: true}), nil
	}), &mockTranscript{})
	activation := provider.Message{Role: provider.RoleUser, Content: "<skill_content name=\"tdd\">test first</skill_content>\n\nnew request"}
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: SystemPromptContent()},
		{Role: provider.RoleUser, Content: "old prompt"},
		{Role: provider.RoleAssistant, Content: "old answer one"},
		{Role: provider.RoleUser, Content: "middle prompt"},
		{Role: provider.RoleAssistant, Content: "old answer two"},
		activation,
		{Role: provider.RoleAssistant, Content: "new answer"},
	}
	cfg := compactCfg()
	cfg.Prune = true
	got, compacted := e.maybeCompact(context.Background(), RunRequest{}, AgentOptions{Compaction: cfg}, messages, true, 1)
	if !compacted {
		t.Fatal("expected forced compaction")
	}
	for _, message := range got {
		if reflect.DeepEqual(message, activation) {
			return
		}
	}
	t.Fatalf("activation after conversation history was evicted: %#v", got)
}

func TestPartitionRecognizesYoloPromptHead(t *testing.T) {
	persona := provider.Message{Role: provider.RoleSystem, Content: SystemPromptYoloContent()}
	workspace := provider.Message{Role: provider.RoleSystem, Content: "## Working directory\n/workspace"}
	repo := provider.Message{Role: provider.RoleSystem, Content: "## Repository instructions (AGENTS.md)\nfollow them"}
	conversation := []provider.Message{{Role: provider.RoleAssistant, Content: "answer"}, {Role: provider.RoleUser, Content: "next"}}
	messages := append([]provider.Message{persona, workspace, repo}, conversation...)

	got := partitionMessages(messages)
	if want := []provider.Message{persona, workspace, repo}; !reflect.DeepEqual(got.StableHead, want) {
		t.Fatalf("StableHead = %#v, want %#v", got.StableHead, want)
	}
	if !reflect.DeepEqual(got.History, conversation) {
		t.Fatalf("History = %#v, want %#v", got.History, conversation)
	}
}

func TestStoreSessionHistoryUsesSharedPartitionAndPreservesOrder(t *testing.T) {
	e := New(provider.NewFake(""), &mockTranscript{})
	persona := provider.Message{Role: provider.RoleSystem, Content: SystemPromptContent()}
	workspace := provider.Message{Role: provider.RoleSystem, Content: "## Working directory\n/workspace"}
	skills := provider.Message{Role: provider.RoleSystem, Content: "<available_skills>index</available_skills>"}
	repo := provider.Message{Role: provider.RoleSystem, Content: "## Repository instructions (AGENTS.md)\nfollow them"}
	activation := provider.Message{Role: provider.RoleUser, Content: "<skill_content name=\"tdd\">test first</skill_content>\n\nrequest"}
	conversation := []provider.Message{{Role: provider.RoleAssistant, Content: "answer"}, {Role: provider.RoleUser, Content: "next"}}

	e.storeSessionHistory(t.Name(), append([]provider.Message{persona, workspace, skills, repo, activation}, conversation...))
	want := append([]provider.Message{activation}, conversation...)
	if got := e.sessionHistory(t.Name()); !reflect.DeepEqual(got, want) {
		t.Fatalf("sessionHistory = %#v, want %#v", got, want)
	}
}

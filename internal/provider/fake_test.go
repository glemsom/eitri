package provider

import (
	"context"
	"errors"
	"io"
	"testing"
)

// TestFakeProviderStreamsFixture drives the fake Chat-Completions provider over
// a committed source-of-truth fixture, asserting the full chunk stream: answer
// content, reasoning_content surface, terminal usage, Done, then io.EOF.
func TestFakeProviderStreamsFixture(t *testing.T) {
	t.Parallel()
	fp := NewFake("testdata/hello.sse")
	s, err := fp.Stream(context.TODO(), Request{Model: "deepseek-v4-flash"})
	if err != nil {
		t.Fatalf("Fake.Stream() error = %v, want nil", err)
	}

	var gotContent, gotReasoning string
	var gotUsage *Usage
	var sawDone bool

	for {
		c, err := s.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("Fake Stream Next() error = %v, want nil then EOF", err)
		}
		if c.Done {
			sawDone = true
		}
		gotContent += c.Content
		gotReasoning += c.ReasoningContent
		if c.Usage != nil {
			gotUsage = c.Usage
		}
	}

	if !sawDone {
		t.Fatal("stream never produced a Done chunk")
	}
	if gotContent != "Hello world" {
		t.Fatalf("answer content = %q, want %q", gotContent, "Hello world")
	}
	if gotReasoning != "think step by step" {
		t.Fatalf("reasoning = %q, want %q", gotReasoning, "think step by step")
	}
	if gotUsage == nil {
		t.Fatal("usage not parsed from the stream")
	} else if gotUsage.PromptTokens != 12 || gotUsage.CompletionTokens != 5 {
		t.Fatalf("usage = %+v, want prompt=12 completion=5", gotUsage)
	}
}

// TestFakeProviderCompletesWithUsageOnlyChunk verifies a stream that carries
// usage in its terminal chunk still terminates with Done + EOF.
func TestFakeProviderCompletesWithUsageOnlyChunk(t *testing.T) {
	t.Parallel()
	fp := NewFake("testdata/usage-final.sse")
	s, err := fp.Stream(context.TODO(), Request{})
	if err != nil {
		t.Fatalf("Fake.Stream() error = %v, want nil", err)
	}
	// Consume to completion; the stream must terminate with a Done chunk and
	// then io.EOF, carrying usage on the terminal content chunk.
	var sawDone bool
	var usage *Usage
	for {
		c, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v, want nil then EOF", err)
		}
		if c.Done {
			sawDone = true
		}
		if c.Usage != nil {
			usage = c.Usage
		}
	}
	if !sawDone {
		t.Fatal("stream never produced a Done chunk")
	}
	if usage == nil || usage.CompletionTokens != 1 {
		t.Fatalf("terminal usage = %+v, want completion=1", usage)
	}
}

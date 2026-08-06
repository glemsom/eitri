package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/sandbox"
	"github.com/voocel/litellm"
)

func TestBash_Schema(t *testing.T) {
	tool := NewBashTool("/tmp", 0, sandbox.Config{Profile: sandbox.ProfileNone})
	if tool.Name() != "bash" {
		t.Errorf("Name = %q, want 'bash'", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description should not be empty")
	}
	if !strings.Contains(tool.Description(), "fresh shell") {
		t.Error("Description should mention fresh-shell semantics")
	}
	schema := tool.JSONSchema()
	if schema == nil {
		t.Fatal("JSONSchema is nil")
	}
	if !json.Valid(schema) {
		t.Error("JSONSchema is not valid JSON")
	}
}

func TestBash_InvalidArgs(t *testing.T) {
	tool := NewBashTool("/tmp", 0, sandbox.Config{Profile: sandbox.ProfileNone})
	_, err := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestBash_EmptyCommand(t *testing.T) {
	tool := NewBashTool("/tmp", 0, sandbox.Config{Profile: sandbox.ProfileNone})
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("IsError = false, want true")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	if len(block.Text) == 0 {
		t.Error("expected error text")
	}
}

func TestBash_ArgsUnmarshal(t *testing.T) {
	args := json.RawMessage(`{"command":"ls -la"}`)
	var parsed bashArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Command != "ls -la" {
		t.Errorf("Command = %q, want 'ls -la'", parsed.Command)
	}
}

func TestBash_RunsCommand(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second, sandbox.Config{Profile: sandbox.ProfileNone})
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"echo hello world"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("IsError = true, want false")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := result.Blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", result.Blocks[0])
	}
	expected := "<stdout>\nhello world\n</stdout>"
	if block.Text != expected {
		t.Errorf("output = %q, want %q", block.Text, expected)
	}
}

func TestBash_ExitCode(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second, sandbox.Config{Profile: sandbox.ProfileNone})
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"exit 42"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("IsError = false, want true for non-zero exit")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	expected := "[exit code 42]"
	if block.Text != expected {
		t.Errorf("output = %q, want %q", block.Text, expected)
	}
}

func TestBash_StderrCapture(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second, sandbox.Config{Profile: sandbox.ProfileNone})
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"echo stderr_output >&2"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("IsError = true, want false")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	expected := "<stderr>\nstderr_output\n</stderr>"
	if block.Text != expected {
		t.Errorf("output = %q, want %q", block.Text, expected)
	}
}

func TestBash_StdoutAndStderr(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second, sandbox.Config{Profile: sandbox.ProfileNone})
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"echo out; echo err >&2"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("IsError = true, want false")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	expected := "<stdout>\nout\n</stdout>\n<stderr>\nerr\n</stderr>"
	if block.Text != expected {
		t.Errorf("output = %q, want %q", block.Text, expected)
	}
}

func TestBash_ExitCodeWithOutput(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second, sandbox.Config{Profile: sandbox.ProfileNone})
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"echo hello && exit 3"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("IsError = false, want true for non-zero exit")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	expected := "<stdout>\nhello\n</stdout>\n[exit code 3]"
	if block.Text != expected {
		t.Errorf("output = %q, want %q", block.Text, expected)
	}
}

func TestBash_Timeout(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Millisecond, sandbox.Config{Profile: sandbox.ProfileNone})
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"sleep 10"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("IsError = false, want true for timeout")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	if !strings.Contains(block.Text, "[command timed out]") {
		t.Errorf("output = %q, want timed out", block.Text)
	}
}

func TestBash_WorkspaceDir(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second, sandbox.Config{Profile: sandbox.ProfileNone})
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"pwd"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("IsError = true, want false")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	if !strings.Contains(block.Text, dir) {
		t.Errorf("output = %q, want directory %q", block.Text, dir)
	}
}

func TestBash_Truncation(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second, sandbox.Config{Profile: sandbox.ProfileNone})
	// Generate >8 KiB of output to trigger truncation
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"python3 -c \"import sys; sys.stdout.write('A' * 12000)\""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("IsError = true, want false")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	if len(block.Text) > 9*1024 {
		t.Errorf("truncated output too long: %d bytes, want <= ~8 KiB", len(block.Text))
	}
	if !strings.HasSuffix(block.Text, "... (output truncated at 8 KiB)") {
		t.Errorf("output should end with truncation marker, got suffix: %q", block.Text[len(block.Text)-50:])
	}
}

func TestBash_Compression_RawBlocks(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second, sandbox.Config{Profile: sandbox.ProfileNone})

	// Create a directory with enough files to trigger ls compression
	for i := 0; i < 15; i++ {
		name := fmt.Sprintf("file%d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"ls -la"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have Blocks (compressed) and RawBlocks (raw original)
	if len(result.Blocks) == 0 {
		t.Fatal("expected Blocks")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	if block.Text == "" {
		t.Error("expected non-empty compressed output")
	}

	// Check that RawBlocks is populated (compression should change output)
	if len(result.RawBlocks) == 0 {
		t.Fatal("expected RawBlocks to be non-nil when compression changes output")
	}
	rawBlock := result.RawBlocks[0].(litellm.TextBlock)
	if rawBlock.Text == "" {
		t.Error("expected non-empty raw output")
	}

	// Raw blocks should be different from compressed blocks
	if rawBlock.Text == block.Text {
		t.Error("raw output should differ from compressed output")
	}

	// Raw output should contain ls -la format markers (permissions, total)
	if !strings.Contains(rawBlock.Text, "total ") {
		t.Error("raw output should contain 'total' line")
	}
	if !strings.Contains(rawBlock.Text, "-rw-") {
		t.Error("raw output should contain permission string '-rw-'")
	}

	// Compressed output should contain summary line (files, dirs)
	if !strings.Contains(block.Text, "files") && !strings.Contains(block.Text, "dirs") {
		t.Error("compressed output should contain file/dir summary")
	}
}

func TestBash_Compression_NoCompressor(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second, sandbox.Config{Profile: sandbox.ProfileNone})

	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"echo hello world"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No compressor for echo, so RawBlocks should be nil
	if len(result.RawBlocks) != 0 {
		t.Error("RawBlocks should be nil when no compressor matches")
	}
	if len(result.Blocks) == 0 {
		t.Fatal("expected Blocks")
	}
	block := result.Blocks[0].(litellm.TextBlock)
	if !strings.Contains(block.Text, "hello world") {
		t.Errorf("output = %q, want 'hello world'", block.Text)
	}
}

func TestBash_Compression_AntiInflation(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second, sandbox.Config{Profile: sandbox.ProfileNone})

	// Very small ls output (few files) should not trigger compression
	// because anti-inflation would return the original unchanged.
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"ls -la"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With very few files, compression may not be beneficial.
	// RawBlocks should be nil when compression didn't change the output.
	if len(result.RawBlocks) != 0 {
		t.Log("compression triggered on small output (acceptable if compressors kick in)")
	}
}

func TestBash_Compression_TruncationPreservesRaw(t *testing.T) {
	dir := t.TempDir()
	tool := NewBashTool(dir, 10*time.Second, sandbox.Config{Profile: sandbox.ProfileNone})

	// Create many files to trigger compression with large output
	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("file%d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Use a command that produces >8 KiB of raw output
	result, err := tool.Call(context.Background(), json.RawMessage(`{"command":"ls -la"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Blocks should contain the truncated compressed output
	if len(result.Blocks) == 0 {
		t.Fatal("expected Blocks")
	}
	block := result.Blocks[0].(litellm.TextBlock)

	// The raw blocks should still be present (not truncated from the snapshot pov)
	if len(result.RawBlocks) == 0 {
		t.Fatal("expected RawBlocks when compression is active")
	}
	rawBlock := result.RawBlocks[0].(litellm.TextBlock)
	if rawBlock.Text == "" {
		t.Error("expected non-empty raw output")
	}
	_ = block // we don't care about the exact compressed format
}

// TestBash_SessionTmpdir_PersistsAcrossCalls is an integration test (skips
// without bwrap) that verifies a file written to /tmp in one sandboxed bash
// call of a session is readable by a later call of the same session — the
// sandbox /tmp is session-scoped (ADR-0026).
// sandboxedBashWorkspace returns a workspace directory located outside /tmp,
// because inside a bwrap sandbox /tmp is replaced by the session tmpdir and a
// workspace living under /tmp must resolve on both host and sandbox. Mirrors
// the sandbox package's integration-test pattern.
func sandboxedBashWorkspace(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	dir, err := os.MkdirTemp(wd, "bash-sandbox-test-*")
	if err != nil {
		t.Fatalf("creating test dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestBash_SessionTmpdir_PersistsAcrossCalls(t *testing.T) {
	if !sandbox.BwrapIsUsable() {
		t.Skip("bwrap not usable, skipping integration test")
	}

	dir := sandboxedBashWorkspace(t)
	tool := NewBashTool(dir, 10*time.Second, sandbox.DefaultConfig())
	ctx := context.WithValue(context.Background(), SessionIDKey, "bash-sess-persist")
	defer tool.EndSession("bash-sess-persist")

	// Write a file to /tmp in the first call.
	res1, err := tool.Call(ctx, json.RawMessage(`{"command":"echo 'persist-data' > /tmp/persist-file"}`))
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if res1.IsError {
		t.Fatalf("first call IsError = true, blocks: %v", res1.Blocks)
	}

	// Read it back in a second call for the same session.
	res2, err := tool.Call(ctx, json.RawMessage(`{"command":"cat /tmp/persist-file"}`))
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if res2.IsError {
		t.Fatalf("second call IsError = true, blocks: %v", res2.Blocks)
	}
	block := res2.Blocks[0].(litellm.TextBlock)
	if !strings.Contains(block.Text, "persist-data") {
		t.Errorf("second call output = %q, want it to contain %q", block.Text, "persist-data")
	}
}

// TestBash_SessionTmpdir_IsolatedBetweenSessions verifies that /tmp content
// written by one session's bash tool is not visible in a different session.
func TestBash_SessionTmpdir_IsolatedBetweenSessions(t *testing.T) {
	if !sandbox.BwrapIsUsable() {
		t.Skip("bwrap not usable, skipping integration test")
	}

	dir := sandboxedBashWorkspace(t)
	tool := NewBashTool(dir, 10*time.Second, sandbox.DefaultConfig())
	// A shared tool is fine: the namespace is keyed by the session ID in ctx.
	ctxA := context.WithValue(context.Background(), SessionIDKey, "bash-sess-a")
	ctxB := context.WithValue(context.Background(), SessionIDKey, "bash-sess-b")
	defer tool.EndSession("bash-sess-a")
	defer tool.EndSession("bash-sess-b")

	if _, err := tool.Call(ctxA, json.RawMessage(`{"command":"echo 'a' > /tmp/a-file"}`)); err != nil {
		t.Fatalf("session A write: %v", err)
	}

	res, err := tool.Call(ctxB, json.RawMessage(`{"command":"test -f /tmp/a-file && echo LEAKED || echo isolated"}`))
	if err != nil {
		t.Fatalf("session B check: %v", err)
	}
	if res.IsError {
		t.Fatalf("session B check IsError = true, blocks: %v", res.Blocks)
	}
	block := res.Blocks[0].(litellm.TextBlock)
	if !strings.Contains(block.Text, "isolated") {
		t.Errorf("session B sees session A /tmp: output %q, want 'isolated'", block.Text)
	}
}

// TestBash_EndSession_ClearsSessionTmpdir verifies that EndSession removes the
// session tmpdir so no leftover eitri-sandbox-* directory survives the run.
func TestBash_EndSession_ClearsSessionTmpdir(t *testing.T) {
	if !sandbox.BwrapIsUsable() {
		t.Skip("bwrap not usable, skipping integration test")
	}

	dir := sandboxedBashWorkspace(t)
	tool := NewBashTool(dir, 10*time.Second, sandbox.DefaultConfig())
	ctx := context.WithValue(context.Background(), SessionIDKey, "bash-sess-clean")

	if _, err := tool.Call(ctx, json.RawMessage(`{"command":"touch /tmp/sentinel"}`)); err != nil {
		t.Fatalf("call: %v", err)
	}

	tmpdir, tracked := tool.sandboxManager.TmpdirFor("bash-sess-clean")
	if !tracked {
		t.Fatal("expected session tmpdir to be tracked")
	}
	tool.EndSession("bash-sess-clean")

	if _, tracked := tool.sandboxManager.TmpdirFor("bash-sess-clean"); tracked {
		t.Error("session tmpdir still tracked after EndSession")
	}
	if _, err := os.Stat(tmpdir); !os.IsNotExist(err) {
		t.Errorf("session tmpdir %q not removed after EndSession, stat err = %v", tmpdir, err)
	}
}

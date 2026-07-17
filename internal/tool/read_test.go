package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/litellm"
)

func TestRead_Schema(t *testing.T) {
	tool := NewReadTool("/tmp", nil)
	if tool.Name() != "read" {
		t.Errorf("Name = %q, want 'read'", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description should not be empty")
	}
	schema := tool.JSONSchema()
	if schema == nil {
		t.Fatal("JSONSchema is nil")
	}
	if !json.Valid(schema) {
		t.Error("JSONSchema is not valid JSON")
	}
}

func TestRead_InvalidArgs(t *testing.T) {
	tool := NewReadTool("/tmp", nil)
	_, err, _ := tool.Call(context.Background(), json.RawMessage(`invalid`))
	if err == nil {
		t.Fatal("expected error for invalid args")
	}
}

func TestRead_EmptyPath(t *testing.T) {
	tool := NewReadTool("/tmp", nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":""}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	if !strings.Contains(block.Text, "required") {
		t.Errorf("expected error mentioning 'required', got %q", block.Text)
	}
}

func TestRead_SuccessfulRead(t *testing.T) {
	dir := t.TempDir()
	// No trailing newline to avoid extra empty line in split
	content := "line1\nline2\nline3\nline4\nline5"
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadTool(dir, nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"test.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	// Default range is 1-100, file has 5 lines, so no metadata prefix expected
	expected := "line1\nline2\nline3\nline4\nline5"
	if block.Text != expected {
		t.Errorf("got %q, want %q", block.Text, expected)
	}
}

func TestRead_WithLineRange(t *testing.T) {
	dir := t.TempDir()
	// No trailing newline to avoid extra line from split
	content := "line1\nline2\nline3\nline4\nline5"
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadTool(dir, nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"test.txt","start_line":2,"end_line":3}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	expected := "[file: test.txt, lines 2-3 of 5]\nline2\nline3"
	if block.Text != expected {
		t.Errorf("got %q, want %q", block.Text, expected)
	}
}

func TestRead_TruncatedWithMetadata(t *testing.T) {
	dir := t.TempDir()
	// Create a file with more lines than the default limit (100)
	var lines []string
	for i := 1; i <= 150; i++ {
		lines = append(lines, fmt.Sprintf("line%d", i))
	}
	content := strings.Join(lines, "\n") + "\n"
	fp := filepath.Join(dir, "long.txt")
	if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadTool(dir, nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"long.txt","start_line":1,"end_line":100}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	// Should have metadata prefix (151 total due to trailing \n producing empty last line)
	if !strings.HasPrefix(block.Text, "[file: long.txt, lines 1-100 of 151]") {
		t.Errorf("expected metadata prefix, got %q", block.Text[:60])
	}
	// Should contain first line
	if !strings.Contains(block.Text, "line1") {
		t.Errorf("expected content to contain 'line1', got %q", block.Text)
	}
	// Should contain last requested line
	if !strings.Contains(block.Text, "line100") {
		t.Errorf("expected content to contain 'line100', got %q", block.Text)
	}
	// Should NOT contain line 101
	if strings.Contains(block.Text, "line101") {
		t.Error("content should not contain 'line101'")
	}
}

func TestRead_PathTraversalRejection(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadTool(dir, nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"../../../etc/passwd"}`))
	if err == nil {
		t.Fatal("expected ErrNeedsConfirmation, got nil error")
	}
	var needsConf *ErrNeedsConfirmation
	if !errors.As(err, &needsConf) {
		t.Fatalf("expected *ErrNeedsConfirmation, got %T: %v", err, err)
	}
	if needsConf.Path != "../../../etc/passwd" {
		t.Errorf("Path = %q, want %q", needsConf.Path, "../../../etc/passwd")
	}
	if !strings.Contains(needsConf.Message, "outside") && !strings.Contains(needsConf.Message, "escapes") {
		t.Errorf("expected error about path rejection, got %q", needsConf.Message)
	}
	if blocks != nil {
		t.Errorf("expected nil blocks for confirmation, got %v", blocks)
	}
	if isError {
		t.Error("isError = true, want false for confirmation")
	}
}

func TestRead_AbsolutePathOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadTool(dir, nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"/etc/passwd"}`))
	if err == nil {
		t.Fatal("expected ErrNeedsConfirmation, got nil error")
	}
	var needsConf *ErrNeedsConfirmation
	if !errors.As(err, &needsConf) {
		t.Fatalf("expected *ErrNeedsConfirmation, got %T: %v", err, err)
	}
	if needsConf.Path != "/etc/passwd" {
		t.Errorf("Path = %q, want %q", needsConf.Path, "/etc/passwd")
	}
	if blocks != nil {
		t.Errorf("expected nil blocks for confirmation, got %v", blocks)
	}
	if isError {
		t.Error("isError = true, want false for confirmation")
	}
}

func TestRead_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadTool(dir, nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"nonexistent.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	if !strings.Contains(block.Text, "Error") {
		t.Errorf("expected error message, got %q", block.Text)
	}
}

func TestRead_SkillDirsAllowed(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills", "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "skill content\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadTool(dir, []string{skillDir})
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"skills/my-skill/SKILL.md"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	if !strings.Contains(block.Text, "skill content") {
		t.Errorf("expected 'skill content', got %q", block.Text)
	}
}

func TestRead_ArgsUnmarshal(t *testing.T) {
	args := json.RawMessage(`{"path":"foo.go","start_line":5,"end_line":10}`)
	var parsed readArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Path != "foo.go" {
		t.Errorf("Path = %q, want 'foo.go'", parsed.Path)
	}
	if parsed.StartLine != 5 {
		t.Errorf("StartLine = %d, want 5", parsed.StartLine)
	}
	if parsed.EndLine != 10 {
		t.Errorf("EndLine = %d, want 10", parsed.EndLine)
	}
}

func TestRead_AllowedPathsAcceptsExternalPath(t *testing.T) {
	dir := t.TempDir()
	extDir := t.TempDir()
	content := "external content"
	extFile := filepath.Join(extDir, "ref.txt")
	if err := os.WriteFile(extFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadTool(dir, nil, []string{extDir})
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"`+extFile+`"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	if !strings.Contains(block.Text, "external content") {
		t.Errorf("expected 'external content', got %q", block.Text)
	}
}

func TestRead_AllowedPathsRejectsPathNotInAllowed(t *testing.T) {
	dir := t.TempDir()
	extDir1 := t.TempDir()
	extDir2 := t.TempDir()
	content := "secret"
	extFile := filepath.Join(extDir2, "secret.txt")
	if err := os.WriteFile(extFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadTool(dir, nil, []string{extDir1})
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"`+extFile+`"}`))
	if err == nil {
		t.Fatal("expected ErrNeedsConfirmation, got nil error")
	}
	var needsConf *ErrNeedsConfirmation
	if !errors.As(err, &needsConf) {
		t.Fatalf("expected *ErrNeedsConfirmation, got %T: %v", err, err)
	}
	if blocks != nil {
		t.Errorf("expected nil blocks for confirmation, got %v", blocks)
	}
	if isError {
		t.Error("isError = true, want false for confirmation")
	}
}

func TestRead_NilAllowedPathsBehavesLikeWorkspaceOnly(t *testing.T) {
	dir := t.TempDir()
	content := "workspace file"
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadTool(dir, nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"test.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	block, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	if !strings.Contains(block.Text, "workspace file") {
		t.Errorf("expected 'workspace file', got %q", block.Text)
	}
}

func TestRead_DefaultLineRange(t *testing.T) {
	dir := t.TempDir()
	// 100 lines without trailing newline = exactly 100 lines in split
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, fmt.Sprintf("line%d", i))
	}
	content := strings.Join(lines, "\n")
	fp := filepath.Join(dir, "hundred.txt")
	if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadTool(dir, nil)
	blocks, err, isError := tool.Call(context.Background(), json.RawMessage(`{"path":"hundred.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	block, ok := blocks[0].(litellm.TextBlock)
	if !ok {
		t.Fatalf("block is %T, want TextBlock", blocks[0])
	}
	// File has exactly 100 lines, default end_line=100, so no truncation
	if !strings.Contains(block.Text, "line100") {
		t.Errorf("expected 'line100' in output, got %q", block.Text)
	}
	// Should not have metadata prefix (not truncated)
	if strings.HasPrefix(block.Text, "[file:") {
		t.Errorf("expected no metadata prefix for full file read, got %q", block.Text[:40])
	}
}

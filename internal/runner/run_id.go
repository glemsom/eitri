// run_id.go — unified run/session job ID generation and validation for UI,
// batch, and sub-agent runs. Problem: batch session IDs came from a dedicated
// path reading the EITRI_BATCH_SESSION_ID env override with its own default
// (batch-<unixnano>) and validation rules, while sub-agent task IDs came from
// an internal counter and UI sessions from the session manager. That parallel
// logic drifted. Since ADR-0025, one runJobID helper generates and
// path-safety-validates run job IDs for all three run kinds (issue #1108).
//
// A run job ID names the on-disk review trail under ~/.eitri/sessions/<id>/,
// so it must never contain a path separator or ".." — otherwise it could
// escape the sessions directory. Generated IDs always satisfy this; the
// shared validator also guards pre-existing IDs (e.g. UI session IDs passed in
// from the session manager) at the run seam.

package runner

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// runJobRole discriminates the run kinds served by the unified ID helper.
type runJobRole string

const (
	// runJobRoleBatch is a headless batch run. IDs use the batch-<hex> shape,
	// preserving the batch session ID vocabulary.
	runJobRoleBatch runJobRole = "batch"
	// runJobRoleSubagent is a delegated sub-agent run. IDs use the task_<hex>
	// shape, preserving the sub-agent task ID vocabulary.
	runJobRoleSubagent runJobRole = "task"
)

// runJobID is the single generator for run/session job IDs for batch and
// sub-agent runs. It returns an auto-generated, unique, path-safety-safe ID
// (never empty, no path separators, no "..") for the given role, keeping each
// role's vocabulary prefix. UI session IDs are created by the session manager
// and are guarded path-safety-wise by validateRunJobID at the run seam rather
// than regenerated here.
func runJobID(role runJobRole) string {
	hex := randomHex()
	switch role {
	case runJobRoleBatch:
		return "batch-" + hex
	case runJobRoleSubagent:
		return "task_" + hex
	default:
		panic(fmt.Sprintf("runJobID: unknown role %q", role))
	}
}

// randomHex returns 16 random bytes as a lowercase hex string via crypto/rand.
// The non-crypto fallback is avoided by construction; a read failure is fatal,
// matching the session manager's ID generation (the only difference is the
// shape, not the source).
func randomHex() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate random run ID: %v", err))
	}
	return fmt.Sprintf("%x", b)
}

// validateRunJobID rejects run/session job IDs that could escape the sessions
// directory or collide with directory traversal. An empty ID is rejected too.
// It is the shared validator for whatever ID a run uses to write its review
// trail (issue #1108).
func validateRunJobID(id string) error {
	if id == "" {
		return fmt.Errorf("run job ID must not be empty")
	}
	if strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("run job ID %q is invalid: must not contain path separators", id)
	}
	if strings.Contains(id, "..") {
		return fmt.Errorf("run job ID %q is invalid: must not contain \"..\"", id)
	}
	return nil
}

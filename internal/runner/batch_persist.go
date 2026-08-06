// Batch-mode session ID resolution and title (issues #1038, #1039, #1107).
// Batch runs persist the same on-disk trail as UI sessions under
// ~/.eitri/sessions/<id>/ via the unified run-completer (run_completer.go):
// session.json snapshots, per-call HTTP traces, and a per-run timeline, so
// the same jq/cat inspection and on-demand load/report paths work for headless
// runs.

package runner

import (
	"fmt"
	"os"
	"strings"
	"time"

	uisession "github.com/glemsom/eitri/internal/session"
)

// batchSessionIDEnv overrides the session ID (and thus the
// ~/.eitri/sessions/<id>/ directory) of a headless batch run, letting
// callers like the agent loop name sessions meaningfully.
const batchSessionIDEnv = "EITRI_BATCH_SESSION_ID"

// batchSessionID returns the session ID for a batch run: the value of
// EITRI_BATCH_SESSION_ID when set, otherwise batch-<unixnano>. The env
// override is validated (non-empty, no path separators, no "..") so it can
// never escape the sessions directory.
func batchSessionID() (string, error) {
	if id, ok := os.LookupEnv(batchSessionIDEnv); ok {
		if err := validateBatchSessionID(id); err != nil {
			return "", err
		}
		return id, nil
	}
	return fmt.Sprintf("batch-%d", time.Now().UnixNano()), nil
}

// validateBatchSessionID rejects session IDs that could escape the sessions
// directory or collide with directory traversal. An explicitly empty value is
// rejected too — the caller falls back to the default only when the variable
// is unset, not when it is set to nothing.
func validateBatchSessionID(id string) error {
	if id == "" {
		return fmt.Errorf("%s must not be empty", batchSessionIDEnv)
	}
	if strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("%s %q is invalid: must not contain path separators", batchSessionIDEnv, id)
	}
	if strings.Contains(id, "..") {
		return fmt.Errorf("%s %q is invalid: must not contain \"..\"", batchSessionIDEnv, id)
	}
	return nil
}

// batchTitle derives the batch session title from the prompt using the same
// rule as UI session titles (session.TitlePreview). Blank prompts (e.g. a
// whitespace-only or empty `-b` argument) fall back to the session ID so
// reports and listings never show a blank title.
func batchTitle(prompt, fallback string) string {
	if title := uisession.TitlePreview(prompt); title != "" {
		return title
	}
	return fallback
}

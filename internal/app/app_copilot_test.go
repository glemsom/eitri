package app

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/provider"
)

// TestRunBatchCopilotNoCredentialErrorsReauth drives the batch path with a
// Copilot provider that has no stored token and no refresh path: the run must
// fail cleanly with a re-auth-in-TUI message and never attempt the interactive
// device flow (T11 acceptance criterion (b) at the app/engine seam).
func TestRunBatchCopilotNoCredentialErrorsReauth(t *testing.T) {
	cp := provider.NewCopilot(config.CopilotConfig{}, "https://unused.invalid/chat/completions",
		http.DefaultClient, nil, nil)
	var out bytes.Buffer
	dir := t.TempDir()

	err := Run(Options{
		DataDir:  filepath.Join(dir, ".eitri"),
		LookPath: okLookPath,
		Prompt:   "Change nothing",
		Stdout:   &out,
		Provider: cp,
	})
	if err == nil {
		t.Fatalf("Run(batch copilot, no credential) = nil error, want reauth error")
	}
	if !strings.Contains(err.Error(), "TUI") || !strings.Contains(err.Error(), "re-authenticate") {
		t.Fatalf("batch error %q does not direct the user to re-auth in the TUI", err)
	}
}

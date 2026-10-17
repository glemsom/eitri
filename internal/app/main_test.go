package app

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv(ProviderKeyEnv) == "" {
		_ = os.Setenv(ProviderKeyEnv, "test-key")
	}
	// Tests that exercise config resolution point at their own temp path; an
	// EITRI_CONFIG inherited from the shell must never redirect them.
	_ = os.Unsetenv(ConfigEnv)
	os.Exit(m.Run())
}

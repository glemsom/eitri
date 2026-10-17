package app

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Isolate the whole test run from the developer's ambient Eitri environment
	// so no test resolves the real config or data directory. Tests that need a
	// specific location set it with t.Setenv, which overrides this within the
	// test.
	_ = os.Unsetenv(ConfigEnv)
	_ = os.Unsetenv(DataDirEnv)
	if os.Getenv(ProviderKeyEnv) == "" {
		_ = os.Setenv(ProviderKeyEnv, "test-key")
	}
	// Tests that exercise config resolution point at their own temp path; an
	// EITRI_CONFIG inherited from the shell must never redirect them.
	_ = os.Unsetenv(ConfigEnv)
	os.Exit(m.Run())
}

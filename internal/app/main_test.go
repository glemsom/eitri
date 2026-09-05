package app

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv(ProviderKeyEnv) == "" {
		_ = os.Setenv(ProviderKeyEnv, "test-key")
	}
	os.Exit(m.Run())
}

package main

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("OPENCODE_API_KEY") == "" {
		_ = os.Setenv("OPENCODE_API_KEY", "test-key")
	}
	os.Exit(m.Run())
}

package assets

import "testing"

func TestCacheBustVersion(t *testing.T) {
	if CacheBustVersion == "" {
		t.Fatal("CacheBustVersion must not be empty")
	}
	if len(CacheBustVersion) != 16 {
		t.Errorf("CacheBustVersion = %q, want 16 hex chars", CacheBustVersion)
	}
	for _, c := range CacheBustVersion {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("CacheBustVersion = %q contains non-hex char %q", CacheBustVersion, c)
		}
	}
	if got := computeCacheBustVersion(); got != CacheBustVersion {
		t.Errorf("computeCacheBustVersion() = %q, want stable %q", got, CacheBustVersion)
	}
}

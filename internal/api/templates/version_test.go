package templates

import (
	"strings"
	"testing"
)

// ─── staticAsset / assetVersion ────────────────────────────────────────

func TestStaticAssetVersionsURL(t *testing.T) {
	got := staticAsset("/static/eitri.css")
	if !strings.HasPrefix(got, "/static/eitri.css?v=") {
		t.Errorf("staticAsset(\"/static/eitri.css\") = %q, want ?v= cache-bust suffix", got)
	}
	if got == "/static/eitri.css" {
		t.Error("staticAsset must append a version query string")
	}
}

func TestStaticAssetVersionValueIsAssetFingerprint(t *testing.T) {
	version := assetVersion()
	if version == "" {
		t.Errorf("assetVersion() = %q, want the embedded-asset content fingerprint", version)
	}
	// Every static URL must share the same version value within one build.
	if got := staticAsset("/static/eitri-stream.js"); !strings.HasSuffix(got, "?v="+version) {
		t.Errorf("staticAsset returned %q, want suffix %q", got, "?v="+version)
	}
}

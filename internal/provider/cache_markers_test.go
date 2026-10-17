package provider

import (
	"encoding/json"
	"testing"
)

// cacheMarkerKeys are every cache key Eitri knows how to emit on any wire.
// A body is marker-free when none of them appear, at any depth.
var cacheMarkerKeys = []string{
	"prompt_cache_key",
	"prompt_cache_retention",
	"cache_control",
	"prompt_cache_options",
	"prompt_cache_breakpoint",
}

// assertNoCacheMarkers fails when body carries any cache marker key, except the
// keys named in supported, which the caller has already asserted the value of.
// A wire that supports none of them passes with no supported keys at all.
func assertNoCacheMarkers(t *testing.T, body string, supported ...string) {
	t.Helper()
	var parsed any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("request body is not JSON: %v: %s", err, body)
	}
	allowed := make(map[string]bool, len(supported))
	for _, key := range supported {
		allowed[key] = true
	}
	for _, key := range cacheMarkerKeys {
		if allowed[key] {
			continue
		}
		if jsonContainsKey(parsed, key) {
			t.Errorf("request body leaked cache marker %q: %s", key, body)
		}
	}
}

// jsonContainsKey reports whether key appears at any depth of a decoded JSON
// value, so a marker buried in a tool definition or a content block counts.
func jsonContainsKey(value any, key string) bool {
	switch value := value.(type) {
	case map[string]any:
		if _, ok := value[key]; ok {
			return true
		}
		for _, child := range value {
			if jsonContainsKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if jsonContainsKey(child, key) {
				return true
			}
		}
	}
	return false
}

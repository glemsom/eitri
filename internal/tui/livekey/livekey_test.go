package livekey

import "testing"

func TestLiveSessionKeyGetSet(t *testing.T) {
	l := NewLiveSessionKey("initial")
	if got := l.Get(); got != "initial" {
		t.Fatalf("Get() = %q, want %q", got, "initial")
	}
	l.Set("re-minted")
	if got := l.Get(); got != "re-minted" {
		t.Fatalf("Get() after Set = %q, want %q", got, "re-minted")
	}
}

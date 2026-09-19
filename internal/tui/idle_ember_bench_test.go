package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// BenchmarkIdleEmberRender measures the per-frame cost of the idle surface with
// the ember sweeping versus the settled brand. The ember advances the cached
// layout only on the empty-transcript welcome, so the mid-ember frame must stay
// close to the settled render rather than re-deriving the whole chrome; the
// bounded window (idleEmberWindow) is what keeps the wakeup cost finite.
//
// Run: go test ./internal/tui -run '^$' -bench BenchmarkIdleEmberRender -benchmem
func BenchmarkIdleEmberRender(b *testing.B) {
	modes := []struct {
		name  string
		ember bool
	}{
		{name: "settled"},
		{name: "ember"},
	}
	for _, mode := range modes {
		b.Run(mode.name, func(b *testing.B) {
			m := NewModelCfg(Dependencies{})
			nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			m = asBenchModel(b, nm)
			m.tx.armIdleEmber()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if mode.ember && !m.tx.advanceIdleEmber() {
					m.tx.armIdleEmber()
				}
				_ = view(m)
			}
		})
	}
}

func asBenchModel(b *testing.B, tm tea.Model) Model {
	b.Helper()
	m, ok := tm.(Model)
	if !ok {
		b.Fatalf("tea.Model is %T, want Model", tm)
	}
	return m
}

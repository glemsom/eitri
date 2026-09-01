package tea

import "testing"

func TestWithoutScrollOptim(t *testing.T) {
	p := NewProgram(nil, WithoutScrollOptim())
	if !p.disableScrollOptim {
		t.Fatal("WithoutScrollOptim did not disable hard scrolling")
	}
}

package pulse

import "time"

// Status is a read of the live pulse: whether a turn runs, the derived Phase,
// the elapsed clock, and the animation frames the renderers draw.
type Status struct {
	Busy           bool
	Phase          Phase
	Elapsed        time.Duration
	Pulse          int
	Frame          int
	ForgeFrame     int
	EmberFrame     int
	EmberRemaining int
}

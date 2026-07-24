package runner

import "sync"

// browserBroadcaster manages browser-level SSE subscribers.
// Each browserID maps to a set of subscriberID → channel.
type browserBroadcaster struct {
	mu        sync.Mutex
	subs      map[string]map[uint64]chan BrowserEvent
	nextID    uint64
}

func newBrowserBroadcaster() *browserBroadcaster {
	return &browserBroadcaster{
		subs: make(map[string]map[uint64]chan BrowserEvent),
	}
}

// Subscribe registers a browser-level SSE subscriber for the given browserID.
// Returns subscriber ID and receive-only channel.
func (bb *browserBroadcaster) Subscribe(browserID string) (uint64, <-chan BrowserEvent) {
	bb.mu.Lock()
	defer bb.mu.Unlock()

	if bb.subs[browserID] == nil {
		bb.subs[browserID] = make(map[uint64]chan BrowserEvent)
	}

	id := bb.nextID
	bb.nextID++

	ch := make(chan BrowserEvent, 64)
	bb.subs[browserID][id] = ch
	return id, ch
}

// Unsubscribe removes a browser-level SSE subscriber.
func (bb *browserBroadcaster) Unsubscribe(browserID string, id uint64) {
	bb.mu.Lock()
	defer bb.mu.Unlock()

	subs := bb.subs[browserID]
	if subs == nil {
		return
	}
	ch, ok := subs[id]
	if !ok {
		return
	}
	delete(subs, id)
	close(ch)
	if len(subs) == 0 {
		delete(bb.subs, browserID)
	}
}

// Broadcast sends an event to all browser-level SSE subscribers for the given browserID.
func (bb *browserBroadcaster) Broadcast(browserID string, evt BrowserEvent) {
	bb.mu.Lock()
	defer bb.mu.Unlock()

	subs := bb.subs[browserID]
	if subs == nil {
		return
	}

	for _, ch := range subs {
		select {
		case ch <- evt:
		default:
			// Subscriber too slow; drop event
		}
	}
}

// Count returns the number of browser-level subscribers for a browserID.
func (bb *browserBroadcaster) Count(browserID string) int {
	bb.mu.Lock()
	defer bb.mu.Unlock()
	subs := bb.subs[browserID]
	if subs == nil {
		return 0
	}
	return len(subs)
}

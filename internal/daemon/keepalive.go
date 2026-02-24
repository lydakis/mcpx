package daemon

import (
	"sync"
	"time"

	"github.com/lydakis/mcpx/internal/mcppool"
)

const defaultIdleTimeout = 60 * time.Second

// Keepalive manages per-server sliding window timers.
// When a server is not touched within the idle timeout, its connection is closed.
type Keepalive struct {
	pool        *mcppool.Pool
	mu          sync.Mutex
	timers      map[string]*time.Timer
	timerIDs    map[string]uint64
	nextTimerID uint64
	inFlight    map[string]int
	timeout     time.Duration
	closeServer func(server string)
	onAllIdle   func()
}

// NewKeepalive creates a new keepalive manager.
func NewKeepalive(pool *mcppool.Pool) *Keepalive {
	k := &Keepalive{
		pool:      pool,
		timers:    make(map[string]*time.Timer),
		timerIDs:  make(map[string]uint64),
		inFlight:  make(map[string]int),
		timeout:   defaultIdleTimeout,
		onAllIdle: nil,
	}
	if pool != nil {
		k.closeServer = pool.Close
	}
	return k
}

// SetOnAllIdle configures an optional callback fired once the final idle timer
// expires and there are no in-flight requests remaining.
func (k *Keepalive) SetOnAllIdle(fn func()) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.onAllIdle = fn
}

// Begin marks the beginning of an in-flight request for a server.
// Any existing idle timer is canceled so a long-running request is never evicted.
func (k *Keepalive) Begin(server string) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if t, ok := k.timers[server]; ok {
		t.Stop()
		delete(k.timers, server)
		delete(k.timerIDs, server)
	}

	k.inFlight[server]++
}

// End marks completion of an in-flight request for a server.
// The idle timer starts only after the final in-flight request completes.
func (k *Keepalive) End(server string) {
	k.mu.Lock()
	defer k.mu.Unlock()

	n := k.inFlight[server]
	if n > 1 {
		k.inFlight[server] = n - 1
		return
	}

	delete(k.inFlight, server)
	k.startTimerLocked(server)
}

// Touch resets the idle timer for a server when it has no in-flight requests.
func (k *Keepalive) Touch(server string) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.inFlight[server] > 0 {
		return
	}

	k.startTimerLocked(server)
}

func (k *Keepalive) startTimerLocked(server string) {
	if t, ok := k.timers[server]; ok {
		t.Stop()
		delete(k.timers, server)
		delete(k.timerIDs, server)
	}

	k.nextTimerID++
	timerID := k.nextTimerID
	timer := time.AfterFunc(k.timeout, func() {
		k.expire(server, timerID)
	})
	k.timers[server] = timer
	k.timerIDs[server] = timerID
}

func (k *Keepalive) expire(server string, timerID uint64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	currentID, ok := k.timerIDs[server]
	if !ok || currentID != timerID || k.inFlight[server] > 0 {
		return
	}

	delete(k.timers, server)
	delete(k.timerIDs, server)
	if k.closeServer != nil {
		k.closeServer(server)
	}
	if len(k.timers) == 0 && len(k.inFlight) == 0 && k.onAllIdle != nil {
		go k.onAllIdle()
	}
}

// Stop cancels all keepalive timers.
func (k *Keepalive) Stop() {
	k.mu.Lock()
	defer k.mu.Unlock()

	for _, t := range k.timers {
		t.Stop()
	}
	k.timers = make(map[string]*time.Timer)
	k.timerIDs = make(map[string]uint64)
	k.inFlight = make(map[string]int)
}

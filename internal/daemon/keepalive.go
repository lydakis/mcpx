package daemon

import (
	"sync"
	"time"

	"github.com/lydakis/mcpx/internal/mcppool"
)

const (
	defaultIdleTimeout = 60 * time.Second
	daemonIdleSentinel = "__mcpx_daemon_idle__"
)

// Keepalive manages per-server sliding window timers.
// When a server is not touched within the idle timeout, its connection is closed.
type Keepalive struct {
	pool         *mcppool.Pool
	mu           sync.Mutex
	timers       map[string]*time.Timer
	timerIDs     map[string]uint64
	closeLocks   map[string]*sync.Mutex
	stopSeq      uint64
	nextTimerID  uint64
	inFlight     map[string]int
	activeCloses int
	stopCond     *sync.Cond
	idleSignaled bool
	timeout      time.Duration
	closeServer  func(server string)
	onAllIdle    func()
}

// NewKeepalive creates a new keepalive manager.
func NewKeepalive(pool *mcppool.Pool) *Keepalive {
	k := &Keepalive{
		pool:       pool,
		timers:     make(map[string]*time.Timer),
		timerIDs:   make(map[string]uint64),
		closeLocks: make(map[string]*sync.Mutex),
		inFlight:   make(map[string]int),
		timeout:    defaultIdleTimeout,
		onAllIdle:  nil,
	}
	k.stopCond = sync.NewCond(&k.mu)
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
	closeLock := k.closeLockForServerLocked(server)
	k.mu.Unlock()
	// Serialize request starts with in-progress close operations for the same
	// server so a newly started request cannot race with an idle close.
	closeLock.Lock()
	defer closeLock.Unlock()

	k.mu.Lock()
	defer k.mu.Unlock()

	if t, ok := k.timers[server]; ok {
		t.Stop()
		delete(k.timers, server)
		delete(k.timerIDs, server)
	}

	k.inFlight[server]++
	k.idleSignaled = false
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

// TouchDaemon resets the daemon-level idle timer.
func (k *Keepalive) TouchDaemon() {
	k.Touch(daemonIdleSentinel)
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
	k.idleSignaled = false
}

func (k *Keepalive) closeLockForServerLocked(server string) *sync.Mutex {
	lock, ok := k.closeLocks[server]
	if ok {
		return lock
	}
	lock = &sync.Mutex{}
	k.closeLocks[server] = lock
	return lock
}

func (k *Keepalive) expire(server string, timerID uint64) {
	k.mu.Lock()
	currentID, ok := k.timerIDs[server]
	if !ok || currentID != timerID || k.inFlight[server] > 0 {
		k.mu.Unlock()
		return
	}

	delete(k.timers, server)
	delete(k.timerIDs, server)
	stopSeq := k.stopSeq
	closeLock := k.closeLockForServerLocked(server)
	k.mu.Unlock()

	closeLock.Lock()
	k.mu.Lock()
	if k.stopSeq != stopSeq {
		k.mu.Unlock()
		closeLock.Unlock()
		return
	}
	// Re-check that the server is still idle before closing. New traffic may
	// have started after the timer callback released k.mu above.
	if k.inFlight[server] > 0 {
		k.mu.Unlock()
		closeLock.Unlock()
		return
	}
	if _, rescheduled := k.timerIDs[server]; rescheduled {
		k.mu.Unlock()
		closeLock.Unlock()
		return
	}
	closeServer := k.closeServer
	if closeServer != nil {
		k.activeCloses++
	}
	k.mu.Unlock()

	if closeServer != nil {
		closeServer(server)
	}

	k.mu.Lock()
	if closeServer != nil {
		k.activeCloses--
		if k.activeCloses == 0 {
			k.stopCond.Broadcast()
		}
	}
	if k.stopSeq != stopSeq {
		k.mu.Unlock()
		closeLock.Unlock()
		return
	}
	shouldSignalIdle := len(k.timers) == 0 && len(k.inFlight) == 0 && k.onAllIdle != nil && !k.idleSignaled
	if shouldSignalIdle {
		k.idleSignaled = true
	}
	onAllIdle := k.onAllIdle
	k.mu.Unlock()
	closeLock.Unlock()

	if shouldSignalIdle {
		go onAllIdle()
	}
}

// Stop cancels all keepalive timers.
func (k *Keepalive) Stop() {
	k.mu.Lock()
	k.stopSeq++

	for _, t := range k.timers {
		t.Stop()
	}
	k.timers = make(map[string]*time.Timer)
	k.timerIDs = make(map[string]uint64)
	k.inFlight = make(map[string]int)
	k.idleSignaled = false
	for k.activeCloses > 0 {
		k.stopCond.Wait()
	}
	k.mu.Unlock()
}

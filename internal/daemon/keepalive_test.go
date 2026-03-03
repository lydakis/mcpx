package daemon

import (
	"github.com/lydakis/mcpx/internal/mcppool"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefaultIdleTimeoutIsSixtySeconds(t *testing.T) {
	if defaultIdleTimeout != 60*time.Second {
		t.Fatalf("defaultIdleTimeout = %s, want %s", defaultIdleTimeout, 60*time.Second)
	}
}

func TestNewKeepaliveUsesDefaultTimeout(t *testing.T) {
	ka := NewKeepalive(nil)
	if ka.timeout != defaultIdleTimeout {
		t.Fatalf("NewKeepalive timeout = %s, want %s", ka.timeout, defaultIdleTimeout)
	}
}

func TestKeepaliveBeginEndDefersIdleTimerUntilRequestCompletes(t *testing.T) {
	ka := NewKeepalive(&mcppool.Pool{})
	ka.timeout = 20 * time.Millisecond
	defer ka.Stop()

	ka.Begin("github")

	time.Sleep(40 * time.Millisecond)

	ka.mu.Lock()
	_, hasTimer := ka.timers["github"]
	inFlight := ka.inFlight["github"]
	ka.mu.Unlock()

	if hasTimer {
		t.Fatal("timer started while request is still in flight")
	}
	if inFlight != 1 {
		t.Fatalf("inFlight = %d, want 1", inFlight)
	}

	ka.End("github")

	ka.mu.Lock()
	_, hasTimer = ka.timers["github"]
	_, hasInFlight := ka.inFlight["github"]
	ka.mu.Unlock()

	if !hasTimer {
		t.Fatal("timer missing after request completed")
	}
	if hasInFlight {
		t.Fatal("inFlight entry not cleared after request completed")
	}
}

func TestKeepaliveWaitsForAllConcurrentRequestsBeforeStartingTimer(t *testing.T) {
	ka := NewKeepalive(&mcppool.Pool{})
	ka.timeout = 20 * time.Millisecond
	defer ka.Stop()

	ka.Begin("github")
	ka.Begin("github")
	ka.End("github")

	time.Sleep(30 * time.Millisecond)

	ka.mu.Lock()
	_, hasTimer := ka.timers["github"]
	inFlight := ka.inFlight["github"]
	ka.mu.Unlock()

	if hasTimer {
		t.Fatal("timer started before last in-flight request completed")
	}
	if inFlight != 1 {
		t.Fatalf("inFlight = %d, want 1", inFlight)
	}

	ka.End("github")

	ka.mu.Lock()
	_, hasTimer = ka.timers["github"]
	ka.mu.Unlock()

	if !hasTimer {
		t.Fatal("timer missing after final in-flight request completed")
	}
}

func TestKeepaliveExpireBlocksBeginWhileCloseCompletes(t *testing.T) {
	ka := NewKeepalive(nil)
	defer ka.Stop()

	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	closeStarted := make(chan struct{})
	allowClose := make(chan struct{})
	expireDone := make(chan struct{})

	ka.closeServer = func(server string) {
		if server != "github" {
			t.Fatalf("close called for %q, want github", server)
		}
		close(closeStarted)
		<-allowClose
	}

	ka.mu.Lock()
	ka.timers["github"] = timer
	ka.timerIDs["github"] = 1
	ka.mu.Unlock()

	go func() {
		ka.expire("github", 1)
		close(expireDone)
	}()

	select {
	case <-closeStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expire did not enter close path")
	}

	beginDone := make(chan struct{})
	go func() {
		ka.Begin("github")
		close(beginDone)
	}()

	select {
	case <-beginDone:
		t.Fatal("Begin completed while expire close was in progress")
	case <-time.After(50 * time.Millisecond):
	}

	close(allowClose)

	select {
	case <-expireDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expire did not finish after allowing close")
	}

	select {
	case <-beginDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Begin did not complete after expire close finished")
	}
}

func TestKeepaliveExpireDoesNotSignalIdleWhenStoppedDuringClose(t *testing.T) {
	ka := NewKeepalive(nil)

	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	closeStarted := make(chan struct{})
	allowClose := make(chan struct{})
	idle := make(chan struct{}, 1)

	ka.closeServer = func(server string) {
		if server != "github" {
			t.Fatalf("close called for %q, want github", server)
		}
		close(closeStarted)
		<-allowClose
	}
	ka.SetOnAllIdle(func() {
		idle <- struct{}{}
	})

	ka.mu.Lock()
	ka.timers["github"] = timer
	ka.timerIDs["github"] = 1
	ka.mu.Unlock()

	go ka.expire("github", 1)

	select {
	case <-closeStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expire did not enter close path")
	}

	stopDone := make(chan struct{})
	go func() {
		ka.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		t.Fatal("Stop returned before in-progress close completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(allowClose)

	select {
	case <-stopDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Stop did not complete after close finished")
	}

	select {
	case <-idle:
		t.Fatal("onAllIdle fired after keepalive stop")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestKeepaliveExpireSkipsCloseWhenStopSequenceChangesBeforeClose(t *testing.T) {
	ka := NewKeepalive(nil)
	defer ka.Stop()

	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	var closeCalls atomic.Int32
	expireDone := make(chan struct{})

	ka.closeServer = func(server string) {
		if server != "github" {
			t.Fatalf("close called for %q, want github", server)
		}
		closeCalls.Add(1)
	}

	ka.mu.Lock()
	ka.timers["github"] = timer
	ka.timerIDs["github"] = 1
	ka.mu.Unlock()

	// Hold the server close lock so expire reaches the handoff point and cannot
	// call close yet.
	ka.mu.Lock()
	closeLock := ka.closeLockForServerLocked("github")
	ka.mu.Unlock()
	closeLock.Lock()
	go func() {
		ka.expire("github", 1)
		close(expireDone)
	}()

	deadline := time.After(200 * time.Millisecond)
	for {
		ka.mu.Lock()
		_, scheduled := ka.timerIDs["github"]
		ka.mu.Unlock()
		if !scheduled {
			break
		}
		select {
		case <-deadline:
			closeLock.Unlock()
			t.Fatal("expire did not reach close handoff")
		default:
			time.Sleep(1 * time.Millisecond)
		}
	}

	ka.mu.Lock()
	ka.stopSeq++
	ka.mu.Unlock()
	closeLock.Unlock()

	select {
	case <-expireDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expire did not complete")
	}
	if got := closeCalls.Load(); got != 0 {
		t.Fatalf("close calls = %d, want 0", got)
	}
}

func TestKeepaliveExpireSkipsCloseWhenServerBecomesActiveBeforeClose(t *testing.T) {
	ka := NewKeepalive(nil)
	defer ka.Stop()

	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	var closeCalls atomic.Int32
	expireDone := make(chan struct{})

	ka.closeServer = func(server string) {
		if server != "github" {
			t.Fatalf("close called for %q, want github", server)
		}
		closeCalls.Add(1)
	}

	ka.mu.Lock()
	ka.timers["github"] = timer
	ka.timerIDs["github"] = 1
	ka.mu.Unlock()

	// Hold the server close lock so expire reaches the handoff point and cannot
	// call close yet.
	ka.mu.Lock()
	closeLock := ka.closeLockForServerLocked("github")
	ka.mu.Unlock()
	closeLock.Lock()
	go func() {
		ka.expire("github", 1)
		close(expireDone)
	}()

	deadline := time.After(200 * time.Millisecond)
	for {
		ka.mu.Lock()
		_, scheduled := ka.timerIDs["github"]
		ka.mu.Unlock()
		if !scheduled {
			break
		}
		select {
		case <-deadline:
			closeLock.Unlock()
			t.Fatal("expire did not reach close handoff")
		default:
			time.Sleep(1 * time.Millisecond)
		}
	}

	// Simulate new request traffic before expire is allowed to call close.
	ka.mu.Lock()
	ka.inFlight["github"] = 1
	ka.mu.Unlock()
	closeLock.Unlock()

	select {
	case <-expireDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expire did not complete")
	}
	if got := closeCalls.Load(); got != 0 {
		t.Fatalf("close calls = %d, want 0", got)
	}

	ka.mu.Lock()
	inFlight := ka.inFlight["github"]
	ka.mu.Unlock()
	if inFlight != 1 {
		t.Fatalf("inFlight = %d, want 1", inFlight)
	}
}

func TestKeepaliveClosesServerAfterIdleTimeout(t *testing.T) {
	ka := NewKeepalive(nil)
	ka.timeout = 20 * time.Millisecond
	defer ka.Stop()

	closed := make(chan string, 1)
	ka.closeServer = func(server string) {
		closed <- server
	}

	ka.Begin("github")
	ka.End("github")

	select {
	case srv := <-closed:
		if srv != "github" {
			t.Fatalf("closed server = %q, want %q", srv, "github")
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("server was not closed after idle timeout")
	}
}

func TestKeepaliveSignalsWhenAllServersBecomeIdle(t *testing.T) {
	ka := NewKeepalive(nil)
	ka.timeout = 20 * time.Millisecond
	defer ka.Stop()

	ka.closeServer = func(string) {}

	idle := make(chan struct{}, 1)
	ka.SetOnAllIdle(func() {
		idle <- struct{}{}
	})

	ka.Begin("github")
	ka.End("github")

	select {
	case <-idle:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("onAllIdle callback not fired after final timer expiry")
	}
}

func TestKeepaliveTouchDaemonStartsIdleTimer(t *testing.T) {
	ka := NewKeepalive(nil)
	defer ka.Stop()

	ka.TouchDaemon()

	ka.mu.Lock()
	_, hasTimer := ka.timers[daemonIdleSentinel]
	ka.mu.Unlock()
	if !hasTimer {
		t.Fatal("daemon idle timer not started")
	}
}

func TestKeepaliveTouchDaemonTriggersIdleSignalWithoutServerTraffic(t *testing.T) {
	ka := NewKeepalive(nil)
	ka.timeout = 20 * time.Millisecond
	defer ka.Stop()

	closed := make(chan string, 1)
	ka.closeServer = func(server string) {
		closed <- server
	}
	idle := make(chan struct{}, 1)
	ka.SetOnAllIdle(func() {
		idle <- struct{}{}
	})

	ka.TouchDaemon()

	select {
	case srv := <-closed:
		if srv != daemonIdleSentinel {
			t.Fatalf("closed server = %q, want %q", srv, daemonIdleSentinel)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("daemon idle sentinel was not closed after timeout")
	}

	select {
	case <-idle:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("onAllIdle callback not fired for daemon idle sentinel")
	}
}

func TestKeepaliveSignalsOnAllIdleOncePerIdleTransition(t *testing.T) {
	ka := NewKeepalive(nil)
	defer ka.Stop()

	timerA := time.NewTimer(time.Hour)
	defer timerA.Stop()
	timerB := time.NewTimer(time.Hour)
	defer timerB.Stop()

	var idleCalls atomic.Int32
	ka.SetOnAllIdle(func() {
		idleCalls.Add(1)
	})
	ka.closeServer = func(string) {}

	ka.mu.Lock()
	ka.timers["github"] = timerA
	ka.timerIDs["github"] = 1
	ka.timers["gitlab"] = timerB
	ka.timerIDs["gitlab"] = 2
	ka.mu.Unlock()

	// Hold each server's close lock so both expire callbacks clear timers before
	// either can run close+idle signaling, which used to duplicate callbacks.
	ka.mu.Lock()
	closeLockGithub := ka.closeLockForServerLocked("github")
	closeLockGitlab := ka.closeLockForServerLocked("gitlab")
	ka.mu.Unlock()
	closeLockGithub.Lock()
	closeLockGitlab.Lock()
	doneA := make(chan struct{})
	doneB := make(chan struct{})
	go func() {
		ka.expire("github", 1)
		close(doneA)
	}()
	go func() {
		ka.expire("gitlab", 2)
		close(doneB)
	}()

	deadline := time.After(200 * time.Millisecond)
	for {
		ka.mu.Lock()
		remaining := len(ka.timerIDs)
		ka.mu.Unlock()
		if remaining == 0 {
			break
		}
		select {
		case <-deadline:
			closeLockGithub.Unlock()
			closeLockGitlab.Unlock()
			t.Fatal("expire callbacks did not clear timers before close handoff")
		default:
			time.Sleep(1 * time.Millisecond)
		}
	}
	closeLockGithub.Unlock()
	closeLockGitlab.Unlock()

	select {
	case <-doneA:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("first expire callback did not complete")
	}
	select {
	case <-doneB:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("second expire callback did not complete")
	}

	time.Sleep(50 * time.Millisecond)
	if got := idleCalls.Load(); got != 1 {
		t.Fatalf("onAllIdle calls = %d, want 1", got)
	}

	ka.Touch("github")
	ka.mu.Lock()
	timerID := ka.timerIDs["github"]
	ka.mu.Unlock()
	ka.expire("github", timerID)

	time.Sleep(50 * time.Millisecond)
	if got := idleCalls.Load(); got != 2 {
		t.Fatalf("onAllIdle calls after re-arming idle = %d, want 2", got)
	}
}

func TestKeepaliveTouchResetsSlidingWindow(t *testing.T) {
	ka := NewKeepalive(nil)
	ka.timeout = 40 * time.Millisecond
	defer ka.Stop()

	var closes atomic.Int32
	closedAt := make(chan time.Time, 2)
	ka.closeServer = func(string) {
		closes.Add(1)
		closedAt <- time.Now()
	}

	ka.Begin("github")
	ka.End("github")

	time.Sleep(25 * time.Millisecond)
	touchAt := time.Now()
	ka.Touch("github")

	time.Sleep(25 * time.Millisecond)
	if got := closes.Load(); got != 0 {
		t.Fatalf("close count after touch window = %d, want 0", got)
	}

	select {
	case at := <-closedAt:
		if at.Sub(touchAt) < 30*time.Millisecond {
			t.Fatalf("idle close happened too soon after touch: %s", at.Sub(touchAt))
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("server did not close after extended sliding window")
	}
}

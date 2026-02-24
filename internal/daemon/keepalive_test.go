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

func TestKeepaliveExpireBlocksBeginUntilCloseCompletes(t *testing.T) {
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
	case <-time.After(30 * time.Millisecond):
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

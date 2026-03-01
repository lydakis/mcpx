package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
)

func TestDispatchShutdownReturnsAckAndSignalsProcess(t *testing.T) {
	signaled := make(chan struct{}, 1)
	deps := runtimeDefaultDeps()
	deps.signalShutdownProcess = func() {
		signaled <- struct{}{}
	}

	resp := dispatchWithDeps(context.Background(), &config.Config{}, nil, nil, &ipc.Request{Type: "shutdown"}, deps)
	if string(resp.Content) != "shutting down\n" {
		t.Fatalf("dispatch(shutdown) content = %q, want %q", resp.Content, "shutting down\n")
	}
	if resp.ExitCode != 0 {
		t.Fatalf("dispatch(shutdown) exit code = %d, want 0", resp.ExitCode)
	}

	select {
	case <-signaled:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("dispatch(shutdown) did not signal process")
	}
}

func TestDispatchPingReturnsOKWithoutSignal(t *testing.T) {
	signaled := make(chan struct{}, 1)
	deps := runtimeDefaultDeps()
	deps.signalShutdownProcess = func() {
		signaled <- struct{}{}
	}

	resp := dispatchWithDeps(context.Background(), &config.Config{}, nil, nil, &ipc.Request{Type: "ping"}, deps)
	if resp.ExitCode != ipc.ExitOK {
		t.Fatalf("dispatch(ping) exit code = %d, want %d", resp.ExitCode, ipc.ExitOK)
	}
	if len(resp.Content) != 0 {
		t.Fatalf("dispatch(ping) content = %q, want empty", resp.Content)
	}

	select {
	case <-signaled:
		t.Fatal("dispatch(ping) unexpectedly signaled shutdown")
	default:
	}
}

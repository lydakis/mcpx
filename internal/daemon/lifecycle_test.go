package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
)

func TestDispatchShutdownReturnsAckAndSignalsProcess(t *testing.T) {
	oldSignalShutdownFn := signalShutdownFn
	defer func() {
		signalShutdownFn = oldSignalShutdownFn
	}()

	signaled := make(chan struct{}, 1)
	signalShutdownFn = func() {
		signaled <- struct{}{}
	}

	resp := dispatch(context.Background(), &config.Config{}, nil, nil, &ipc.Request{Type: "shutdown"})
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

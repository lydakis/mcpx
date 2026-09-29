package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/paths"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sys/unix"
)

const spawnProcessHelperEnv = "GO_WANT_MCPX_SPAWN_PROCESS_HELPER"

type spawnProbeResult struct {
	PID   int   `json:"pid"`
	Calls int32 `json:"calls"`
}

func TestDaemonSurvivesCallerProcessGroupCleanup(t *testing.T) {
	// Keep Unix socket paths short on macOS and isolate all runtime/config state.
	root, err := os.MkdirTemp("/tmp", "mcpx-spawn-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	for _, name := range []string{"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		t.Setenv(name, root)
	}
	if err := paths.EnsureDir(paths.ConfigDir()); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`fallback_sources = []
[servers.probe]
command = %q
args = ["-test.run=^TestSpawnProcessHelper$"]
[servers.probe.env]
%s = "server"
`, os.Args[0], spawnProcessHelperEnv)
	if err := os.WriteFile(paths.ConfigFile(), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}

	// Agent puts each shell command in its own process group, waits for the
	// command to finish, then SIGKILLs that group to clean up descendants.
	caller := exec.Command(os.Args[0], "-test.run=^TestSpawnProcessHelper$")
	caller.Env = append(os.Environ(), spawnProcessHelperEnv+"=caller")
	caller.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	output, err := caller.CombinedOutput()
	if err != nil {
		t.Fatalf("caller failed: %v (%s)", err, output)
	}
	var daemonPID int
	var nonce string
	if _, err := fmt.Sscanf(string(output), "%d %s", &daemonPID, &nonce); err != nil {
		t.Fatalf("caller output = %q: %v", output, err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(daemonPID, syscall.SIGTERM)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(paths.SocketPath()); os.IsNotExist(err) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		_ = syscall.Kill(daemonPID, syscall.SIGKILL)
	})
	client := ipc.NewClient(paths.SocketPath(), nonce)
	callProbe := func() spawnProbeResult {
		t.Helper()
		response, err := client.Send(&ipc.Request{Type: "call_tool", Server: "probe", Tool: "probe", Args: json.RawMessage(`{}`)})
		if err != nil || response.ExitCode != ipc.ExitOK {
			t.Fatalf("probe response = %+v, error = %v", response, err)
		}
		var result spawnProbeResult
		if err := json.Unmarshal(response.Content, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := callProbe()
	if err := syscall.Kill(-caller.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		t.Fatalf("clean up caller group: %v", err)
	}

	// A session leader has its own process group and no caller terminal.
	if session, err := unix.Getsid(daemonPID); err != nil || session != daemonPID {
		t.Fatalf("daemon session = %d, error = %v; want %d", session, err, daemonPID)
	}
	if group, err := syscall.Getpgid(daemonPID); err != nil || group != daemonPID {
		t.Fatalf("daemon process group = %d, error = %v; want %d", group, err, daemonPID)
	}
	response, err := client.Send(&ipc.Request{Type: "ping"})
	if err != nil || response.ExitCode != ipc.ExitOK {
		t.Fatalf("daemon ping after cleanup = %+v, error = %v", response, err)
	}
	// Reconnecting must reuse the daemon, rather than start a cold replacement.
	if got, err := SpawnOrConnect(); err != nil || got != nonce {
		t.Fatalf("reconnect nonce = %q, error = %v; want %q", got, err, nonce)
	}
	if second := callProbe(); second.PID != first.PID || first.Calls != 1 || second.Calls != 2 {
		t.Fatalf("MCP server was not reused: first = %+v, second = %+v", first, second)
	}
}

func TestSpawnProcessHelper(t *testing.T) {
	switch os.Getenv(spawnProcessHelperEnv) {
	case "caller":
		var daemonCommand *exec.Cmd
		execCommandFn = func(string, ...string) *exec.Cmd {
			daemonCommand = exec.Command(os.Args[0], "-test.run=^TestSpawnProcessHelper$")
			daemonCommand.Env = append(os.Environ(), spawnProcessHelperEnv+"=daemon")
			return daemonCommand
		}
		nonce, err := SpawnOrConnect()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%d %s\n", daemonCommand.Process.Pid, nonce)
		os.Exit(0)
	case "daemon":
		if err := Run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	case "server":
		server := mcp.NewServer(&mcp.Implementation{Name: "spawn-probe", Version: "1"}, nil)
		var calls atomic.Int32
		mcp.AddTool(server, &mcp.Tool{Name: "probe"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, spawnProbeResult, error) {
			return nil, spawnProbeResult{PID: os.Getpid(), Calls: calls.Add(1)}, nil
		})
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/paths"
)

var (
	readNonceFn           = readNonce
	isListeningFn         = isListening
	validateDaemonNonceFn = validateDaemonNonce
	spawnDaemonFn         = spawnDaemon
	waitForDaemonFn       = waitForDaemon
	acquireSpawnLockFn    = acquireSpawnLock
	execCommandFn         = exec.Command
)

// SpawnOrConnect ensures a daemon is running and returns the nonce for IPC auth.
// If no daemon is listening, it spawns one and waits for it to be ready.
func SpawnOrConnect() (string, error) {
	if err := paths.EnsureDir(paths.RuntimeDir()); err != nil {
		return "", fmt.Errorf("creating runtime dir: %w", err)
	}

	releaseLock, err := acquireSpawnLockFn(paths.LockPath())
	if err != nil {
		return "", fmt.Errorf("acquiring daemon lock: %w", err)
	}
	defer releaseLock() //nolint:errcheck

	// Try connecting to existing daemon
	if nonce, err := readNonceFn(); err == nil {
		if isListeningFn() {
			if valid, err := validateDaemonNonceFn(nonce); err == nil && valid {
				return nonce, nil
			}

			// State may have changed between reads (daemon restart); retry once.
			if freshNonce, err := readNonceFn(); err == nil && freshNonce != nonce {
				if valid, err := validateDaemonNonceFn(freshNonce); err == nil && valid {
					return freshNonce, nil
				}
			}

			clearDaemonRuntimeState()
		}
	}

	// Spawn a new daemon
	if err := spawnDaemonFn(); err != nil {
		return "", err
	}

	// Wait for it to be ready
	return waitForDaemonFn()
}

func validateDaemonNonce(nonce string) (bool, error) {
	client := ipc.NewClient(paths.SocketPath(), nonce)
	resp, err := client.Send(&ipc.Request{Type: "list_servers"})
	if err != nil {
		return false, err
	}
	if strings.Contains(strings.ToLower(resp.Stderr), "nonce mismatch") {
		return false, nil
	}
	return true, nil
}

func clearDaemonRuntimeState() {
	_ = os.Remove(paths.SocketPath())
	_ = os.Remove(paths.StatePath())
}

func acquireSpawnLock(path string) (func() error, error) {
	lockFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		lockFile.Close()
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}

	return func() error {
		unlockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		closeErr := lockFile.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}

func spawnDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}

	cmd, cleanup, err := newDaemonCommand(exe)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawning daemon: %w", err)
	}

	// Detach: don't wait for the daemon process
	go cmd.Wait() //nolint: errcheck
	return nil
}

func newDaemonCommand(exe string) (*exec.Cmd, func(), error) {
	cmd := execCommandFn(exe, "__daemon")
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("opening %s: %w", os.DevNull, err)
	}

	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.SysProcAttr = nil // daemon inherits signals; caller detaches
	return cmd, func() {
		_ = devNull.Close()
	}, nil
}

func waitForDaemon() (string, error) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if nonce, err := readNonce(); err == nil {
			if isListening() {
				return nonce, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", fmt.Errorf("daemon did not start within timeout")
}

func isListening() bool {
	conn, err := net.DialTimeout("unix", paths.SocketPath(), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func readNonce() (string, error) {
	data, err := os.ReadFile(paths.StatePath())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readOrCreateNonce() (string, error) {
	nonce, err := generateNonce()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(paths.StatePath(), []byte(nonce+"\n"), 0600); err != nil {
		return "", fmt.Errorf("writing nonce: %w", err)
	}
	return nonce, nil
}

func generateNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

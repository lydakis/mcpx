package daemon

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/paths"
)

func TestSpawnOrConnectSerializesDaemonSpawn(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	restore := saveSpawnHooks()
	defer restore()

	var ready atomic.Bool
	var spawns atomic.Int32

	readNonceFn = func() (string, error) {
		if ready.Load() {
			return "nonce-1", nil
		}
		return "", errors.New("nonce missing")
	}
	isListeningFn = func() bool {
		return ready.Load()
	}
	validateDaemonNonceFn = func(nonce string) (bool, error) {
		return ready.Load() && nonce == "nonce-1", nil
	}
	spawnDaemonFn = func() error {
		spawns.Add(1)
		time.Sleep(20 * time.Millisecond)
		ready.Store(true)
		return nil
	}
	waitForDaemonFn = func() (string, error) {
		return "nonce-1", nil
	}

	const callers = 12
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := SpawnOrConnect()
			errs <- err
		}()
	}

	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("SpawnOrConnect() error = %v", err)
		}
	}

	if got := spawns.Load(); got != 1 {
		t.Fatalf("spawnDaemon called %d times, want 1", got)
	}
}

func saveSpawnHooks() func() {
	oldReadNonce := readNonceFn
	oldIsListening := isListeningFn
	oldSpawn := spawnDaemonFn
	oldWait := waitForDaemonFn
	oldLock := acquireSpawnLockFn
	oldValidate := validateDaemonNonceFn

	return func() {
		readNonceFn = oldReadNonce
		isListeningFn = oldIsListening
		spawnDaemonFn = oldSpawn
		waitForDaemonFn = oldWait
		acquireSpawnLockFn = oldLock
		validateDaemonNonceFn = oldValidate
	}
}

func TestNewDaemonCommandDetachesStandardStreams(t *testing.T) {
	cmd, cleanup, err := newDaemonCommand("/tmp/mcpx")
	if err != nil {
		t.Fatalf("newDaemonCommand() error = %v", err)
	}
	defer cleanup()

	if cmd.Stdin == nil {
		t.Fatal("cmd.Stdin = nil, want detached stream")
	}
	if cmd.Stdout == nil {
		t.Fatal("cmd.Stdout = nil, want detached stream")
	}
	if cmd.Stderr == nil {
		t.Fatal("cmd.Stderr = nil, want detached stream")
	}
	if len(cmd.Args) != 2 || cmd.Args[1] != "__daemon" {
		t.Fatalf("cmd.Args = %#v, want daemon argv", cmd.Args)
	}
}

func TestSpawnOrConnectRecoversFromStaleNonceState(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	restore := saveSpawnHooks()
	defer restore()

	if err := paths.EnsureDir(paths.RuntimeDir()); err != nil {
		t.Fatalf("EnsureDir(runtime): %v", err)
	}
	if err := os.WriteFile(paths.SocketPath(), []byte("stale"), 0600); err != nil {
		t.Fatalf("write stale socket marker: %v", err)
	}
	if err := os.WriteFile(paths.StatePath(), []byte("stale-nonce\n"), 0600); err != nil {
		t.Fatalf("write stale state: %v", err)
	}

	readNonceFn = func() (string, error) {
		return "stale-nonce", nil
	}
	isListeningFn = func() bool {
		return true
	}
	validateDaemonNonceFn = func(nonce string) (bool, error) {
		if nonce != "stale-nonce" {
			t.Fatalf("validate nonce = %q, want stale-nonce", nonce)
		}
		return false, nil
	}

	var spawned atomic.Bool
	spawnDaemonFn = func() error {
		spawned.Store(true)
		return nil
	}
	waitForDaemonFn = func() (string, error) {
		return "fresh-nonce", nil
	}

	nonce, err := SpawnOrConnect()
	if err != nil {
		t.Fatalf("SpawnOrConnect() error = %v", err)
	}
	if nonce != "fresh-nonce" {
		t.Fatalf("SpawnOrConnect() nonce = %q, want %q", nonce, "fresh-nonce")
	}
	if !spawned.Load() {
		t.Fatal("SpawnOrConnect() did not spawn daemon after stale nonce validation failed")
	}

	if _, err := os.Stat(paths.SocketPath()); !os.IsNotExist(err) {
		t.Fatalf("socket path not cleaned up before respawn, stat err = %v", err)
	}
	if _, err := os.Stat(paths.StatePath()); !os.IsNotExist(err) {
		t.Fatalf("state path not cleaned up before respawn, stat err = %v", err)
	}
}

func TestSpawnOrConnectUsesExistingDaemonWhenNonceValid(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	restore := saveSpawnHooks()
	defer restore()

	readNonceFn = func() (string, error) {
		return "nonce-existing", nil
	}
	isListeningFn = func() bool {
		return true
	}
	validateDaemonNonceFn = func(nonce string) (bool, error) {
		return nonce == "nonce-existing", nil
	}

	var spawned atomic.Bool
	spawnDaemonFn = func() error {
		spawned.Store(true)
		return nil
	}
	waitForDaemonFn = func() (string, error) {
		t.Fatal("waitForDaemon should not be called when existing daemon is valid")
		return "", nil
	}

	nonce, err := SpawnOrConnect()
	if err != nil {
		t.Fatalf("SpawnOrConnect() error = %v", err)
	}
	if nonce != "nonce-existing" {
		t.Fatalf("SpawnOrConnect() nonce = %q, want %q", nonce, "nonce-existing")
	}
	if spawned.Load() {
		t.Fatal("spawnDaemon called for already valid daemon")
	}
}

func TestSpawnOrConnectSpawnsDaemonWhenMissing(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	restore := saveSpawnHooks()
	defer restore()

	readNonceFn = func() (string, error) {
		return "", errors.New("missing nonce")
	}
	isListeningFn = func() bool {
		return false
	}
	validateDaemonNonceFn = func(string) (bool, error) {
		t.Fatal("validateDaemonNonce should not be called when nonce read fails")
		return false, nil
	}

	var spawned atomic.Bool
	spawnDaemonFn = func() error {
		spawned.Store(true)
		return nil
	}
	waitForDaemonFn = func() (string, error) {
		return "nonce-fresh", nil
	}

	nonce, err := SpawnOrConnect()
	if err != nil {
		t.Fatalf("SpawnOrConnect() error = %v", err)
	}
	if nonce != "nonce-fresh" {
		t.Fatalf("SpawnOrConnect() nonce = %q, want %q", nonce, "nonce-fresh")
	}
	if !spawned.Load() {
		t.Fatal("spawnDaemon was not called for missing daemon state")
	}
}

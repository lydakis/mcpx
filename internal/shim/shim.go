package shim

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const markerPrefix = "# mcpx-shim:server="

var (
	ErrInvalidServerName = errors.New("invalid shim server name")
	ErrPathOccupied      = errors.New("shim path already exists")
	ErrCommandCollision  = errors.New("command already exists in PATH")
	ErrNotInstalled      = errors.New("shim is not installed")
	ErrNotManagedShim    = errors.New("shim path is not managed by mcpx")
)

var serverNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type InstallOptions struct {
	Dir string
}

type InstallResult struct {
	Dir              string
	Path             string
	Server           string
	AlreadyInstalled bool
	DirInPath        bool
}

type RemoveOptions struct {
	Dir string
}

type RemoveResult struct {
	Dir    string
	Path   string
	Server string
}

type ListOptions struct {
	Dir string
}

type Entry struct {
	Dir    string
	Path   string
	Server string
}

// DefaultDir returns the default install location for shims.
func DefaultDir() string {
	if v := strings.TrimSpace(os.Getenv("XDG_BIN_HOME")); v != "" {
		return v
	}
	return filepath.Join(homeDir(), ".local", "bin")
}

func Install(server string, opts InstallOptions) (*InstallResult, error) {
	server = strings.TrimSpace(server)
	if !serverNameRe.MatchString(server) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidServerName, server)
	}

	dir, err := normalizeDir(opts.Dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, server)

	if cmdPath, err := exec.LookPath(server); err == nil {
		if !samePath(cmdPath, path) {
			return nil, fmt.Errorf("%w: %s resolves to %s", ErrCommandCollision, server, cmdPath)
		}
	}

	if info, err := os.Lstat(path); err == nil {
		managedServer, managed, readErr := readManagedServer(path, info)
		if readErr != nil {
			return nil, fmt.Errorf("reading existing shim: %w", readErr)
		}
		if managed && managedServer == server {
			return &InstallResult{
				Dir:              dir,
				Path:             path,
				Server:           server,
				AlreadyInstalled: true,
				DirInPath:        dirInPath(dir),
			}, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrPathOccupied, path)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("checking existing shim: %w", err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating shim directory: %w", err)
	}

	script := renderShimScript(server)
	tmp, err := os.CreateTemp(dir, ".mcpx-shim-*")
	if err != nil {
		return nil, fmt.Errorf("creating shim temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.WriteString(tmp, script); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("writing shim: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("setting shim mode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("closing shim temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return nil, fmt.Errorf("installing shim: %w", err)
	}
	cleanupTmp = false

	return &InstallResult{
		Dir:              dir,
		Path:             path,
		Server:           server,
		AlreadyInstalled: false,
		DirInPath:        dirInPath(dir),
	}, nil
}

func Remove(server string, opts RemoveOptions) (*RemoveResult, error) {
	server = strings.TrimSpace(server)
	if !serverNameRe.MatchString(server) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidServerName, server)
	}

	dir, err := normalizeDir(opts.Dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, server)

	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotInstalled, server)
		}
		return nil, fmt.Errorf("checking shim: %w", err)
	}

	managedServer, managed, err := readManagedServer(path, info)
	if err != nil {
		return nil, fmt.Errorf("reading shim: %w", err)
	}
	if !managed || managedServer != server {
		return nil, fmt.Errorf("%w: %s", ErrNotManagedShim, path)
	}

	if err := os.Remove(path); err != nil {
		return nil, fmt.Errorf("removing shim: %w", err)
	}

	return &RemoveResult{Dir: dir, Path: path, Server: server}, nil
}

func List(opts ListOptions) ([]Entry, error) {
	dir, err := normalizeDir(opts.Dir)
	if err != nil {
		return nil, err
	}

	items, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading shim directory: %w", err)
	}

	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		path := filepath.Join(dir, item.Name())
		info, err := item.Info()
		if err != nil {
			return nil, fmt.Errorf("reading shim entry info: %w", err)
		}
		server, managed, err := readManagedServer(path, info)
		if err != nil {
			return nil, fmt.Errorf("reading shim entry: %w", err)
		}
		if !managed {
			continue
		}
		entries = append(entries, Entry{Dir: dir, Path: path, Server: server})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Server < entries[j].Server
	})
	return entries, nil
}

func normalizeDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = DefaultDir()
	}
	abs, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil {
		return "", fmt.Errorf("resolving shim dir: %w", err)
	}
	return abs, nil
}

func readManagedServer(path string, info os.FileInfo) (string, bool, error) {
	if !info.Mode().IsRegular() {
		return "", false, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for i := 0; i < 4 && scanner.Scan(); i++ {
		line := scanner.Text()
		if strings.HasPrefix(line, markerPrefix) {
			server := strings.TrimSpace(strings.TrimPrefix(line, markerPrefix))
			if server == "" {
				return "", false, nil
			}
			return server, true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return "", false, nil
		}
		return "", false, err
	}
	return "", false, nil
}

func renderShimScript(server string) string {
	return fmt.Sprintf("#!/bin/sh\n%s%s\nexec mcpx '%s' \"$@\"\n", markerPrefix, server, shellSingleQuote(server))
}

func shellSingleQuote(value string) string {
	return strings.ReplaceAll(value, "'", `'"'"'`)
}

func dirInPath(dir string) bool {
	pathEnv := os.Getenv("PATH")
	for _, item := range filepath.SplitList(pathEnv) {
		if item == "" {
			continue
		}
		if samePath(item, dir) {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	cleanA := filepath.Clean(a)
	cleanB := filepath.Clean(b)
	if cleanA == cleanB {
		return true
	}

	if resolved, err := filepath.EvalSymlinks(cleanA); err == nil {
		cleanA = filepath.Clean(resolved)
	}
	if resolved, err := filepath.EvalSymlinks(cleanB); err == nil {
		cleanB = filepath.Clean(resolved)
	}

	return cleanA == cleanB
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

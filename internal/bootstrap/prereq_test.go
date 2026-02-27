package bootstrap

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
)

func TestCheckPrerequisitesMissingRuntime(t *testing.T) {
	lookup := func(bin string) (string, error) {
		if bin == "npx" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + bin, nil
	}

	err := checkPrerequisitesWithLookup(config.ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-github"},
	}, lookup)
	if err == nil {
		t.Fatal("checkPrerequisitesWithLookup() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), `required runtime "npx"`) {
		t.Fatalf("checkPrerequisitesWithLookup() error = %q, want to contain %q", err.Error(), `required runtime "npx"`)
	}
}

func TestCheckPrerequisitesEnvWrapperChecksUnderlyingRuntime(t *testing.T) {
	lookup := func(bin string) (string, error) {
		if bin == "/usr/bin/env" {
			return bin, nil
		}
		if bin == "uvx" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + bin, nil
	}

	err := checkPrerequisitesWithLookup(config.ServerConfig{
		Command: "/usr/bin/env",
		Args:    []string{"UV_CACHE_DIR=/tmp/uv", "uvx", "mcp-server"},
	}, lookup)
	if err == nil {
		t.Fatal("checkPrerequisitesWithLookup() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), `required runtime "uvx"`) {
		t.Fatalf("checkPrerequisitesWithLookup() error = %q, want to contain %q", err.Error(), `required runtime "uvx"`)
	}
}

func TestCheckPrerequisitesEnvSplitStringChecksUnderlyingRuntime(t *testing.T) {
	lookup := func(bin string) (string, error) {
		if bin == "/usr/bin/env" {
			return bin, nil
		}
		if bin == "npx" {
			return "", errors.New("not found")
		}
		return "", fmt.Errorf("unexpected lookup for %q", bin)
	}

	err := checkPrerequisitesWithLookup(config.ServerConfig{
		Command: "/usr/bin/env",
		Args:    []string{"-S", "npx -y @modelcontextprotocol/server-github"},
	}, lookup)
	if err == nil {
		t.Fatal("checkPrerequisitesWithLookup() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), `required runtime "npx"`) {
		t.Fatalf("checkPrerequisitesWithLookup() error = %q, want to contain %q", err.Error(), `required runtime "npx"`)
	}
}

func TestCheckPrerequisitesEnvUnsetOptionSkipsValue(t *testing.T) {
	lookup := func(bin string) (string, error) {
		if bin == "/usr/bin/env" {
			return bin, nil
		}
		if bin == "uvx" {
			return "", errors.New("not found")
		}
		return "", fmt.Errorf("unexpected lookup for %q", bin)
	}

	err := checkPrerequisitesWithLookup(config.ServerConfig{
		Command: "/usr/bin/env",
		Args:    []string{"-u", "PYTHONPATH", "uvx", "mcp-server"},
	}, lookup)
	if err == nil {
		t.Fatal("checkPrerequisitesWithLookup() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), `required runtime "uvx"`) {
		t.Fatalf("checkPrerequisitesWithLookup() error = %q, want to contain %q", err.Error(), `required runtime "uvx"`)
	}
}

func TestCheckPrerequisitesEnvChdirOptionSkipsValue(t *testing.T) {
	lookup := func(bin string) (string, error) {
		if bin == "/usr/bin/env" {
			return bin, nil
		}
		if bin == "uvx" {
			return "", errors.New("not found")
		}
		return "", fmt.Errorf("unexpected lookup for %q", bin)
	}

	err := checkPrerequisitesWithLookup(config.ServerConfig{
		Command: "/usr/bin/env",
		Args:    []string{"--chdir", "/tmp", "uvx", "mcp-server"},
	}, lookup)
	if err == nil {
		t.Fatal("checkPrerequisitesWithLookup() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), `required runtime "uvx"`) {
		t.Fatalf("checkPrerequisitesWithLookup() error = %q, want to contain %q", err.Error(), `required runtime "uvx"`)
	}
}

func TestCheckPrerequisitesEnvDoubleDashSkipsAssignments(t *testing.T) {
	lookup := func(bin string) (string, error) {
		if bin == "/usr/bin/env" {
			return bin, nil
		}
		if bin == "uvx" {
			return "", errors.New("not found")
		}
		return "", fmt.Errorf("unexpected lookup for %q", bin)
	}

	err := checkPrerequisitesWithLookup(config.ServerConfig{
		Command: "/usr/bin/env",
		Args:    []string{"--", "UV_CACHE_DIR=/tmp/uv", "uvx", "mcp-server"},
	}, lookup)
	if err == nil {
		t.Fatal("checkPrerequisitesWithLookup() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), `required runtime "uvx"`) {
		t.Fatalf("checkPrerequisitesWithLookup() error = %q, want to contain %q", err.Error(), `required runtime "uvx"`)
	}
}

func TestCheckPrerequisitesSkipsHTTPServers(t *testing.T) {
	err := checkPrerequisitesWithLookup(config.ServerConfig{
		URL: "https://example.com/mcp",
	}, func(string) (string, error) {
		return "", errors.New("should not be called")
	})
	if err != nil {
		t.Fatalf("checkPrerequisitesWithLookup() error = %v, want nil", err)
	}
}

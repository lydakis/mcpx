package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/ipc"
)

func TestServerSkillNameKeepsStableServerNames(t *testing.T) {
	got := serverSkillName("github")
	if got != "mcpx-github" {
		t.Fatalf("serverSkillName(github) = %q, want %q", got, "mcpx-github")
	}

	got = serverSkillName("github-enterprise_2")
	if got != "mcpx-github-enterprise_2" {
		t.Fatalf("serverSkillName(github-enterprise_2) = %q, want %q", got, "mcpx-github-enterprise_2")
	}
}

func TestServerSkillNameAvoidsCollisionsForDistinctServerIDs(t *testing.T) {
	lower := serverSkillName("github")
	upper := serverSkillName("GitHub")
	punct := serverSkillName("github!")

	if upper == lower {
		t.Fatalf("serverSkillName(GitHub) = %q, must differ from %q", upper, lower)
	}
	if punct == lower {
		t.Fatalf("serverSkillName(github!) = %q, must differ from %q", punct, lower)
	}
	if !strings.HasPrefix(upper, "mcpx-github-") {
		t.Fatalf("serverSkillName(GitHub) = %q, want prefix %q", upper, "mcpx-github-")
	}
	if !strings.HasPrefix(punct, "mcpx-github-") {
		t.Fatalf("serverSkillName(github!) = %q, want prefix %q", punct, "mcpx-github-")
	}
	if serverSkillName("GitHub") != upper {
		t.Fatalf("serverSkillName(GitHub) must be stable across calls")
	}
}

func TestServerSkillNameAddsHashForFullySanitizedInputs(t *testing.T) {
	got := serverSkillName("!!!")
	if got == "mcpx-server" {
		t.Fatalf("serverSkillName(!!!) = %q, want hashed suffix", got)
	}
	if !strings.HasPrefix(got, "mcpx-server-") {
		t.Fatalf("serverSkillName(!!!) = %q, want prefix %q", got, "mcpx-server-")
	}
}

func TestRenderServerSkillContentIncludesJSONAndCacheGuidance(t *testing.T) {
	content := renderServerSkillContent("github", []toolListEntry{
		{Name: "search-repositories", Description: "Search repositories"},
	})

	if !strings.Contains(content, `Prefer JSON payloads for nested or complex arguments`) {
		t.Fatalf("rendered skill missing JSON guidance: %q", content)
	}
	if !strings.Contains(content, `Use flags for simple one-off scalar arguments`) {
		t.Fatalf("rendered skill missing flags guidance: %q", content)
	}
	if !strings.Contains(content, "add `--cache=<ttl>` on the first call") {
		t.Fatalf("rendered skill missing cache guidance: %q", content)
	}
}

func TestListServerToolsForSkillReturnsConnectError(t *testing.T) {
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	spawnOrConnectFn = func() (string, error) {
		return "", errors.New("daemon offline")
	}

	_, err := listServerToolsForSkill("github")
	if err == nil {
		t.Fatal("listServerToolsForSkill() error = nil, want non-nil")
	}
	if got := err.Error(); !strings.Contains(got, "connecting to daemon") || !strings.Contains(got, "daemon offline") {
		t.Fatalf("listServerToolsForSkill() error = %q, want connect context", got)
	}
}

func TestListServerToolsForSkillSurfacesDaemonStderr(t *testing.T) {
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{
			sendFn: func(req *ipc.Request) (*ipc.Response, error) {
				if req.Type != "list_tools" {
					return nil, errors.New("unexpected request type")
				}
				if req.Server != "github" {
					return nil, errors.New("unexpected server")
				}
				if !req.Verbose {
					return nil, errors.New("expected verbose tool listing")
				}
				return &ipc.Response{
					ExitCode: ipc.ExitUsageErr,
					Stderr:   "unknown server: github",
				}, nil
			},
		}
	}

	_, err := listServerToolsForSkill("github")
	if err == nil {
		t.Fatal("listServerToolsForSkill() error = nil, want non-nil")
	}
	if got := err.Error(); got != "unknown server: github" {
		t.Fatalf("listServerToolsForSkill() error = %q, want %q", got, "unknown server: github")
	}
}

func TestListServerToolsForSkillReturnsExitCodeErrorWithoutStderr(t *testing.T) {
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{
			sendFn: func(req *ipc.Request) (*ipc.Response, error) {
				return &ipc.Response{
					ExitCode: ipc.ExitInternal,
				}, nil
			},
		}
	}

	_, err := listServerToolsForSkill("github")
	if err == nil {
		t.Fatal("listServerToolsForSkill() error = nil, want non-nil")
	}
	if got := err.Error(); got != "listing tools failed with exit 3" {
		t.Fatalf("listServerToolsForSkill() error = %q, want %q", got, "listing tools failed with exit 3")
	}
}

func TestListServerToolsForSkillReturnsDecodeError(t *testing.T) {
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{
			sendFn: func(req *ipc.Request) (*ipc.Response, error) {
				return &ipc.Response{
					ExitCode: ipc.ExitOK,
					Content:  []byte(`not-json`),
				}, nil
			},
		}
	}

	_, err := listServerToolsForSkill("github")
	if err == nil {
		t.Fatal("listServerToolsForSkill() error = nil, want non-nil")
	}
	if got := err.Error(); !strings.Contains(got, "decoding tool list payload") {
		t.Fatalf("listServerToolsForSkill() error = %q, want decode context", got)
	}
}

func TestListServerToolsForSkillSortsEntriesByName(t *testing.T) {
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{
			sendFn: func(req *ipc.Request) (*ipc.Response, error) {
				return &ipc.Response{
					ExitCode: ipc.ExitOK,
					Content: []byte(`[
						{"name":"zeta","description":"last"},
						{"name":"alpha","description":"first"}
					]`),
				}, nil
			},
		}
	}

	entries, err := listServerToolsForSkill("github")
	if err != nil {
		t.Fatalf("listServerToolsForSkill() error = %v, want nil", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].Name != "alpha" || entries[1].Name != "zeta" {
		t.Fatalf("entries order = [%q %q], want [alpha zeta]", entries[0].Name, entries[1].Name)
	}
}

func TestInstallServerSkillValidatesInputs(t *testing.T) {
	if _, err := installServerSkill("  ", &skillInstallArgs{}); err == nil {
		t.Fatal("installServerSkill(empty server) error = nil, want non-nil")
	}
	if _, err := installServerSkill("github", nil); err == nil {
		t.Fatal("installServerSkill(nil options) error = nil, want non-nil")
	}
}

func TestInstallServerSkillCreatesServerSpecificSkillFile(t *testing.T) {
	oldSpawn := spawnOrConnectFn
	oldClient := newDaemonClient
	defer func() {
		spawnOrConnectFn = oldSpawn
		newDaemonClient = oldClient
	}()

	spawnOrConnectFn = func() (string, error) { return "nonce", nil }
	newDaemonClient = func(_, _ string) daemonRequester {
		return stubDaemonClient{
			sendFn: func(req *ipc.Request) (*ipc.Response, error) {
				if req.Type != "list_tools" {
					return nil, errors.New("unexpected request type")
				}
				return &ipc.Response{
					ExitCode: ipc.ExitOK,
					Content: []byte(`[
						{"name":"zeta","description":"Z"},
						{"name":"alpha","description":"A"}
					]`),
				}, nil
			},
		}
	}

	tmp := t.TempDir()
	result, err := installServerSkill("github", &skillInstallArgs{
		dataAgentDir:   filepath.Join(tmp, "agents", "skills"),
		skipClaudeLink: true,
	})
	if err != nil {
		t.Fatalf("installServerSkill() error = %v, want nil", err)
	}
	if result == nil {
		t.Fatal("installServerSkill() result = nil, want non-nil")
	}
	if !strings.Contains(result.SkillFile, filepath.Join("mcpx-github", "SKILL.md")) {
		t.Fatalf("SkillFile = %q, want mcpx-github/SKILL.md suffix", result.SkillFile)
	}
	if result.ClaudeLink != "" {
		t.Fatalf("ClaudeLink = %q, want empty when skipClaudeLink=true", result.ClaudeLink)
	}

	content, readErr := os.ReadFile(result.SkillFile)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.SkillFile, readErr)
	}
	rendered := string(content)
	if !strings.Contains(rendered, "# github server skill") {
		t.Fatalf("skill content missing server heading: %q", rendered)
	}
	if !strings.Contains(rendered, "- `alpha`: A") || !strings.Contains(rendered, "- `zeta`: Z") {
		t.Fatalf("skill content missing tool list: %q", rendered)
	}
	if strings.Index(rendered, "- `alpha`: A") > strings.Index(rendered, "- `zeta`: Z") {
		t.Fatalf("skill tools are not sorted: %q", rendered)
	}
}

package cli

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/skill"
)

func installServerSkill(server string, parsed *skillInstallArgs) (*skill.InstallResult, error) {
	server = strings.TrimSpace(server)
	if server == "" {
		return nil, fmt.Errorf("missing server (usage: mcpx skill install-server <server>)")
	}
	if parsed == nil {
		return nil, fmt.Errorf("internal: missing skill install options")
	}

	tools, err := listServerToolsForSkill(server)
	if err != nil {
		return nil, err
	}

	content := []byte(renderServerSkillContent(server, tools))
	return skill.InstallSkill(serverSkillName(server), content, skill.InstallOptions{
		DataAgentDir:    parsed.dataAgentDir,
		ClaudeDir:       parsed.claudeDir,
		SkipClaudeLink:  parsed.skipClaudeLink,
		CodexDir:        parsed.codexDir,
		EnableCodexLink: parsed.enableCodexLink,
		KiroDir:         parsed.kiroDir,
		EnableKiroLink:  parsed.enableKiroLink,
	})
}

func listServerToolsForSkill(server string) ([]toolListEntry, error) {
	nonce, err := spawnOrConnectFn()
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon: %w", err)
	}

	client := newDaemonClient(ipc.SocketPath(), nonce)
	resp, err := client.Send(&ipc.Request{
		Type:    "list_tools",
		Server:  server,
		Verbose: true,
		CWD:     callerWorkingDirectory(),
	})
	if err != nil {
		return nil, fmt.Errorf("listing tools: %w", err)
	}
	if resp.ExitCode != ipc.ExitOK {
		if strings.TrimSpace(resp.Stderr) != "" {
			return nil, fmt.Errorf("%s", strings.TrimSpace(resp.Stderr))
		}
		return nil, fmt.Errorf("listing tools failed with exit %d", resp.ExitCode)
	}

	entries, err := decodeToolListPayload(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("decoding tool list payload: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

func renderServerSkillContent(server string, tools []toolListEntry) string {
	server = strings.TrimSpace(server)
	skillName := serverSkillName(server)

	var b strings.Builder
	fmt.Fprintf(&b, "---\n")
	fmt.Fprintf(&b, "name: %q\n", skillName)
	fmt.Fprintf(&b, "description: %q\n", fmt.Sprintf("Use this skill when interacting with the %s MCP server via mcpx.", server))
	fmt.Fprintf(&b, "---\n\n")
	fmt.Fprintf(&b, "# %s server skill\n\n", server)
	fmt.Fprintf(&b, "Use mcpx for this server namespace:\n\n")
	fmt.Fprintf(&b, "```sh\n")
	fmt.Fprintf(&b, "mcpx %s\n", server)
	fmt.Fprintf(&b, "mcpx %s <tool>\n", server)
	fmt.Fprintf(&b, "mcpx %s <tool> --help\n", server)
	fmt.Fprintf(&b, "```\n\n")
	fmt.Fprintf(&b, "## Tooling notes\n\n")
	fmt.Fprintf(&b, "- Prefer exact tool names from `mcpx %s` output.\n", server)
	fmt.Fprintf(&b, "- Use `--help` on a tool before first call when you need argument details.\n")
	fmt.Fprintf(&b, "- Use `--cache=<ttl>` for read-only repeated calls.\n\n")
	fmt.Fprintf(&b, "## Tools\n\n")
	if len(tools) == 0 {
		fmt.Fprintf(&b, "- No tools were discovered at generation time.\n")
	} else {
		for _, tool := range tools {
			name := strings.TrimSpace(tool.Name)
			if name == "" {
				continue
			}
			desc := oneLine(tool.Description)
			if desc == "" {
				desc = "No description."
			}
			fmt.Fprintf(&b, "- `%s`: %s\n", name, desc)
		}
	}
	return b.String()
}

func serverSkillName(server string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		return "mcpx-server"
	}

	var b strings.Builder
	prevSep := false
	for _, r := range strings.ToLower(server) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			prevSep = false
			continue
		}
		if b.Len() == 0 || prevSep {
			continue
		}
		b.WriteByte('-')
		prevSep = true
	}

	normalized := strings.Trim(b.String(), "-._")
	if normalized == "" {
		normalized = "server"
	}

	name := "mcpx-" + normalized
	if isStableServerSkillName(server) {
		return name
	}
	return name + "-" + shortServerSkillHash(server)
}

func oneLine(value string) string {
	if value == "" {
		return ""
	}
	return strings.Join(strings.Fields(value), " ")
}

func isStableServerSkillName(server string) bool {
	if server == "" {
		return false
	}
	for idx, r := range server {
		if r >= 'A' && r <= 'Z' {
			return false
		}
		isAlpha := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		isPunct := r == '.' || r == '_' || r == '-'
		if !(isAlpha || isDigit || isPunct) {
			return false
		}
		if idx == 0 && !(isAlpha || isDigit) {
			return false
		}
	}

	last := server[len(server)-1]
	if last == '.' || last == '_' || last == '-' {
		return false
	}
	return true
}

func shortServerSkillHash(server string) string {
	sum := sha1.Sum([]byte(server))
	return hex.EncodeToString(sum[:4])
}

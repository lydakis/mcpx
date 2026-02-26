package servercatalog

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/mcppool"
)

const CodexAppsServerName = "codex_apps"

type Route struct {
	Backend       string
	ConfigServer  string
	VirtualName   string
	VirtualPrefix string
}

func (r Route) IsVirtual() bool {
	return strings.TrimSpace(r.VirtualPrefix) != ""
}

type ListToolsFunc func(ctx context.Context, server string) ([]mcppool.ToolInfo, error)

type Catalog struct {
	cfg       *config.Config
	listTools ListToolsFunc
}

func New(cfg *config.Config, listTools ListToolsFunc) *Catalog {
	return &Catalog{
		cfg:       cfg,
		listTools: listTools,
	}
}

func (c *Catalog) ServerNames(ctx context.Context) ([]string, error) {
	if c == nil || c.cfg == nil {
		return nil, nil
	}

	names := make(map[string]struct{}, len(c.cfg.Servers))
	for name := range c.cfg.Servers {
		if name == CodexAppsServerName {
			continue
		}
		names[name] = struct{}{}
	}

	if c.hasCodexApps() {
		if c.listTools == nil {
			return nil, fmt.Errorf("codex apps discovery requires list tools callback")
		}
		tools, err := c.listTools(ctx, CodexAppsServerName)
		if err != nil {
			return nil, err
		}
		for name := range codexVirtualServerMap(tools) {
			if strings.TrimSpace(name) == "" {
				continue
			}
			if _, exists := c.cfg.Servers[name]; exists {
				continue
			}
			names[name] = struct{}{}
		}
	}

	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func (c *Catalog) Resolve(ctx context.Context, requested string) (Route, []mcppool.ToolInfo, bool, error) {
	if c == nil || c.cfg == nil {
		return Route{}, nil, false, nil
	}
	if requested != CodexAppsServerName {
		if _, ok := c.cfg.Servers[requested]; ok {
			return Route{
				Backend:      requested,
				ConfigServer: requested,
			}, nil, true, nil
		}
	}
	if !c.hasCodexApps() {
		return Route{}, nil, false, nil
	}
	if c.listTools == nil {
		return Route{}, nil, false, fmt.Errorf("codex apps discovery requires list tools callback")
	}

	tools, err := c.listTools(ctx, CodexAppsServerName)
	if err != nil {
		return Route{}, nil, false, err
	}
	prefix, ok := codexVirtualServerMap(tools)[requested]
	if !ok {
		return Route{}, nil, false, nil
	}

	return Route{
		Backend:       CodexAppsServerName,
		ConfigServer:  CodexAppsServerName,
		VirtualName:   requested,
		VirtualPrefix: prefix,
	}, tools, true, nil
}

func (c *Catalog) ResolveForTool(ctx context.Context, requested, tool string) (Route, bool, error) {
	if c == nil || c.cfg == nil {
		return Route{}, false, nil
	}
	if requested != CodexAppsServerName {
		if _, ok := c.cfg.Servers[requested]; ok {
			return Route{
				Backend:      requested,
				ConfigServer: requested,
			}, true, nil
		}
	}
	if !c.hasCodexApps() {
		return Route{}, false, nil
	}

	prefix, hasPrefix := connectorPrefixFromToolName(tool)
	if hasPrefix && prefix == requested && normalizeCodexVirtualServerName(prefix) == requested {
		return Route{
			Backend:       CodexAppsServerName,
			ConfigServer:  CodexAppsServerName,
			VirtualName:   requested,
			VirtualPrefix: prefix,
		}, true, nil
	}

	route, _, found, err := c.Resolve(ctx, requested)
	if err != nil {
		return Route{}, false, err
	}
	if !found || !route.IsVirtual() {
		return Route{}, false, nil
	}
	return route, true, nil
}

func (c *Catalog) FilterTools(route Route, tools []mcppool.ToolInfo) []mcppool.ToolInfo {
	if !route.IsVirtual() {
		out := make([]mcppool.ToolInfo, len(tools))
		copy(out, tools)
		return out
	}
	filtered := make([]mcppool.ToolInfo, 0, len(tools))
	for _, tool := range tools {
		if toolMatchesPrefix(tool.Name, route.VirtualPrefix) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func (c *Catalog) ToolInfo(route Route, tools []mcppool.ToolInfo, requested string) (*mcppool.ToolInfo, bool) {
	for i := range tools {
		name := tools[i].Name
		if name != requested {
			continue
		}
		if route.IsVirtual() && !toolMatchesPrefix(name, route.VirtualPrefix) {
			return nil, false
		}
		toolCopy := tools[i]
		return &toolCopy, true
	}
	return nil, false
}

func (c *Catalog) ToolBelongsToRoute(route Route, tool string) bool {
	if !route.IsVirtual() {
		return true
	}
	return toolMatchesPrefix(tool, route.VirtualPrefix)
}

func (c *Catalog) hasCodexApps() bool {
	if c == nil || c.cfg == nil {
		return false
	}
	_, ok := c.cfg.Servers[CodexAppsServerName]
	return ok
}

func codexVirtualServerMap(tools []mcppool.ToolInfo) map[string]string {
	prefixes := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		prefix, ok := connectorPrefixFromToolName(tool.Name)
		if !ok {
			continue
		}
		prefixes[prefix] = struct{}{}
	}

	sortedPrefixes := make([]string, 0, len(prefixes))
	for prefix := range prefixes {
		sortedPrefixes = append(sortedPrefixes, prefix)
	}
	sort.Strings(sortedPrefixes)

	out := make(map[string]string, len(sortedPrefixes))
	for _, prefix := range sortedPrefixes {
		base := normalizeCodexVirtualServerName(prefix)
		if base == "" {
			continue
		}
		name := base
		if existingPrefix, exists := out[name]; exists && existingPrefix != prefix {
			for i := 2; ; i++ {
				candidate := fmt.Sprintf("%s_%d", base, i)
				if _, inUse := out[candidate]; inUse {
					continue
				}
				name = candidate
				break
			}
		}
		out[name] = prefix
	}
	return out
}

func connectorPrefixFromToolName(toolName string) (string, bool) {
	sep := strings.Index(toolName, "_")
	if sep <= 0 {
		return "", false
	}
	prefix := strings.TrimSpace(toolName[:sep])
	if prefix == "" {
		return "", false
	}
	return prefix, true
}

func normalizeCodexVirtualServerName(prefix string) string {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return ""
	}

	var b strings.Builder
	prevUnderscore := false
	for _, r := range prefix {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if b.Len() == 0 || prevUnderscore {
			continue
		}
		b.WriteByte('_')
		prevUnderscore = true
	}
	return strings.Trim(b.String(), "_")
}

func toolMatchesPrefix(name, prefix string) bool {
	return strings.HasPrefix(name, prefix+"_")
}

package mcpimport

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/lydakis/mcpx/internal/config"
)

// Candidate is one server offered by an external MCP configuration source.
type Candidate struct {
	Name                   string
	Enabled                bool
	DisabledReason         string
	Transport              string
	AuthStatus             string
	OAuthScopes            []string
	OAuthClientMetadataURL string
	OAuthUnsupportedReason string
	Supported              bool
	UnsupportedReason      string
	Server                 config.ServerConfig
}

// Source describes one import adapter exposed by mcpx.
type Source struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type sourceAdapter struct {
	source         Source
	prepareContext func(string) (string, error)
	refreshContext func(string) (string, error)
	sameContext    func(string, string) bool
	list           func(context.Context, string) ([]Candidate, error)
}

var sourceAdapters = map[string]sourceAdapter{
	"codex": {
		source:         Source{Name: "codex", DisplayName: "Codex", Description: "Resolved Codex MCP catalog, including enabled plugins"},
		prepareContext: prepareCodexContext,
		refreshContext: refreshCodexContext,
		sameContext:    sameCodexContext,
		list:           ListCodex,
	},
	"cursor": {
		source: Source{Name: "cursor", DisplayName: "Cursor", Description: "Cursor project and user MCP configurations"},
		list:   listCursor,
	},
	"claude": {
		source: Source{Name: "claude", DisplayName: "Claude", Description: "Claude Desktop and Claude Code MCP configurations"},
		list:   listClaude,
	},
	"cline": {
		source: Source{Name: "cline", DisplayName: "Cline", Description: "Cline MCP configuration"},
		list:   listCline,
	},
	"kiro": {
		source: Source{Name: "kiro", DisplayName: "Kiro", Description: "Kiro project and user MCP configurations"},
		list:   listKiro,
	},
}

// RefreshContext renews execution details while preserving the identity of a
// previously saved source scope. File-backed adapters need no renewal.
func RefreshContext(source, savedContext string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(source))
	adapter, ok := sourceAdapters[name]
	if !ok {
		return "", fmt.Errorf("unsupported source %q; supported sources: %s", source, strings.Join(sourceNames(), ", "))
	}
	if adapter.refreshContext != nil {
		return adapter.refreshContext(savedContext)
	}
	return strings.TrimSpace(savedContext), nil
}

// Sources returns the registered import adapters in stable order.
func Sources() []Source {
	sources := make([]Source, 0, len(sourceAdapters))
	for _, adapter := range sourceAdapters {
		sources = append(sources, adapter.source)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	return sources
}

// HasSource reports whether an import adapter is registered.
func HasSource(source string) bool {
	_, ok := sourceAdapters[strings.ToLower(strings.TrimSpace(source))]
	return ok
}

// DisplayName returns the registered human-readable source name.
func DisplayName(source string) string {
	name := strings.ToLower(strings.TrimSpace(source))
	if adapter, ok := sourceAdapters[name]; ok {
		return adapter.source.DisplayName
	}
	return strings.TrimSpace(source)
}

// PrepareContext captures the opaque adapter context needed to preserve source
// scope across later refreshes.
func PrepareContext(source, cwd string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(source))
	adapter, ok := sourceAdapters[name]
	if !ok {
		return "", fmt.Errorf("unsupported source %q; supported sources: %s", source, strings.Join(sourceNames(), ", "))
	}
	if adapter.prepareContext != nil {
		return adapter.prepareContext(cwd)
	}
	return strings.TrimSpace(cwd), nil
}

// SameContext reports whether two opaque contexts identify the same source
// scope. Execution details may differ without changing source identity.
func SameContext(source, left, right string) bool {
	name := strings.ToLower(strings.TrimSpace(source))
	if adapter, ok := sourceAdapters[name]; ok && adapter.sameContext != nil {
		return adapter.sameContext(left, right)
	}
	return left == right
}

// List resolves candidates from one registered source. sourceContext is an
// opaque adapter input; file-backed clients currently interpret it as the
// workspace directory used to resolve project-scoped configuration.
func List(ctx context.Context, source, sourceContext string) ([]Candidate, error) {
	name := strings.ToLower(strings.TrimSpace(source))
	adapter, ok := sourceAdapters[name]
	if !ok {
		return nil, fmt.Errorf("unsupported source %q; supported sources: %s", source, strings.Join(sourceNames(), ", "))
	}
	return adapter.list(ctx, sourceContext)
}

func sourceNames() []string {
	sources := Sources()
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, source.Name)
	}
	return names
}

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
	"github.com/lydakis/mcpx/internal/mcpimport"
	"github.com/lydakis/mcpx/internal/paths"
)

const importSourceTimeout = 15 * time.Second

var listImportCandidatesFn = mcpimport.List

type importArgs struct {
	source    string
	names     []string
	all       bool
	refresh   bool
	overwrite bool
	oauth     bool
	json      bool
	help      bool
}

type importPreviewRow struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Transport string `json:"transport"`
	Detail    string `json:"detail,omitempty"`
}

type importMutationResult struct {
	Source   string   `json:"source"`
	Imported []string `json:"imported"`
	Skipped  int      `json:"skipped"`
	Config   string   `json:"config"`
}

func maybeHandleImportCommand(args []string, cfg *config.Config, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 || args[0] != "import" {
		return false, 0
	}
	if utilityCommandDeferredToServer(cfg, "import") {
		return false, 0
	}
	return true, runImportCommand(args[1:], stdout, stderr)
}

func runImportCommand(args []string, stdout, stderr io.Writer) int {
	parsed, err := parseImportArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: import: %v\n", err)
		printImportHelp(stderr)
		return ipc.ExitUsageErr
	}
	if parsed.help {
		printImportHelp(stdout)
		return ipc.ExitOK
	}
	if parsed.source == "" {
		return printImportSources(parsed.json, stdout, stderr)
	}
	parsed.source = strings.ToLower(parsed.source)
	if !mcpimport.HasSource(parsed.source) {
		fmt.Fprintf(stderr, "mcpx: import: unsupported source %q\n", parsed.source)
		printImportHelp(stderr)
		return ipc.ExitUsageErr
	}

	cfgPath := paths.ConfigFile()
	cfg, err := config.LoadForEditFrom(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "mcpx: import: loading config: %v\n", err)
		return ipc.ExitInternal
	}

	ctx, cancel := context.WithTimeout(context.Background(), importSourceTimeout)
	defer cancel()
	var sourceContext string
	var candidates []mcpimport.Candidate
	if !parsed.refresh {
		cwd := callerWorkingDirectory()
		if cwd == "" {
			fmt.Fprintln(stderr, "mcpx: import: determining source context: working directory unavailable")
			return ipc.ExitInternal
		}
		sourceContext, err = mcpimport.PrepareContext(parsed.source, cwd)
		if err != nil {
			fmt.Fprintf(stderr, "mcpx: import: %v\n", err)
			return ipc.ExitInternal
		}
		candidates, err = listImportCandidatesFn(ctx, parsed.source, sourceContext)
		if err != nil {
			fmt.Fprintf(stderr, "mcpx: import: %v\n", err)
			return ipc.ExitInternal
		}
	}

	if len(parsed.names) == 0 && !parsed.all && !parsed.refresh {
		return printImportPreview(parsed.source, sourceContext, candidates, cfg, parsed.json, stdout, stderr)
	}

	nextServers := make(map[string]config.ServerConfig, len(cfg.Servers)+len(candidates))
	for name, server := range cfg.Servers {
		nextServers[name] = server
	}
	candidateMap := make(map[string]mcpimport.Candidate, len(candidates))
	for _, candidate := range candidates {
		candidateMap[candidate.Name] = candidate
	}

	var imported []string
	skipped := 0
	if parsed.refresh {
		imported, err = refreshSourceImports(ctx, parsed.source, nextServers)
		if err != nil {
			fmt.Fprintf(stderr, "mcpx: import: %v\n", err)
			return ipc.ExitToolErr
		}
		if len(imported) == 0 {
			if parsed.json {
				return writeImportMutationJSON(stdout, importMutationResult{Source: parsed.source, Config: cfgPath})
			}
			fmt.Fprintf(stdout, "No %s imports to refresh.\n", sourceDisplayName(parsed.source))
			return ipc.ExitOK
		}
	} else if parsed.all {
		for _, candidate := range candidates {
			if !candidate.Enabled || !candidate.Supported || isReservedImportServerName(candidate.Name) || parsed.oauth && candidate.OAuthUnsupportedReason != "" {
				skipped++
				continue
			}
			if _, exists := nextServers[candidate.Name]; exists && !parsed.overwrite {
				skipped++
				continue
			}
			nextServers[candidate.Name] = importedServer(parsed.source, sourceContext, candidate, parsed.oauth)
			imported = append(imported, candidate.Name)
		}
	} else {
		for _, name := range parsed.names {
			candidate, ok := candidateMap[name]
			if !ok {
				fmt.Fprintf(stderr, "mcpx: import: %s server %q was not found\n", sourceDisplayName(parsed.source), name)
				return ipc.ExitUsageErr
			}
			if !candidate.Enabled {
				detail := candidate.DisabledReason
				if detail == "" {
					detail = "disabled in " + sourceDisplayName(parsed.source)
				}
				fmt.Fprintf(stderr, "mcpx: import: %s server %q is disabled: %s\n", sourceDisplayName(parsed.source), name, detail)
				return ipc.ExitUsageErr
			}
			if !candidate.Supported {
				fmt.Fprintf(stderr, "mcpx: import: %s server %q cannot be imported: %s\n", sourceDisplayName(parsed.source), name, candidate.UnsupportedReason)
				return ipc.ExitUsageErr
			}
			if parsed.oauth && candidate.OAuthUnsupportedReason != "" {
				fmt.Fprintf(stderr, "mcpx: import: %s server %q cannot enable mcpx OAuth: %s\n", sourceDisplayName(parsed.source), name, candidate.OAuthUnsupportedReason)
				return ipc.ExitUsageErr
			}
			if isReservedImportServerName(name) {
				fmt.Fprintf(stderr, "mcpx: import: %s server %q conflicts with an mcpx command\n", sourceDisplayName(parsed.source), name)
				return ipc.ExitUsageErr
			}
			if _, exists := nextServers[name]; exists && !parsed.overwrite {
				fmt.Fprintf(stderr, "mcpx: import: server %q already exists; rerun with --overwrite to replace it\n", name)
				return ipc.ExitUsageErr
			}
			nextServers[name] = importedServer(parsed.source, sourceContext, candidate, parsed.oauth)
			imported = append(imported, name)
		}
	}

	sort.Strings(imported)
	cfg.Servers = nextServers
	if err := config.ValidateForCurrentEnv(cfg); err != nil {
		fmt.Fprintf(stderr, "mcpx: import: invalid resulting config: %v\n", err)
		return ipc.ExitUsageErr
	}
	if err := config.SaveTo(cfgPath, cfg); err != nil {
		fmt.Fprintf(stderr, "mcpx: import: writing config: %v\n", err)
		return ipc.ExitInternal
	}

	result := importMutationResult{Source: parsed.source, Imported: imported, Skipped: skipped, Config: cfgPath}
	if parsed.json {
		return writeImportMutationJSON(stdout, result)
	}
	if parsed.refresh {
		fmt.Fprintf(stdout, "Refreshed %d %s server(s) in %s\n", len(imported), sourceDisplayName(parsed.source), cfgPath)
		return ipc.ExitOK
	}
	if parsed.all {
		fmt.Fprintf(stdout, "Imported %d %s server(s) into %s; skipped %d\n", len(imported), sourceDisplayName(parsed.source), cfgPath, skipped)
		return ipc.ExitOK
	}
	for _, name := range imported {
		fmt.Fprintf(stdout, "Imported %s server %q into %s\n", sourceDisplayName(parsed.source), name, cfgPath)
	}
	return ipc.ExitOK
}

func parseImportArgs(args []string) (*importArgs, error) {
	parsed := &importArgs{}
	seenNames := make(map[string]struct{})
	for _, arg := range args {
		switch {
		case arg == "--help" || arg == "-h":
			parsed.help = true
		case arg == "--all":
			parsed.all = true
		case arg == "--refresh":
			parsed.refresh = true
		case arg == "--overwrite":
			parsed.overwrite = true
		case arg == "--oauth":
			parsed.oauth = true
		case arg == "--json":
			parsed.json = true
		case strings.HasPrefix(arg, "-"):
			return nil, fmt.Errorf("unknown flag: %s", arg)
		case parsed.source == "":
			parsed.source = strings.TrimSpace(arg)
		default:
			name := strings.TrimSpace(arg)
			if name == "" {
				continue
			}
			if _, exists := seenNames[name]; exists {
				return nil, fmt.Errorf("duplicate server name %q", name)
			}
			seenNames[name] = struct{}{}
			parsed.names = append(parsed.names, name)
		}
	}
	if parsed.help {
		return parsed, nil
	}
	if parsed.source == "" {
		if parsed.all || parsed.refresh || parsed.overwrite || parsed.oauth || len(parsed.names) > 0 {
			return nil, fmt.Errorf("an import source is required with mutation flags")
		}
		return parsed, nil
	}
	if parsed.all && len(parsed.names) > 0 {
		return nil, fmt.Errorf("--all cannot be combined with server names")
	}
	if parsed.refresh && (parsed.all || len(parsed.names) > 0 || parsed.overwrite || parsed.oauth) {
		return nil, fmt.Errorf("--refresh cannot be combined with names, --all, --overwrite, or --oauth")
	}
	if parsed.overwrite && !parsed.all && len(parsed.names) == 0 {
		return nil, fmt.Errorf("--overwrite requires --all or one or more server names")
	}
	if parsed.oauth && !parsed.all && len(parsed.names) == 0 {
		return nil, fmt.Errorf("--oauth requires --all or one or more server names")
	}
	return parsed, nil
}

func printImportPreview(source, sourceContext string, candidates []mcpimport.Candidate, cfg *config.Config, asJSON bool, stdout, stderr io.Writer) int {
	rows := make([]importPreviewRow, 0, len(candidates))
	for _, candidate := range candidates {
		row := importPreviewRow{Name: candidate.Name, Transport: candidate.Transport}
		switch {
		case !candidate.Enabled:
			row.Status = "disabled"
			row.Detail = candidate.DisabledReason
		case !candidate.Supported:
			row.Status = "unsupported"
			row.Detail = candidate.UnsupportedReason
		case isReservedImportServerName(candidate.Name):
			row.Status = "unsupported"
			row.Detail = "name conflicts with an mcpx command"
		default:
			existing, exists := cfg.Servers[candidate.Name]
			switch {
			case !exists:
				row.Status = "available"
			case existing.ImportSource == source && existing.ImportName == candidate.Name && mcpimport.SameContext(source, existing.ImportContext, sourceContext):
				row.Status = "imported"
			default:
				row.Status = "conflict"
				row.Detail = "managed server already exists"
			}
		}
		if row.Detail == "" && candidate.OAuthUnsupportedReason != "" {
			row.Detail = candidate.OAuthUnsupportedReason
		}
		rows = append(rows, row)
	}
	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(rows); err != nil {
			fmt.Fprintf(stderr, "mcpx: import: encoding preview: %v\n", err)
			return ipc.ExitInternal
		}
		return ipc.ExitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tTRANSPORT\tDETAIL")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", row.Name, row.Status, row.Transport, row.Detail)
	}
	_ = tw.Flush()
	return ipc.ExitOK
}

func importedServer(source, sourceContext string, candidate mcpimport.Candidate, oauth bool) config.ServerConfig {
	server := candidate.Server
	server.ImportSource = source
	server.ImportName = candidate.Name
	server.ImportContext = sourceContext
	if server.IsHTTP() && oauth {
		for name := range server.Headers {
			if strings.EqualFold(strings.TrimSpace(name), "Authorization") {
				delete(server.Headers, name)
			}
		}
		server.OAuth = true
		server.OAuthScopes = append([]string(nil), candidate.OAuthScopes...)
		server.OAuthClientMetadataURL = candidate.OAuthClientMetadataURL
	}
	return server
}

func refreshSourceImports(ctx context.Context, source string, servers map[string]config.ServerConfig) ([]string, error) {
	var managedNames []string
	for name, server := range servers {
		if server.ImportSource == source {
			managedNames = append(managedNames, name)
		}
	}
	sort.Strings(managedNames)

	type resolvedImportContext struct {
		sourceContext string
		candidates    map[string]mcpimport.Candidate
	}
	candidatesByContext := make(map[string]resolvedImportContext)
	updates := make(map[string]config.ServerConfig, len(managedNames))
	for _, managedName := range managedNames {
		existing := servers[managedName]
		sourceName := strings.TrimSpace(existing.ImportName)
		sourceContext := strings.TrimSpace(existing.ImportContext)
		if sourceName == "" || sourceContext == "" {
			return nil, fmt.Errorf("managed server %q has incomplete %s import provenance", managedName, sourceDisplayName(source))
		}
		resolved, loaded := candidatesByContext[sourceContext]
		if !loaded {
			refreshedContext, err := mcpimport.RefreshContext(source, sourceContext)
			if err != nil {
				return nil, fmt.Errorf("refreshing %s import context for managed server %q: %w", sourceDisplayName(source), managedName, err)
			}
			listed, err := listImportCandidatesFn(ctx, source, refreshedContext)
			if err != nil {
				return nil, fmt.Errorf("refreshing %s import for managed server %q: %w", sourceDisplayName(source), managedName, err)
			}
			candidates := make(map[string]mcpimport.Candidate, len(listed))
			for _, candidate := range listed {
				candidates[candidate.Name] = candidate
			}
			resolved = resolvedImportContext{sourceContext: refreshedContext, candidates: candidates}
			candidatesByContext[sourceContext] = resolved
		}
		candidate, ok := resolved.candidates[sourceName]
		if !ok {
			return nil, fmt.Errorf("imported %s server %q no longer exists", sourceDisplayName(source), sourceName)
		}
		if !candidate.Enabled {
			return nil, fmt.Errorf("imported %s server %q is now disabled", sourceDisplayName(source), sourceName)
		}
		if !candidate.Supported {
			return nil, fmt.Errorf("imported %s server %q cannot be refreshed: %s", sourceDisplayName(source), sourceName, candidate.UnsupportedReason)
		}
		updated := importedServer(source, resolved.sourceContext, candidate, existing.OAuth)
		updated.ImportName = sourceName
		updated.DefaultCacheTTL = existing.DefaultCacheTTL
		updated.NoCacheTools = existing.NoCacheTools
		updated.Tools = existing.Tools
		if updated.IsHTTP() {
			updated.OAuthScopes = existing.OAuthScopes
			updated.OAuthClientMetadataURL = existing.OAuthClientMetadataURL
		}
		updates[managedName] = updated
	}
	for name, server := range updates {
		servers[name] = server
	}
	return managedNames, nil
}

func writeImportMutationJSON(out io.Writer, result importMutationResult) int {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		return ipc.ExitInternal
	}
	return ipc.ExitOK
}

func printImportSources(asJSON bool, stdout, stderr io.Writer) int {
	sources := mcpimport.Sources()
	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(sources); err != nil {
			fmt.Fprintf(stderr, "mcpx: import: encoding sources: %v\n", err)
			return ipc.ExitInternal
		}
		return ipc.ExitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SOURCE\tDESCRIPTION")
	for _, source := range sources {
		fmt.Fprintf(tw, "%s\t%s\n", source.Name, source.Description)
	}
	_ = tw.Flush()
	return ipc.ExitOK
}

func sourceDisplayName(source string) string {
	name := mcpimport.DisplayName(source)
	if name == "" {
		return "source"
	}
	return name
}

func isReservedImportServerName(name string) bool {
	switch name {
	case "add", "auth", "completion", "doctor", "import", "shim", "skill", "__complete":
		return true
	default:
		return false
	}
}

func printImportHelp(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  mcpx import [--json]")
	fmt.Fprintln(out, "  mcpx import <source> [--json]")
	fmt.Fprintln(out, "  mcpx import <source> <server>... [--oauth] [--overwrite] [--json]")
	fmt.Fprintln(out, "  mcpx import <source> --all [--oauth] [--overwrite] [--json]")
	fmt.Fprintln(out, "  mcpx import <source> --refresh [--json]")
	fmt.Fprintln(out, "")
	sources := mcpimport.Sources()
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, source.Name)
	}
	fmt.Fprintf(out, "Sources: %s\n", strings.Join(names, ", "))
	fmt.Fprintln(out, "Without a source, lists adapters. Without a mutation selection, prints a redacted preview.")
	fmt.Fprintln(out, "Codex keyring OAuth tokens are never exported; use --oauth and then `mcpx auth login <server>`.")
}

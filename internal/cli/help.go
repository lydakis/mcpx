package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

func parseToolHelpPayload(raw []byte) (name, description string, inputSchema map[string]any, outputSchema map[string]any) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", "", nil, nil
	}

	// New structured payload from daemon.
	if in, ok := payload["input_schema"].(map[string]any); ok {
		description, _ = payload["description"].(string)
		if rawName, ok := payload["name"].(string); ok {
			name = rawName
		}
		inputSchema = in
		if out, ok := payload["output_schema"].(map[string]any); ok {
			outputSchema = out
		}
		return name, description, inputSchema, outputSchema
	}

	// Legacy payload: raw input schema only.
	if _, ok := payload["properties"]; ok || payload["type"] != nil {
		return "", "", payload, nil
	}

	return "", "", nil, nil
}

func printToolHelp(w io.Writer, server, tool, description string, inputSchema, outputSchema map[string]any) {
	fmt.Fprintf(w, "Usage: mcpx %s %s [FLAGS]\n", server, tool)
	if description != "" {
		fmt.Fprintf(w, "\nDescription:\n  %s\n", description)
	}

	fmt.Fprintln(w, "\nOptions:")
	fmt.Fprintln(w, "  Tool flags:")
	printToolInputFlags(w, inputSchema)
	fmt.Fprintln(w, "\n  Global flags:")
	printGlobalFlags(w)
	fmt.Fprintln(w, "\n  Namespace:")
	fmt.Fprintln(w, "    Prefix colliding tool params with --tool- (for example: --tool-cache).")
	fmt.Fprintln(w, "    Use -- to force all following flags to tool parameters.")
	fmt.Fprintln(w, "\n  Type to flag forms:")
	printTypeToFlagForms(w)
	fmt.Fprintln(w, "\n  Conventions:")
	fmt.Fprintln(w, "    Flag conventions vary by server and tool. Check this help for each tool.")

	if outputSchema == nil {
		fmt.Fprintln(w, "\nOutput: not declared by server")
	} else {
		fmt.Fprintln(w, "\nOutput:")
		printSchemaProperties(w, outputSchema, false)
	}
	fmt.Fprintln(w, "\nOutput contract:")
	fmt.Fprintln(w, "  - structuredContent is emitted as JSON.")
	fmt.Fprintln(w, "  - text content blocks are emitted as text (single block as-is, multiple newline-joined).")
	fmt.Fprintln(w, "  - mcpx does not add wrappers around server content.")
	fmt.Fprintln(w, "\nExit code caveat:")
	fmt.Fprintln(w, "  Some servers encode domain errors in successful (exit 0) payloads; validate response fields when scripting.")

	fmt.Fprintln(w, "\nExamples:")
	for _, ex := range toolExamples(server, tool, inputSchema) {
		fmt.Fprintf(w, "  %s\n", ex)
	}
}

func printTypeToFlagForms(w io.Writer) {
	fmt.Fprintln(w, "    string/number/integer: --key=value")
	fmt.Fprintln(w, "    boolean: --flag / --no-flag")
	fmt.Fprintln(w, "    array: --item=a --item=b OR --items='[\"a\",\"b\"]'")
	fmt.Fprintln(w, "    object: --config='{\"k\":\"v\"}'")
}

func printToolInputFlags(w io.Writer, schema map[string]any) {
	lines := inputFlagLines(schema)
	if len(lines) == 0 {
		fmt.Fprintln(w, "    (none)")
		return
	}

	for _, line := range lines {
		baseFlag, negFlag := toolFlagNames(line.Path, line.Type)
		fmt.Fprintf(w, "    %s <%s>%s\n", baseFlag, line.Type, optionSemantics(line))
		if line.Description != "" {
			fmt.Fprintf(w, "      %s\n", line.Description)
		}
		if negFlag != "" {
			fmt.Fprintf(w, "    %s <%s>%s\n", negFlag, line.Type, optionSemantics(line))
		}
	}
}

func printGlobalFlags(w io.Writer) {
	fmt.Fprintln(w, "    --cache <duration>   Cache this tool response for a TTL (for example: 30s, 5m).")
	fmt.Fprintln(w, "    --no-cache           Disable cache for this call.")
	fmt.Fprintln(w, "    --verbose, -v        Print verbose diagnostics to stderr.")
	fmt.Fprintln(w, "    --quiet, -q          Suppress stderr output.")
	fmt.Fprintln(w, "    --json               With --help, emit raw schema JSON from mcpx.")
	fmt.Fprintln(w, "    --help, -h           Show this help output.")
}

func printSchemaProperties(w io.Writer, schema map[string]any, includeSemantics bool) {
	lines := schemaLines(schema)
	if len(lines) == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}

	for _, line := range lines {
		semantics := ""
		if includeSemantics {
			semantics = optionSemantics(line)
		}
		fmt.Fprintf(w, "  %s <%s>%s\n", line.Path, line.Type, semantics)
		if line.Description != "" {
			fmt.Fprintf(w, "    %s\n", line.Description)
		}
	}
}

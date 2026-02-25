package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lydakis/mcpx/internal/paths"
)

type schemaLine struct {
	Path        string
	Type        string
	Description string
	Required    bool
	HasDefault  bool
	Default     string
}

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

func schemaLines(schema map[string]any) []schemaLine {
	if schema == nil {
		return nil
	}

	typ := propType(schema)
	if typ == "array" {
		return rootArraySchemaLines(schema)
	}

	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}

	reqSet := make(map[string]bool)
	for _, r := range toStringSlice(schema["required"]) {
		reqSet[r] = true
	}

	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)

	lines := make([]schemaLine, 0, len(names))
	for _, name := range names {
		prop, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		collectSchemaLines(&lines, name, prop, reqSet[name], true)
	}
	return lines
}

func inputFlagLines(schema map[string]any) []schemaLine {
	if schema == nil {
		return nil
	}

	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}

	reqSet := make(map[string]bool)
	for _, r := range toStringSlice(schema["required"]) {
		reqSet[r] = true
	}

	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)

	lines := make([]schemaLine, 0, len(names))
	for _, name := range names {
		prop, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		desc, _ := prop["description"].(string)
		typ := propType(prop)
		def, hasDefault := formatDefaultValue(prop["default"], typ)

		lines = append(lines, schemaLine{
			Path:        name,
			Type:        typ,
			Description: desc,
			Required:    reqSet[name],
			HasDefault:  hasDefault,
			Default:     def,
		})
	}
	return lines
}

func rootArraySchemaLines(schema map[string]any) []schemaLine {
	desc, _ := schema["description"].(string)
	def, hasDefault := formatDefaultValue(schema["default"], "array")

	lines := []schemaLine{
		{
			Path:        "[]",
			Type:        "array",
			Description: desc,
			HasDefault:  hasDefault,
			Default:     def,
		},
	}

	items, _ := schema["items"].(map[string]any)
	if items == nil {
		return lines
	}

	if propType(items) == "object" {
		collectSchemaLines(&lines, "[]", items, false, false)
		return lines
	}

	collectSchemaLines(&lines, "[]", items, false, true)
	return lines
}

func collectSchemaLines(lines *[]schemaLine, path string, schema map[string]any, required bool, includeSelf bool) {
	typ := propType(schema)
	if includeSelf {
		desc, _ := schema["description"].(string)
		def, hasDefault := formatDefaultValue(schema["default"], typ)
		*lines = append(*lines, schemaLine{
			Path:        path,
			Type:        typ,
			Description: desc,
			Required:    required,
			HasDefault:  hasDefault,
			Default:     def,
		})
	}

	switch typ {
	case "object":
		props, _ := schema["properties"].(map[string]any)
		if len(props) == 0 {
			return
		}
		reqSet := make(map[string]bool)
		for _, r := range toStringSlice(schema["required"]) {
			reqSet[r] = true
		}
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child, ok := props[name].(map[string]any)
			if !ok {
				continue
			}
			collectSchemaLines(lines, path+"."+name, child, reqSet[name], true)
		}
	case "array":
		items, _ := schema["items"].(map[string]any)
		if items == nil {
			return
		}
		collectSchemaLines(lines, path+"[]", items, false, true)
	}
}

func writeManPage(server, tool, description string, inputSchema, outputSchema map[string]any) (string, error) {
	outPath := manPagePath(server, tool)
	if err := paths.EnsureDir(filepath.Dir(outPath)); err != nil {
		return "", err
	}

	content := renderManPage(server, tool, description, inputSchema, outputSchema)
	if err := os.WriteFile(outPath, []byte(content), 0644); err != nil {
		return "", err
	}
	return outPath, nil
}

func manPagePath(server, tool string) string {
	name := fmt.Sprintf("mcpx-%s-%s.1", server, tool)
	return filepath.Join(paths.ManDir(), name)
}

func renderManPage(server, tool, description string, inputSchema, outputSchema map[string]any) string {
	title := strings.ToUpper(fmt.Sprintf("MCPX-%s-%s", server, tool))

	var b strings.Builder
	fmt.Fprintf(&b, ".TH %s 1\n", roffEscape(title))
	fmt.Fprintln(&b, ".SH NAME")
	nameLine := fmt.Sprintf("mcpx %s %s", server, tool)
	if description != "" {
		fmt.Fprintf(&b, "%s \\- %s\n", roffEscape(nameLine), roffEscape(description))
	} else {
		fmt.Fprintf(&b, "%s\n", roffEscape(nameLine))
	}

	fmt.Fprintln(&b, ".SH SYNOPSIS")
	fmt.Fprintf(&b, "%s [FLAGS]\n", roffEscape(nameLine))

	fmt.Fprintln(&b, ".SH OPTIONS")
	writeRoffSchemaLines(&b, schemaLines(inputSchema), true)

	fmt.Fprintln(&b, ".SH OUTPUT")
	if outputSchema == nil {
		fmt.Fprintln(&b, "Output not declared by server.")
	} else {
		writeRoffSchemaLines(&b, schemaLines(outputSchema), false)
	}

	fmt.Fprintln(&b, ".SH EXAMPLES")
	writeRoffExamples(&b, toolExamples(server, tool, inputSchema))

	return b.String()
}

func writeRoffSchemaLines(w io.Writer, lines []schemaLine, includeSemantics bool) {
	if len(lines) == 0 {
		fmt.Fprintln(w, "None.")
		return
	}
	for _, line := range lines {
		semantics := ""
		if includeSemantics {
			semantics = optionSemantics(line)
		}
		fmt.Fprintf(w, ".TP\n\\fB%s\\fR <%s>%s\n", roffEscape(line.Path), roffEscape(line.Type), roffEscape(semantics))
		if line.Description != "" {
			fmt.Fprintf(w, "%s\n", roffEscape(line.Description))
		}
	}
}

func writeRoffExamples(w io.Writer, examples []string) {
	if len(examples) == 0 {
		fmt.Fprintln(w, "None.")
		return
	}

	fmt.Fprintln(w, ".nf")
	for _, ex := range examples {
		fmt.Fprintf(w, "%s\n", roffEscape(ex))
	}
	fmt.Fprintln(w, ".fi")
}

func roffEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "-", `\-`)
	return s
}

func propType(prop map[string]any) string {
	if t, ok := prop["type"].(string); ok && t != "" {
		return t
	}
	if enum, ok := prop["enum"].([]any); ok {
		vals := make([]string, len(enum))
		for i, v := range enum {
			vals[i] = fmt.Sprint(v)
		}
		return strings.Join(vals, "|")
	}
	return "any"
}

func toStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func optionSemantics(line schemaLine) string {
	parts := make([]string, 0, 3)
	if line.Required {
		parts = append(parts, "required")
	} else {
		parts = append(parts, "optional")
	}
	if line.Type == "array" {
		parts = append(parts, "repeatable")
	}
	if line.HasDefault {
		parts = append(parts, "default: "+line.Default)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func formatDefaultValue(value any, typ string) (string, bool) {
	if value == nil {
		return "", false
	}

	switch v := value.(type) {
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case float64:
		if typ == "integer" || math.Trunc(v) == v {
			return strconv.FormatInt(int64(v), 10), true
		}
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case json.Number:
		return v.String(), true
	case int:
		return strconv.Itoa(v), true
	case int64:
		return strconv.FormatInt(v, 10), true
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value), true
	}
	return string(raw), true
}

func toolExamples(server, tool string, inputSchema map[string]any) []string {
	command := fmt.Sprintf("mcpx %s %s", server, tool)
	args := sampleExampleArgs(inputSchema)

	flagTokens := sampleFlagTokens(inputSchema, args)
	flagExample := command
	if len(flagTokens) > 0 {
		flagExample += " " + strings.Join(flagTokens, " ")
	}

	rawJSON, err := json.Marshal(args)
	if err != nil || len(rawJSON) == 0 {
		rawJSON = []byte("{}")
	}
	jsonLiteral := string(rawJSON)

	return []string{
		flagExample,
		fmt.Sprintf("%s '%s'", command, jsonLiteral),
		fmt.Sprintf("echo '%s' | %s", jsonLiteral, command),
	}
}

func sampleExampleArgs(schema map[string]any) map[string]any {
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return map[string]any{}
	}

	requiredSet := make(map[string]bool)
	for _, name := range toStringSlice(schema["required"]) {
		requiredSet[name] = true
	}

	requiredNames := make([]string, 0, len(requiredSet))
	optionalNames := make([]string, 0, len(props))
	for name := range props {
		if requiredSet[name] {
			requiredNames = append(requiredNames, name)
		} else {
			optionalNames = append(optionalNames, name)
		}
	}
	sort.Strings(requiredNames)
	sort.Strings(optionalNames)

	selected := requiredNames
	if len(selected) == 0 && len(optionalNames) > 0 {
		selected = append(selected, optionalNames[0])
	}
	if len(selected) > 2 {
		selected = selected[:2]
	}

	args := make(map[string]any, len(selected))
	for _, name := range selected {
		prop, _ := props[name].(map[string]any)
		args[name] = sampleValue(prop)
	}
	return args
}

func sampleFlagTokens(schema map[string]any, args map[string]any) []string {
	if len(args) == 0 {
		return nil
	}

	props, _ := schema["properties"].(map[string]any)
	names := make([]string, 0, len(args))
	for name := range args {
		names = append(names, name)
	}
	sort.Strings(names)

	tokens := make([]string, 0, len(names))
	for _, name := range names {
		prop, _ := props[name].(map[string]any)
		value := args[name]
		typ, _ := prop["type"].(string)
		baseFlag, negFlag := toolFlagNames(name, typ)

		switch typ {
		case "boolean":
			if b, ok := value.(bool); ok && !b {
				if negFlag != "" {
					tokens = append(tokens, negFlag)
				} else {
					tokens = append(tokens, baseFlag)
				}
			} else {
				tokens = append(tokens, baseFlag)
			}
		case "array", "object":
			raw, err := json.Marshal(value)
			if err != nil {
				continue
			}
			tokens = append(tokens, fmt.Sprintf("%s='%s'", baseFlag, string(raw)))
		default:
			tokens = append(tokens, fmt.Sprintf("%s=%s", baseFlag, sampleLiteral(value)))
		}
	}
	return tokens
}

func sampleValue(schema map[string]any) any {
	if schema == nil {
		return "value"
	}

	if def, ok := schema["default"]; ok {
		return def
	}
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}

	typ, _ := schema["type"].(string)
	switch typ {
	case "string":
		return "example"
	case "integer":
		return 1
	case "number":
		return 1.5
	case "boolean":
		return true
	case "array":
		items, _ := schema["items"].(map[string]any)
		return []any{sampleValue(items)}
	case "object":
		props, _ := schema["properties"].(map[string]any)
		if len(props) == 0 {
			return map[string]any{}
		}
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		sort.Strings(names)
		first := names[0]
		child, _ := props[first].(map[string]any)
		return map[string]any{
			first: sampleValue(child),
		}
	default:
		return "value"
	}
}

func sampleLiteral(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		if math.Trunc(v) == v {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

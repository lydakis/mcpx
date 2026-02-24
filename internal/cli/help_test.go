package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPrintToolHelpIncludesOutputSchemaSection(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "search query",
			},
		},
		"required": []any{"query"},
	}
	output := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
			},
		},
	}

	var out bytes.Buffer
	printToolHelp(&out, "github", "search-repositories", "Search repos", input, output)
	got := out.String()

	if !bytes.Contains(out.Bytes(), []byte("Usage: mcpx github search-repositories [FLAGS]")) {
		t.Fatalf("help missing usage: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("Output:")) {
		t.Fatalf("help missing output section: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("items <array>")) {
		t.Fatalf("help missing output property: %q", got)
	}
}

func TestPrintToolHelpShowsUndeclaredOutput(t *testing.T) {
	input := map[string]any{
		"type": "object",
	}

	var out bytes.Buffer
	printToolHelp(&out, "github", "search-repositories", "", input, nil)
	got := out.String()
	if !bytes.Contains(out.Bytes(), []byte("Output: not declared by server")) {
		t.Fatalf("expected undeclared output message, got: %q", got)
	}
}

func TestParseToolHelpPayloadSupportsStructuredAndLegacy(t *testing.T) {
	structured := []byte(`{
		"name":"search_repositories",
		"description":"Search repos",
		"input_schema":{"type":"object","properties":{"query":{"type":"string"}}},
		"output_schema":{"type":"object","properties":{"items":{"type":"array"}}}
	}`)
	name, desc, in, out := parseToolHelpPayload(structured)
	if name != "search-repositories" || desc != "Search repos" || in == nil || out == nil {
		t.Fatalf("parse structured payload failed: name=%q desc=%q in=%v out=%v", name, desc, in, out)
	}

	legacy := []byte(`{"type":"object","properties":{"query":{"type":"string"}}}`)
	name, desc, in, out = parseToolHelpPayload(legacy)
	if name != "" || desc != "" || in == nil || out != nil {
		t.Fatalf("parse legacy payload failed: name=%q desc=%q in=%v out=%v", name, desc, in, out)
	}
}

func TestPrintToolHelpFlattensNestedOutputPaths(t *testing.T) {
	input := map[string]any{"type": "object"}
	output := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string"},
						"owner": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"login": map[string]any{"type": "string"},
							},
						},
					},
				},
			},
		},
	}

	var out bytes.Buffer
	printToolHelp(&out, "github", "search-repositories", "", input, output)
	got := out.String()
	if !bytes.Contains(out.Bytes(), []byte("items[].name <string>")) {
		t.Fatalf("missing nested array field path: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("items[].owner.login <string>")) {
		t.Fatalf("missing nested object field path: %q", got)
	}
}

func TestPrintToolHelpHandlesRootArrayOutputSchema(t *testing.T) {
	input := map[string]any{"type": "object"}
	output := map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string"},
			},
		},
	}

	var out bytes.Buffer
	printToolHelp(&out, "github", "list-results", "", input, output)
	got := out.String()

	if !bytes.Contains(out.Bytes(), []byte("[] <array>")) {
		t.Fatalf("missing root array line: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("[].id <string>")) {
		t.Fatalf("missing root array item field line: %q", got)
	}
}

func TestWriteManPageCreatesFileUnderXDGDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
	}
	output := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{"type": "array"},
		},
	}

	path, err := writeManPage("github", "search_repositories", "Search repos", input, output)
	if err != nil {
		t.Fatalf("writeManPage() error = %v", err)
	}

	expected := filepath.Join(os.Getenv("XDG_DATA_HOME"), "man", "man1", "mcpx-github-search-repositories.1")
	if path != expected {
		t.Fatalf("man page path = %q, want %q", path, expected)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading man page: %v", err)
	}
	if !bytes.Contains(data, []byte(".SH NAME")) {
		t.Fatalf("man page missing NAME section: %q", string(data))
	}
	if !bytes.Contains(data, []byte(".SH OUTPUT")) {
		t.Fatalf("man page missing OUTPUT section: %q", string(data))
	}
}

func TestPrintToolHelpIncludesOptionSemanticsAndExamples(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "search query",
			},
			"page": map[string]any{
				"type":        "integer",
				"description": "page number",
				"default":     float64(1),
			},
			"archived": map[string]any{
				"type":        "boolean",
				"description": "include archived repos",
				"default":     false,
			},
		},
		"required": []any{"query"},
	}

	var out bytes.Buffer
	printToolHelp(&out, "github", "search-repositories", "Search repos", input, nil)
	got := out.String()

	if !bytes.Contains(out.Bytes(), []byte("--query <string> (required)")) {
		t.Fatalf("expected required option marker, got: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("--page <integer> (optional, default: 1)")) {
		t.Fatalf("expected default value marker, got: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("--archived <boolean> (optional, default: false)")) {
		t.Fatalf("expected boolean default marker, got: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("Examples:")) {
		t.Fatalf("expected examples section, got: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("mcpx github search-repositories --query=example")) {
		t.Fatalf("expected flag example, got: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte(`mcpx github search-repositories '{"query":"example"}'`)) {
		t.Fatalf("expected positional json example, got: %q", got)
	}
}

func TestPrintToolHelpShowsGlobalFlagsAndCollisionNamespace(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cache":   map[string]any{"type": "boolean"},
			"query":   map[string]any{"type": "string"},
			"dry_run": map[string]any{"type": "boolean"},
		},
		"required": []any{"query"},
	}

	var out bytes.Buffer
	printToolHelp(&out, "github", "search-repositories", "", input, nil)
	got := out.String()

	if !bytes.Contains(out.Bytes(), []byte("Tool flags:")) {
		t.Fatalf("missing tool flags section: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("--tool-cache <boolean>")) {
		t.Fatalf("missing reserved tool flag prefix: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("--tool-no-cache <boolean>")) {
		t.Fatalf("missing reserved negative tool flag prefix: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("--dry_run <boolean>")) {
		t.Fatalf("missing boolean tool flag: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("--no-dry_run <boolean>")) {
		t.Fatalf("missing boolean negation tool flag: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("Global flags:")) {
		t.Fatalf("missing global flags section: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("--cache <duration>")) {
		t.Fatalf("missing global cache flag help: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("Namespace:")) {
		t.Fatalf("missing namespace section: %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("Use -- to force all following flags to tool parameters")) {
		t.Fatalf("missing -- namespace guidance: %q", got)
	}
}

func TestToolExamplesUseToolPrefixForReservedFlags(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cache": map[string]any{"type": "boolean"},
		},
		"required": []any{"cache"},
	}

	examples := toolExamples("github", "search-repositories", input)
	if len(examples) == 0 {
		t.Fatal("toolExamples() returned no examples")
	}
	if !bytes.Contains([]byte(examples[0]), []byte("--tool-cache")) {
		t.Fatalf("expected reserved flag to use --tool- prefix, got %q", examples[0])
	}
}

func TestRenderManPageIncludesExamplesAndOptionSemantics(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
			"page":  map[string]any{"type": "integer", "default": float64(2)},
		},
		"required": []any{"query"},
	}

	content := renderManPage("github", "search_repositories", "Search repos", input, nil)
	if !bytes.Contains([]byte(content), []byte(".SH EXAMPLES")) {
		t.Fatalf("man page missing EXAMPLES section: %q", content)
	}
	if !bytes.Contains([]byte(content), []byte("(required)")) {
		t.Fatalf("man page missing required marker: %q", content)
	}
	if !bytes.Contains([]byte(content), []byte("(optional, default: 2)")) {
		t.Fatalf("man page missing default marker: %q", content)
	}
}

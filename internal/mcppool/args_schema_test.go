package mcppool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestCallToolCoercesArgsByInputSchema(t *testing.T) {
	var calledArgs map[string]any

	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type: "object",
						Properties: map[string]any{
							"page":    map[string]any{"type": "integer"},
							"score":   map[string]any{"type": "number"},
							"enabled": map[string]any{"type": "boolean"},
							"config": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"nested": map[string]any{"type": "string"},
								},
								"required": []string{"nested"},
							},
							"tags": map[string]any{
								"type":  "array",
								"items": map[string]any{"type": "integer"},
							},
						},
						Required: []string{"page", "enabled"},
					},
				},
			}, nil
		},
		callTool: func(_ context.Context, _ string, args map[string]any) (*mcp.CallToolResult, error) {
			calledArgs = args
			return &mcp.CallToolResult{}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	raw := map[string]any{
		"page":    "2",
		"score":   "1.5",
		"enabled": "false",
		"config":  `{"nested":"x"}`,
		"tags":    []any{"1", "2"},
	}
	argsJSON, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	if _, err := p.CallTool(context.Background(), "github", "search", argsJSON); err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	if _, ok := calledArgs["page"].(int64); !ok {
		t.Fatalf("page type = %T, want int64", calledArgs["page"])
	}
	if calledArgs["page"] != int64(2) {
		t.Fatalf("page = %v, want 2", calledArgs["page"])
	}
	if _, ok := calledArgs["score"].(float64); !ok {
		t.Fatalf("score type = %T, want float64", calledArgs["score"])
	}
	if calledArgs["score"] != 1.5 {
		t.Fatalf("score = %v, want 1.5", calledArgs["score"])
	}
	if _, ok := calledArgs["enabled"].(bool); !ok {
		t.Fatalf("enabled type = %T, want bool", calledArgs["enabled"])
	}
	if calledArgs["enabled"] != false {
		t.Fatalf("enabled = %v, want false", calledArgs["enabled"])
	}

	configArg, ok := calledArgs["config"].(map[string]any)
	if !ok {
		t.Fatalf("config type = %T, want map[string]any", calledArgs["config"])
	}
	if configArg["nested"] != "x" {
		t.Fatalf("config.nested = %v, want x", configArg["nested"])
	}

	tags, ok := calledArgs["tags"].([]any)
	if !ok {
		t.Fatalf("tags type = %T, want []any", calledArgs["tags"])
	}
	if len(tags) != 2 || tags[0] != int64(1) || tags[1] != int64(2) {
		t.Fatalf("tags = %#v, want [1 2]", tags)
	}
}

func TestCallToolRejectsMissingRequiredArgs(t *testing.T) {
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type: "object",
						Properties: map[string]any{
							"query": map[string]any{"type": "string"},
						},
						Required: []string{"query"},
					},
				},
			}, nil
		},
		callTool: func(_ context.Context, _ string, _ map[string]any) (*mcp.CallToolResult, error) {
			t.Fatal("callTool should not run when required args are missing")
			return nil, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	_, err := p.CallTool(context.Background(), "github", "search", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("CallTool() error = nil, want non-nil")
	}
	if !errors.Is(err, mcp.ErrInvalidParams) {
		t.Fatalf("CallTool() error = %v, want invalid params", err)
	}
}

func TestCallToolAllowsUnknownFlagsWhenAdditionalPropertiesIsOmitted(t *testing.T) {
	var calledArgs map[string]any

	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type: "object",
						Properties: map[string]any{
							"query": map[string]any{"type": "string"},
						},
					},
				},
			}, nil
		},
		callTool: func(_ context.Context, _ string, args map[string]any) (*mcp.CallToolResult, error) {
			calledArgs = args
			return &mcp.CallToolResult{}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	_, err := p.CallTool(context.Background(), "github", "search", json.RawMessage(`{"query":"mcp","unexpected":"value"}`))
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if calledArgs["unexpected"] != "value" {
		t.Fatalf("unexpected arg = %#v, want value", calledArgs["unexpected"])
	}
}

func TestCallToolRejectsUnknownFlagsWhenAdditionalPropertiesFalse(t *testing.T) {
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type: "object",
						Properties: map[string]any{
							"query": map[string]any{"type": "string"},
						},
						AdditionalProperties: false,
					},
				},
			}, nil
		},
		callTool: func(_ context.Context, _ string, _ map[string]any) (*mcp.CallToolResult, error) {
			t.Fatal("callTool should not run for unknown flags")
			return nil, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	_, err := p.CallTool(context.Background(), "github", "search", json.RawMessage(`{"query":"mcp","unexpected":"value"}`))
	if err == nil {
		t.Fatal("CallTool() error = nil, want non-nil")
	}
	if !errors.Is(err, mcp.ErrInvalidParams) {
		t.Fatalf("CallTool() error = %v, want invalid params", err)
	}
}

func TestCallToolAllowsBooleanPropertySchemaWhenAdditionalPropertiesFalse(t *testing.T) {
	var calledArgs map[string]any

	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type: "object",
						Properties: map[string]any{
							"query": true,
						},
						AdditionalProperties: false,
					},
				},
			}, nil
		},
		callTool: func(_ context.Context, _ string, args map[string]any) (*mcp.CallToolResult, error) {
			calledArgs = args
			return &mcp.CallToolResult{}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.CallTool(context.Background(), "github", "search", json.RawMessage(`{"query":"mcp"}`)); err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if calledArgs["query"] != "mcp" {
		t.Fatalf("query = %#v, want mcp", calledArgs["query"])
	}
}

func TestCallToolCoercesUnknownFlagsWithAdditionalPropertiesSchema(t *testing.T) {
	var calledArgs map[string]any

	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type: "object",
						Properties: map[string]any{
							"query": map[string]any{"type": "string"},
						},
						AdditionalProperties: map[string]any{"type": "integer"},
					},
				},
			}, nil
		},
		callTool: func(_ context.Context, _ string, args map[string]any) (*mcp.CallToolResult, error) {
			calledArgs = args
			return &mcp.CallToolResult{}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.CallTool(context.Background(), "github", "search", json.RawMessage(`{"query":"mcp","page":"2"}`)); err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	if calledArgs["page"] != int64(2) {
		t.Fatalf("page = %#v, want int64(2)", calledArgs["page"])
	}
}

func TestCallToolRejectsUnknownFlagsWhenAdditionalPropertiesFalseAndNoProperties(t *testing.T) {
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type:                 "object",
						AdditionalProperties: false,
					},
				},
			}, nil
		},
		callTool: func(_ context.Context, _ string, _ map[string]any) (*mcp.CallToolResult, error) {
			t.Fatal("callTool should not run for unknown flags")
			return nil, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	_, err := p.CallTool(context.Background(), "github", "search", json.RawMessage(`{"unexpected":"value"}`))
	if err == nil {
		t.Fatal("CallTool() error = nil, want non-nil")
	}
	if !errors.Is(err, mcp.ErrInvalidParams) {
		t.Fatalf("CallTool() error = %v, want invalid params", err)
	}
}

func TestCallToolCoercesUnknownFlagsWithAdditionalPropertiesSchemaAndNoProperties(t *testing.T) {
	var calledArgs map[string]any

	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type:                 "object",
						AdditionalProperties: map[string]any{"type": "integer"},
					},
				},
			}, nil
		},
		callTool: func(_ context.Context, _ string, args map[string]any) (*mcp.CallToolResult, error) {
			calledArgs = args
			return &mcp.CallToolResult{}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.CallTool(context.Background(), "github", "search", json.RawMessage(`{"page":"2"}`)); err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	if calledArgs["page"] != int64(2) {
		t.Fatalf("page = %#v, want int64(2)", calledArgs["page"])
	}
}

func TestCallToolTreatsNoPrefixedAliasAsBooleanNegation(t *testing.T) {
	var calledArgs map[string]any

	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type: "object",
						Properties: map[string]any{
							"dry-run": map[string]any{"type": "boolean"},
						},
					},
				},
			}, nil
		},
		callTool: func(_ context.Context, _ string, args map[string]any) (*mcp.CallToolResult, error) {
			calledArgs = args
			return &mcp.CallToolResult{}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.CallTool(context.Background(), "github", "search", json.RawMessage(`{"no-dry-run":true}`)); err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	if calledArgs["dry-run"] != false {
		t.Fatalf("dry-run = %#v, want false", calledArgs["dry-run"])
	}
	if _, ok := calledArgs["no-dry-run"]; ok {
		t.Fatalf("no-dry-run should be rewritten away, args = %#v", calledArgs)
	}
}

func TestCallToolPreservesLiteralNoPrefixedBooleanParams(t *testing.T) {
	var calledArgs map[string]any

	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type: "object",
						Properties: map[string]any{
							"no-color": map[string]any{"type": "boolean"},
						},
						Required: []string{"no-color"},
					},
				},
			}, nil
		},
		callTool: func(_ context.Context, _ string, args map[string]any) (*mcp.CallToolResult, error) {
			calledArgs = args
			return &mcp.CallToolResult{}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.CallTool(context.Background(), "github", "search", json.RawMessage(`{"no-color":true}`)); err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	if calledArgs["no-color"] != true {
		t.Fatalf("no-color = %#v, want true", calledArgs["no-color"])
	}
}

func TestCoerceIntegerCoversSupportedInputTypesAndFailures(t *testing.T) {
	t.Parallel()

	successes := []struct {
		name  string
		value any
		want  int64
	}{
		{name: "int", value: int(2), want: 2},
		{name: "int32", value: int32(3), want: 3},
		{name: "int64", value: int64(4), want: 4},
		{name: "float32 integral", value: float32(5), want: 5},
		{name: "float64 integral", value: float64(6), want: 6},
		{name: "json number", value: json.Number("7"), want: 7},
		{name: "string", value: " 8 ", want: 8},
	}

	for _, tc := range successes {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := coerceInteger(tc.value, "page")
			if err != nil {
				t.Fatalf("coerceInteger(%v) error = %v", tc.value, err)
			}
			if got != tc.want {
				t.Fatalf("coerceInteger(%v) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}

	failures := []struct {
		name       string
		value      any
		wantSubstr string
	}{
		{name: "float64 fraction", value: 1.5, wantSubstr: `must be integer`},
		{name: "json number fraction", value: json.Number("1.2"), wantSubstr: `must be integer`},
		{name: "string parse failure", value: "oops", wantSubstr: `must be integer`},
		{name: "invalid type", value: true, wantSubstr: `must be integer, got bool`},
	}

	for _, tc := range failures {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := coerceInteger(tc.value, "page")
			if err == nil {
				t.Fatalf("coerceInteger(%v) error = nil, want non-nil", tc.value)
			}
			if !errors.Is(err, mcp.ErrInvalidParams) {
				t.Fatalf("coerceInteger(%v) error = %v, want invalid params", tc.value, err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("coerceInteger(%v) error = %q, want substring %q", tc.value, err.Error(), tc.wantSubstr)
			}
		})
	}
}

func TestCoerceNumberCoversSupportedInputTypesAndFailures(t *testing.T) {
	t.Parallel()

	successes := []struct {
		name  string
		value any
		want  float64
	}{
		{name: "int", value: int(2), want: 2},
		{name: "int32", value: int32(3), want: 3},
		{name: "int64", value: int64(4), want: 4},
		{name: "float32", value: float32(5.5), want: 5.5},
		{name: "float64", value: float64(6.25), want: 6.25},
		{name: "json number", value: json.Number("7.75"), want: 7.75},
		{name: "string", value: " 8.5 ", want: 8.5},
	}

	for _, tc := range successes {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := coerceNumber(tc.value, "score")
			if err != nil {
				t.Fatalf("coerceNumber(%v) error = %v", tc.value, err)
			}
			if got != tc.want {
				t.Fatalf("coerceNumber(%v) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}

	failures := []struct {
		name       string
		value      any
		wantSubstr string
	}{
		{name: "json number parse failure", value: json.Number("oops"), wantSubstr: `must be number`},
		{name: "string parse failure", value: "nope", wantSubstr: `must be number`},
		{name: "invalid type", value: true, wantSubstr: `must be number, got bool`},
	}

	for _, tc := range failures {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := coerceNumber(tc.value, "score")
			if err == nil {
				t.Fatalf("coerceNumber(%v) error = nil, want non-nil", tc.value)
			}
			if !errors.Is(err, mcp.ErrInvalidParams) {
				t.Fatalf("coerceNumber(%v) error = %v, want invalid params", tc.value, err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("coerceNumber(%v) error = %q, want substring %q", tc.value, err.Error(), tc.wantSubstr)
			}
		})
	}
}

func TestCoerceArrayAcceptsJSONArrayAndScalarForms(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"items": map[string]any{"type": "integer"},
	}

	list, err := coerceArray([]any{"1", 2.0}, schema, "tags")
	if err != nil {
		t.Fatalf("coerceArray(slice) error = %v", err)
	}
	if len(list) != 2 || list[0] != int64(1) || list[1] != int64(2) {
		t.Fatalf("coerceArray(slice) = %#v, want [1 2]", list)
	}

	fromJSON, err := coerceArray(`[ "3", 4 ]`, schema, "tags")
	if err != nil {
		t.Fatalf("coerceArray(json) error = %v", err)
	}
	if len(fromJSON) != 2 || fromJSON[0] != int64(3) || fromJSON[1] != int64(4) {
		t.Fatalf("coerceArray(json) = %#v, want [3 4]", fromJSON)
	}

	fromScalarString, err := coerceArray("5", schema, "tags")
	if err != nil {
		t.Fatalf("coerceArray(string scalar) error = %v", err)
	}
	if len(fromScalarString) != 1 || fromScalarString[0] != int64(5) {
		t.Fatalf("coerceArray(string scalar) = %#v, want [5]", fromScalarString)
	}

	fromScalarInt, err := coerceArray(6, schema, "tags")
	if err != nil {
		t.Fatalf("coerceArray(int scalar) error = %v", err)
	}
	if len(fromScalarInt) != 1 || fromScalarInt[0] != int64(6) {
		t.Fatalf("coerceArray(int scalar) = %#v, want [6]", fromScalarInt)
	}
}

func TestCoerceArrayReturnsInvalidParamsForBadInput(t *testing.T) {
	t.Parallel()

	schema := map[string]any{
		"items": map[string]any{"type": "integer"},
	}

	_, err := coerceArray(`[1,`, schema, "tags")
	if err == nil {
		t.Fatal("coerceArray(invalid json) error = nil, want non-nil")
	}
	if !errors.Is(err, mcp.ErrInvalidParams) {
		t.Fatalf("coerceArray(invalid json) error = %v, want invalid params", err)
	}
	if !strings.Contains(err.Error(), `must be JSON array`) {
		t.Fatalf("coerceArray(invalid json) error = %q, want JSON array failure", err.Error())
	}

	_, err = coerceArray([]any{"oops"}, schema, "tags")
	if err == nil {
		t.Fatal("coerceArray(invalid item) error = nil, want non-nil")
	}
	if !errors.Is(err, mcp.ErrInvalidParams) {
		t.Fatalf("coerceArray(invalid item) error = %v, want invalid params", err)
	}
	if !strings.Contains(err.Error(), `tags[0]`) {
		t.Fatalf("coerceArray(invalid item) error = %q, want indexed path", err.Error())
	}
}

func TestSchemaTypeCoversExplicitAndInferredTypes(t *testing.T) {
	t.Parallel()

	if got := schemaType(nil); got != "" {
		t.Fatalf("schemaType(nil) = %q, want empty", got)
	}

	if got := schemaType(map[string]any{"type": " BOOLEAN "}); got != "boolean" {
		t.Fatalf("schemaType(explicit) = %q, want boolean", got)
	}

	if got := schemaType(map[string]any{"properties": map[string]any{"x": map[string]any{}}}); got != "object" {
		t.Fatalf("schemaType(inferred) = %q, want object", got)
	}

	if got := schemaType(map[string]any{}); got != "" {
		t.Fatalf("schemaType(empty) = %q, want empty", got)
	}
}

func TestInvalidParamsTypeWrapsErrInvalidParams(t *testing.T) {
	t.Parallel()

	err := invalidParamsType("page", "integer", true)
	if err == nil {
		t.Fatal("invalidParamsType() error = nil, want non-nil")
	}
	if !errors.Is(err, mcp.ErrInvalidParams) {
		t.Fatalf("invalidParamsType() error = %v, want invalid params", err)
	}
	if !strings.Contains(err.Error(), `argument "page" must be integer, got bool`) {
		t.Fatalf("invalidParamsType() error = %q, want type mismatch detail", err.Error())
	}
}

func TestCompileToolArgsRejectsNonObjectSchemaType(t *testing.T) {
	t.Parallel()

	_, err := compileToolArgsAgainstSchema(map[string]any{"value": "1"}, map[string]any{"type": "array"})
	if err == nil {
		t.Fatal("compileToolArgsAgainstSchema() error = nil, want non-nil")
	}
	if !errors.Is(err, mcp.ErrInvalidParams) {
		t.Fatalf("compileToolArgsAgainstSchema() error = %v, want invalid params", err)
	}
	if !strings.Contains(err.Error(), `tool input schema must be object`) {
		t.Fatalf("compileToolArgsAgainstSchema() error = %q, want schema type failure", err.Error())
	}
}

func TestCompileToolArgsReturnsParseErrorsForInvalidSchemaJSON(t *testing.T) {
	t.Parallel()

	_, err := compileToolArgs(map[string]any{"query": "mcp"}, json.RawMessage("{"))
	if err == nil {
		t.Fatal("compileToolArgs() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "parsing input schema") {
		t.Fatalf("compileToolArgs() error = %q, want schema parse error", err.Error())
	}
}

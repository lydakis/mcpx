package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPropTypeHandlesExplicitEnumAndFallback(t *testing.T) {
	if got := propType(map[string]any{"type": "string"}); got != "string" {
		t.Fatalf("propType(type=string) = %q, want %q", got, "string")
	}
	if got := propType(map[string]any{"enum": []any{"alpha", "beta"}}); got != "alpha|beta" {
		t.Fatalf("propType(enum) = %q, want %q", got, "alpha|beta")
	}
	if got := propType(map[string]any{}); got != "any" {
		t.Fatalf("propType(fallback) = %q, want %q", got, "any")
	}
}

func TestFormatDefaultValueCoversNumericAndFallbackTypes(t *testing.T) {
	tests := []struct {
		name  string
		value any
		typ   string
		want  string
	}{
		{name: "float integer type", value: float64(1.9), typ: "integer", want: "1"},
		{name: "float number type", value: float64(1.9), typ: "number", want: "1.9"},
		{name: "json number", value: json.Number("42"), typ: "integer", want: "42"},
		{name: "bool", value: true, typ: "boolean", want: "true"},
		{name: "string", value: "x", typ: "string", want: "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := formatDefaultValue(tt.value, tt.typ)
			if !ok {
				t.Fatalf("formatDefaultValue(%v, %q) ok = false, want true", tt.value, tt.typ)
			}
			if got != tt.want {
				t.Fatalf("formatDefaultValue(%v, %q) = %q, want %q", tt.value, tt.typ, got, tt.want)
			}
		})
	}

	got, ok := formatDefaultValue(map[string]any{"bad": func() {}}, "object")
	if !ok {
		t.Fatal("formatDefaultValue(unmarshalable object) ok = false, want true")
	}
	if strings.TrimSpace(got) == "" {
		t.Fatalf("formatDefaultValue(unmarshalable object) = %q, want non-empty fallback", got)
	}
}

func TestSampleValueCoversSchemaTypeBranches(t *testing.T) {
	if got := sampleValue(nil); got != "value" {
		t.Fatalf("sampleValue(nil) = %#v, want %q", got, "value")
	}

	withDefault := map[string]any{"type": "string", "default": "preset"}
	if got := sampleValue(withDefault); got != "preset" {
		t.Fatalf("sampleValue(default) = %#v, want %q", got, "preset")
	}

	withEnum := map[string]any{"enum": []any{"first", "second"}}
	if got := sampleValue(withEnum); got != "first" {
		t.Fatalf("sampleValue(enum) = %#v, want %q", got, "first")
	}

	if got := sampleValue(map[string]any{"type": "integer"}); got != 1 {
		t.Fatalf("sampleValue(integer) = %#v, want %d", got, 1)
	}
	if got := sampleValue(map[string]any{"type": "number"}); got != 1.5 {
		t.Fatalf("sampleValue(number) = %#v, want %v", got, 1.5)
	}
	if got := sampleValue(map[string]any{"type": "boolean"}); got != true {
		t.Fatalf("sampleValue(boolean) = %#v, want true", got)
	}

	arr := sampleValue(map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "integer"},
	})
	values, ok := arr.([]any)
	if !ok || len(values) != 1 || values[0] != 1 {
		t.Fatalf("sampleValue(array) = %#v, want []any{1}", arr)
	}

	obj := sampleValue(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"z": map[string]any{"type": "string"},
			"a": map[string]any{"type": "boolean"},
		},
	})
	props, ok := obj.(map[string]any)
	if !ok {
		t.Fatalf("sampleValue(object) = %#v, want map[string]any", obj)
	}
	if len(props) != 1 || props["a"] != true {
		t.Fatalf("sampleValue(object) = %#v, want map[a:true]", obj)
	}
}

func TestSampleLiteralFormatsNumericValues(t *testing.T) {
	if got := sampleLiteral(float64(2)); got != "2" {
		t.Fatalf("sampleLiteral(2.0) = %q, want %q", got, "2")
	}
	if got := sampleLiteral(float64(2.25)); got != "2.25" {
		t.Fatalf("sampleLiteral(2.25) = %q, want %q", got, "2.25")
	}
	if got := sampleLiteral(json.Number("7.5")); got != "7.5" {
		t.Fatalf("sampleLiteral(json.Number) = %q, want %q", got, "7.5")
	}
	if got := sampleLiteral(true); got != "true" {
		t.Fatalf("sampleLiteral(true) = %q, want %q", got, "true")
	}
}

func TestSampleFlagTokensHandlesBooleanNegationAndStructuredValues(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"dry_run":  map[string]any{"type": "boolean"},
			"no-cache": map[string]any{"type": "boolean"},
			"filters":  map[string]any{"type": "object"},
			"tags":     map[string]any{"type": "array"},
		},
	}
	args := map[string]any{
		"dry_run":  false,
		"no-cache": false,
		"filters":  map[string]any{"state": "open"},
		"tags":     []any{"a", "b"},
	}

	tokens := sampleFlagTokens(schema, args)
	joined := strings.Join(tokens, " ")
	for _, want := range []string{
		"--no-dry_run",
		"--tool-no-cache",
		`--filters='{"state":"open"}'`,
		`--tags='["a","b"]'`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("sampleFlagTokens() missing %q in %q", want, joined)
		}
	}
}

func TestSchemaLinesHandlesRootArrayPrimitiveItems(t *testing.T) {
	lines := schemaLines(map[string]any{
		"type":        "array",
		"description": "values",
		"items":       map[string]any{"type": "string"},
	})
	if len(lines) != 2 {
		t.Fatalf("len(schemaLines(root array primitive)) = %d, want 2", len(lines))
	}
	if lines[0].Path != "[]" || lines[0].Type != "array" {
		t.Fatalf("lines[0] = %#v, want path/type []/array", lines[0])
	}
	if lines[1].Path != "[]" || lines[1].Type != "string" {
		t.Fatalf("lines[1] = %#v, want path/type []/string", lines[1])
	}
}

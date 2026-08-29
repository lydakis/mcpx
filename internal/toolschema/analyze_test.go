package toolschema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalyzeSimpleObjectSupportsFlags(t *testing.T) {
	analysis := AnalyzeRaw(json.RawMessage(`{
		"type":"object",
		"properties":{"count":{"type":"integer"},"labels":{"type":"array","items":{"type":"string"}}}
	}`))
	if !analysis.FlagSafe {
		t.Fatalf("FlagSafe = false, reason = %q", analysis.Reason)
	}
}

func TestAnalyzeCompositionRequiresJSON(t *testing.T) {
	analysis := AnalyzeRaw(json.RawMessage(`{
		"type":"object",
		"properties":{"target":{"oneOf":[{"type":"string"},{"type":"integer"}]}}
	}`))
	if analysis.FlagSafe {
		t.Fatal("FlagSafe = true, want false")
	}
	if !strings.Contains(analysis.Reason, "oneOf") {
		t.Fatalf("Reason = %q, want oneOf", analysis.Reason)
	}
}

func TestAnalyzeLocalAndExternalRefsRequireJSON(t *testing.T) {
	for _, ref := range []string{"#/$defs/target", "https://example.com/schema.json"} {
		analysis := AnalyzeRaw(json.RawMessage(`{"type":"object","properties":{"target":{"$ref":` + mustJSONString(t, ref) + `}}}`))
		if analysis.FlagSafe || !strings.Contains(analysis.Reason, "$ref") {
			t.Fatalf("AnalyzeRaw(%q) = %+v, want JSON-only ref", ref, analysis)
		}
	}
}

func TestAnalyzeUntypedNonStringEnumAndConstRequireJSON(t *testing.T) {
	for _, schema := range []string{
		`{"type":"object","properties":{"value":{"enum":[1,2]}}}`,
		`{"type":"object","properties":{"value":{"const":true}}}`,
	} {
		analysis := AnalyzeRaw(json.RawMessage(schema))
		if analysis.FlagSafe {
			t.Fatalf("AnalyzeRaw(%s) FlagSafe = true, want false", schema)
		}
		if !strings.Contains(analysis.Reason, "without an explicit type") {
			t.Fatalf("AnalyzeRaw(%s) reason = %q, want explicit-type guidance", schema, analysis.Reason)
		}
	}
}

func TestAnalyzeUntypedStringEnumSupportsFlags(t *testing.T) {
	analysis := AnalyzeRaw(json.RawMessage(`{"type":"object","properties":{"value":{"enum":["a","b"]}}}`))
	if !analysis.FlagSafe {
		t.Fatalf("FlagSafe = false, reason = %q", analysis.Reason)
	}
}

func TestAnalyzeAmbiguousUntypedPropertiesRequireJSON(t *testing.T) {
	for _, schema := range []string{
		`{"type":"object","properties":{"value":{"default":1}}}`,
		`{"type":"object","properties":{"value":{"default":"text"}}}`,
		`{"type":"object","properties":{"value":{}}}`,
	} {
		analysis := AnalyzeRaw(json.RawMessage(schema))
		if analysis.FlagSafe {
			t.Fatalf("AnalyzeRaw(%s) FlagSafe = true, want false", schema)
		}
		if !strings.Contains(analysis.Reason, "without an explicit type") {
			t.Fatalf("AnalyzeRaw(%s) reason = %q, want explicit-type guidance", schema, analysis.Reason)
		}
	}
}

func TestAnalyzeUntypedStringConstSupportsFlags(t *testing.T) {
	analysis := AnalyzeRaw(json.RawMessage(`{"type":"object","properties":{"value":{"const":"fixed"}}}`))
	if !analysis.FlagSafe {
		t.Fatalf("FlagSafe = false, reason = %q", analysis.Reason)
	}
}

func TestAnalyzeNullTypeRequiresJSON(t *testing.T) {
	analysis := AnalyzeRaw(json.RawMessage(`{"type":"object","properties":{"value":{"type":"null"}}}`))
	if analysis.FlagSafe {
		t.Fatal("FlagSafe = true, want false")
	}
	if !strings.Contains(analysis.Reason, "null") {
		t.Fatalf("Reason = %q, want null type guidance", analysis.Reason)
	}
}

func TestAnalyzeAmbiguousFlagPropertyNamesRequireJSON(t *testing.T) {
	for _, name := range []string{"tool-interactive", "value=alias", "has space", "-leading"} {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				name: map[string]any{"type": "string"},
			},
		}
		analysis := Analyze(schema)
		if analysis.FlagSafe {
			t.Fatalf("Analyze(property %q) FlagSafe = true, want false", name)
		}
		if !strings.Contains(analysis.Reason, name) {
			t.Fatalf("Analyze(property %q) reason = %q, want property name", name, analysis.Reason)
		}
	}
}

func TestAnalyzeNestedPropertyNamesRemainJSONRepresentable(t *testing.T) {
	analysis := AnalyzeRaw(json.RawMessage(`{
		"type":"object",
		"properties":{"config":{"type":"object","properties":{"tool-value":{"type":"string"}}}}
	}`))
	if !analysis.FlagSafe {
		t.Fatalf("FlagSafe = false, reason = %q", analysis.Reason)
	}
}

func mustJSONString(t *testing.T, value string) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

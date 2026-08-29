// Package toolschema classifies MCP tool schemas for shell-native use.
package toolschema

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

const maxInspectionDepth = 32

// Analysis describes whether a JSON Schema can be represented truthfully as
// mcpx flags. Schemas that are valid JSON Schema but ambiguous as flags remain
// usable through positional or stdin JSON.
type Analysis struct {
	FlagSafe bool
	Reason   string
}

// AnalyzeRaw classifies a raw JSON Schema document.
func AnalyzeRaw(raw json.RawMessage) Analysis {
	if len(raw) == 0 {
		return Analysis{FlagSafe: true}
	}
	var schema any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return Analysis{Reason: "the server returned an invalid JSON Schema document"}
	}
	root, ok := schema.(map[string]any)
	if !ok {
		return Analysis{Reason: "the input schema is not an object schema"}
	}
	return Analyze(root)
}

// Analyze classifies a decoded JSON Schema document.
func Analyze(schema map[string]any) Analysis {
	if len(schema) == 0 {
		return Analysis{FlagSafe: true}
	}
	if reason := unsupportedKeyword(schema, "input", 0, true); reason != "" {
		return Analysis{Reason: reason}
	}
	typ := schemaType(schema)
	if typ != "" && typ != "object" {
		return Analysis{Reason: fmt.Sprintf("the root input type is %s, not object", typ)}
	}
	return Analysis{FlagSafe: true}
}

func unsupportedKeyword(schema map[string]any, path string, depth int, root bool) string {
	if depth > maxInspectionDepth {
		return fmt.Sprintf("%s exceeds the maximum schema inspection depth", path)
	}

	for _, keyword := range []string{
		"$ref", "$dynamicRef", "allOf", "anyOf", "oneOf", "not", "if", "then", "else",
		"dependentSchemas", "patternProperties", "unevaluatedProperties", "propertyNames",
		"prefixItems", "contains",
	} {
		if _, ok := schema[keyword]; ok {
			return fmt.Sprintf("%s uses %s", path, keyword)
		}
	}

	if rawType, ok := schema["type"]; ok {
		typ, ok := rawType.(string)
		if !ok {
			return fmt.Sprintf("%s uses a union type", path)
		}
		typ = strings.ToLower(strings.TrimSpace(typ))
		switch typ {
		case "object", "array", "string", "integer", "number", "boolean":
		default:
			return fmt.Sprintf("%s uses unsupported type %s", path, typ)
		}
	} else {
		provenString := false
		if values, ok := schema["enum"].([]any); ok {
			provenString = len(values) > 0
			for _, value := range values {
				if _, ok := value.(string); !ok {
					return fmt.Sprintf("%s uses a non-string enum without an explicit type", path)
				}
			}
		}
		if value, ok := schema["const"]; ok {
			if _, ok := value.(string); !ok {
				return fmt.Sprintf("%s uses a non-string const without an explicit type", path)
			}
			provenString = true
		}
		if !root && schemaType(schema) == "" && !provenString {
			return fmt.Sprintf("%s is ambiguous without an explicit type", path)
		}
	}

	if props, ok := schema["properties"].(map[string]any); ok {
		for name, raw := range props {
			if root {
				if reason := unsupportedFlagPropertyName(name); reason != "" {
					return reason
				}
			}
			child, ok := raw.(map[string]any)
			if !ok {
				return fmt.Sprintf("%s.%s uses a boolean or malformed subschema", path, name)
			}
			if reason := unsupportedKeyword(child, path+"."+name, depth+1, false); reason != "" {
				return reason
			}
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		if reason := unsupportedKeyword(items, path+"[]", depth+1, false); reason != "" {
			return reason
		}
	}
	if additional, ok := schema["additionalProperties"].(map[string]any); ok {
		if reason := unsupportedKeyword(additional, path+".*", depth+1, false); reason != "" {
			return reason
		}
	}
	return ""
}

func unsupportedFlagPropertyName(name string) string {
	if name == "" {
		return "input uses an empty property name"
	}
	if strings.HasPrefix(name, "tool-") {
		return fmt.Sprintf("input property %q collides with the --tool- flag namespace", name)
	}
	if strings.HasPrefix(name, "-") || strings.ContainsRune(name, '=') {
		return fmt.Sprintf("input property %q cannot be represented as a flag", name)
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Sprintf("input property %q cannot be represented as a flag", name)
		}
	}
	return ""
}

func schemaType(schema map[string]any) string {
	if raw, ok := schema["type"].(string); ok {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	if _, ok := schema["properties"]; ok {
		return "object"
	}
	return ""
}

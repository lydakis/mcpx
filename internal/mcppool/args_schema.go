package mcppool

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func compileToolArgs(raw map[string]any, schemaRaw json.RawMessage) (map[string]any, error) {
	if raw == nil {
		raw = map[string]any{}
	}
	if len(schemaRaw) == 0 {
		return raw, nil
	}

	var schema map[string]any
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		return nil, fmt.Errorf("parsing input schema: %w", err)
	}
	if len(schema) == 0 {
		return raw, nil
	}

	typ := schemaType(schema)
	if typ != "" && typ != "object" {
		return nil, invalidParamsError("tool input schema must be object, got %q", typ)
	}

	compiled, err := coerceObject(raw, schema, "")
	if err != nil {
		return nil, err
	}
	return compiled, nil
}

func coerceObject(raw map[string]any, schema map[string]any, path string) (map[string]any, error) {
	if raw == nil {
		raw = map[string]any{}
	}

	props, _ := schema["properties"].(map[string]any)
	required := requiredSet(schema)
	var err error
	raw, err = rewriteNoPrefixedBooleanAliases(raw, props, path)
	if err != nil {
		return nil, err
	}

	if len(props) > 0 {
		for key := range raw {
			if _, ok := props[key]; !ok {
				return nil, invalidParamsError("unknown argument %q", dottedPath(path, key))
			}
		}
	}

	for key := range required {
		if _, ok := raw[key]; !ok {
			return nil, invalidParamsError("missing required argument %q", dottedPath(path, key))
		}
	}

	out := make(map[string]any, len(raw))
	for key, value := range raw {
		propSchema, _ := props[key].(map[string]any)
		if propSchema == nil {
			out[key] = value
			continue
		}

		coerced, err := coerceValue(value, propSchema, dottedPath(path, key))
		if err != nil {
			return nil, err
		}
		out[key] = coerced
	}
	return out, nil
}

func rewriteNoPrefixedBooleanAliases(raw map[string]any, props map[string]any, path string) (map[string]any, error) {
	if len(raw) == 0 || len(props) == 0 {
		return raw, nil
	}

	rewritten := make(map[string]any, len(raw))
	for key, value := range raw {
		rewritten[key] = value
	}

	for key, value := range raw {
		if _, ok := props[key]; ok {
			continue
		}
		if !strings.HasPrefix(key, "no-") || len(key) <= len("no-") {
			continue
		}

		baseKey := strings.TrimPrefix(key, "no-")
		baseSchema, ok := props[baseKey].(map[string]any)
		if !ok || schemaType(baseSchema) != "boolean" {
			continue
		}
		if _, exists := rewritten[baseKey]; exists {
			return nil, invalidParamsError("conflicting arguments %q and %q", dottedPath(path, baseKey), dottedPath(path, key))
		}

		negatedValue, err := coerceBoolean(value, dottedPath(path, key))
		if err != nil {
			return nil, err
		}
		rewritten[baseKey] = !negatedValue
		delete(rewritten, key)
	}

	return rewritten, nil
}

func coerceValue(value any, schema map[string]any, path string) (any, error) {
	if schema == nil || value == nil {
		return value, nil
	}

	switch schemaType(schema) {
	case "string":
		s, ok := value.(string)
		if !ok {
			return nil, invalidParamsType(path, "string", value)
		}
		return s, nil
	case "integer":
		return coerceInteger(value, path)
	case "number":
		return coerceNumber(value, path)
	case "boolean":
		return coerceBoolean(value, path)
	case "array":
		return coerceArray(value, schema, path)
	case "object":
		return coerceObjectValue(value, schema, path)
	default:
		return value, nil
	}
}

func coerceInteger(value any, path string) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case float32:
		f := float64(v)
		if math.Trunc(f) != f {
			return 0, invalidParamsError("argument %q must be integer", path)
		}
		return int64(f), nil
	case float64:
		if math.Trunc(v) != v {
			return 0, invalidParamsError("argument %q must be integer", path)
		}
		return int64(v), nil
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return 0, invalidParamsError("argument %q must be integer: %v", path, err)
		}
		return i, nil
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, invalidParamsError("argument %q must be integer: %v", path, err)
		}
		return i, nil
	default:
		return 0, invalidParamsType(path, "integer", value)
	}
}

func coerceNumber(value any, path string) (float64, error) {
	switch v := value.(type) {
	case int:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case float32:
		return float64(v), nil
	case float64:
		return v, nil
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, invalidParamsError("argument %q must be number: %v", path, err)
		}
		return f, nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, invalidParamsError("argument %q must be number: %v", path, err)
		}
		return f, nil
	default:
		return 0, invalidParamsType(path, "number", value)
	}
}

func coerceBoolean(value any, path string) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return false, invalidParamsError("argument %q must be boolean: %v", path, err)
		}
		return b, nil
	default:
		return false, invalidParamsType(path, "boolean", value)
	}
}

func coerceArray(value any, schema map[string]any, path string) ([]any, error) {
	itemsSchema, _ := schema["items"].(map[string]any)

	switch v := value.(type) {
	case []any:
		out := make([]any, 0, len(v))
		for i, item := range v {
			coerced, err := coerceArrayItem(item, itemsSchema, indexedPath(path, i))
			if err != nil {
				return nil, err
			}
			out = append(out, coerced)
		}
		return out, nil
	case string:
		trimmed := strings.TrimSpace(v)
		if strings.HasPrefix(trimmed, "[") {
			var parsed []any
			if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
				return nil, invalidParamsError("argument %q must be JSON array: %v", path, err)
			}
			out := make([]any, 0, len(parsed))
			for i, item := range parsed {
				coerced, err := coerceArrayItem(item, itemsSchema, indexedPath(path, i))
				if err != nil {
					return nil, err
				}
				out = append(out, coerced)
			}
			return out, nil
		}
		coerced, err := coerceArrayItem(v, itemsSchema, indexedPath(path, 0))
		if err != nil {
			return nil, err
		}
		return []any{coerced}, nil
	default:
		coerced, err := coerceArrayItem(v, itemsSchema, indexedPath(path, 0))
		if err != nil {
			return nil, err
		}
		return []any{coerced}, nil
	}
}

func coerceArrayItem(value any, schema map[string]any, path string) (any, error) {
	if schema == nil {
		return value, nil
	}
	return coerceValue(value, schema, path)
}

func coerceObjectValue(value any, schema map[string]any, path string) (map[string]any, error) {
	switch v := value.(type) {
	case map[string]any:
		return coerceObject(v, schema, path)
	case string:
		var parsed any
		if err := json.Unmarshal([]byte(strings.TrimSpace(v)), &parsed); err != nil {
			return nil, invalidParamsError("argument %q must be JSON object: %v", path, err)
		}
		obj, ok := parsed.(map[string]any)
		if !ok {
			return nil, invalidParamsError("argument %q must be object", path)
		}
		return coerceObject(obj, schema, path)
	default:
		return nil, invalidParamsType(path, "object", value)
	}
}

func requiredSet(schema map[string]any) map[string]struct{} {
	out := map[string]struct{}{}

	switch v := schema["required"].(type) {
	case []string:
		for _, name := range v {
			out[name] = struct{}{}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				out[s] = struct{}{}
			}
		}
	}

	return out
}

func schemaType(schema map[string]any) string {
	if schema == nil {
		return ""
	}
	if t, ok := schema["type"].(string); ok {
		return strings.TrimSpace(strings.ToLower(t))
	}
	if _, ok := schema["properties"]; ok {
		return "object"
	}
	return ""
}

func invalidParamsType(path, want string, got any) error {
	return invalidParamsError("argument %q must be %s, got %T", path, want, got)
}

func invalidParamsError(format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return fmt.Errorf("%w: %s", mcp.ErrInvalidParams, msg)
}

func dottedPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func indexedPath(path string, idx int) string {
	return fmt.Sprintf("%s[%d]", path, idx)
}

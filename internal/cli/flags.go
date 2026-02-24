package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type toolCallArgs struct {
	toolArgs map[string]any
	cacheTTL *time.Duration
	verbose  bool
	quiet    bool
	help     bool
}

func parseToolCallArgs(args []string, stdin io.Reader, stdinIsTTY bool) (*toolCallArgs, error) {
	parsed := &toolCallArgs{
		toolArgs: make(map[string]any),
	}

	var positionalJSON string
	hasToolFlags := false
	hasAnyFlags := false
	afterSeparator := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			afterSeparator = true
			continue
		}

		if !afterSeparator {
			switch {
			case arg == "-v" || arg == "--verbose":
				parsed.verbose = true
				hasAnyFlags = true
				continue
			case arg == "-q" || arg == "--quiet":
				parsed.quiet = true
				hasAnyFlags = true
				continue
			case arg == "-h" || arg == "--help":
				parsed.help = true
				hasAnyFlags = true
				continue
			case strings.HasPrefix(arg, "--cache="):
				if parsed.cacheTTL != nil {
					return nil, fmt.Errorf("conflicting cache flags")
				}
				ttl, err := parseCacheDuration(strings.TrimPrefix(arg, "--cache="))
				if err != nil {
					return nil, err
				}
				parsed.cacheTTL = &ttl
				hasAnyFlags = true
				continue
			case arg == "--cache":
				if parsed.cacheTTL != nil {
					return nil, fmt.Errorf("conflicting cache flags")
				}
				if i+1 >= len(args) {
					return nil, fmt.Errorf("missing value for --cache")
				}
				i++
				ttl, err := parseCacheDuration(args[i])
				if err != nil {
					return nil, err
				}
				parsed.cacheTTL = &ttl
				hasAnyFlags = true
				continue
			case arg == "--no-cache":
				if parsed.cacheTTL != nil {
					return nil, fmt.Errorf("conflicting cache flags")
				}
				ttl := time.Duration(0)
				parsed.cacheTTL = &ttl
				hasAnyFlags = true
				continue
			}
		}

		if strings.HasPrefix(arg, "--") {
			flagArg := arg
			if strings.HasPrefix(arg, "--tool-") {
				flagArg = "--" + strings.TrimPrefix(arg, "--tool-")
			}
			if positionalJSON != "" {
				return nil, fmt.Errorf("cannot mix positional JSON arguments with --flags")
			}

			key, value, err := parseLongFlagValue(args, &i, flagArg)
			if err != nil {
				return nil, err
			}
			putArgValue(parsed.toolArgs, key, value)
			hasToolFlags = true
			hasAnyFlags = true
			continue
		}

		if strings.HasPrefix(arg, "-") {
			return nil, fmt.Errorf("unsupported short flag: %s", arg)
		}

		if hasToolFlags {
			return nil, fmt.Errorf("unexpected positional argument: %s", arg)
		}
		if positionalJSON != "" {
			return nil, fmt.Errorf("multiple positional arguments are not supported")
		}
		positionalJSON = arg
	}

	if positionalJSON != "" {
		obj, err := parseJSONObject(positionalJSON)
		if err != nil {
			return nil, err
		}
		parsed.toolArgs = obj
		return parsed, nil
	}

	if !hasAnyFlags && !hasToolFlags && !stdinIsTTY && stdin != nil {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
		trimmed := strings.TrimSpace(string(data))
		if trimmed != "" {
			obj, err := parseJSONObject(trimmed)
			if err != nil {
				return nil, err
			}
			parsed.toolArgs = obj
		}
	}

	return parsed, nil
}

// parseFlags parses GNU-style flags (--key=value or --key value) into a map.
// Boolean flags (--flag without value) are set to true and --no-flag to false.
func parseFlags(args []string) (map[string]any, error) {
	if len(args) == 1 && !strings.HasPrefix(args[0], "--") {
		return parseJSONObject(args[0])
	}

	result := make(map[string]any)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			return nil, fmt.Errorf("unexpected positional argument: %s", arg)
		}

		key, value, err := parseLongFlagValue(args, &i, arg)
		if err != nil {
			return nil, err
		}
		putArgValue(result, key, value)
	}
	return result, nil
}

func parseJSONObject(raw string) (map[string]any, error) {
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("invalid JSON arguments: %w", err)
	}

	obj, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("JSON arguments must be an object")
	}
	return obj, nil
}

func parseLongFlagValue(args []string, idx *int, token string) (string, any, error) {
	body := strings.TrimPrefix(token, "--")
	if body == "" {
		return "", nil, fmt.Errorf("invalid flag: %s", token)
	}

	if eq := strings.Index(body, "="); eq >= 0 {
		key := body[:eq]
		value := body[eq+1:]
		if key == "" {
			return "", nil, fmt.Errorf("invalid flag: %s", token)
		}
		return key, value, nil
	}

	key := body
	if key == "" {
		return "", nil, fmt.Errorf("invalid flag: %s", token)
	}

	if *idx+1 < len(args) && !strings.HasPrefix(args[*idx+1], "--") {
		*idx = *idx + 1
		return key, args[*idx], nil
	}

	return key, true, nil
}

func putArgValue(dst map[string]any, key string, value any) {
	if existing, ok := dst[key]; ok {
		switch v := existing.(type) {
		case []any:
			dst[key] = append(v, value)
		default:
			dst[key] = []any{v, value}
		}
		return
	}
	dst[key] = value
}

func parseCacheDuration(raw string) (time.Duration, error) {
	ttl, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid --cache value: %w", err)
	}
	if ttl <= 0 {
		return 0, fmt.Errorf("--cache must be > 0")
	}
	return ttl, nil
}

package cli

import "sort"

var (
	globalCallFlags = []string{
		"--cache",
		"--no-cache",
		"--verbose",
		"-v",
		"--quiet",
		"-q",
		"--json",
		"--help",
		"-h",
	}
	reservedToolFlagNames = map[string]struct{}{
		"cache":    {},
		"no-cache": {},
		"verbose":  {},
		"quiet":    {},
		"json":     {},
		"help":     {},
		"version":  {},
	}
)

func toolFlagCompletions(inputSchema map[string]any) []string {
	flags := append([]string{}, globalCallFlags...)

	props, _ := inputSchema["properties"].(map[string]any)
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		prop, _ := props[name].(map[string]any)
		typ, _ := prop["type"].(string)
		base, neg := toolFlagNames(name, typ)

		flags = append(flags, base)
		if neg != "" {
			flags = append(flags, neg)
		}
	}

	return uniqueSorted(flags)
}

func isReservedToolFlagName(name string) bool {
	_, ok := reservedToolFlagNames[name]
	return ok
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

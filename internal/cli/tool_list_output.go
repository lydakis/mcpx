package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

type toolListEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func decodeToolListPayload(raw []byte) ([]toolListEntry, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("invalid daemon response for tool list: expected JSON array payload")
	}
	if trimmed[0] != '[' {
		return nil, fmt.Errorf("invalid daemon response for tool list: expected JSON array payload")
	}

	var entries []toolListEntry
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return nil, fmt.Errorf("invalid daemon response for tool list: %w", err)
	}
	if entries == nil {
		entries = make([]toolListEntry, 0)
	}
	return entries, nil
}

func writeToolListText(w io.Writer, entries []toolListEntry) error {
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		line := name
		if desc := strings.TrimSpace(entry.Description); desc != "" {
			line += "\t" + desc
		}
		if _, err := io.WriteString(w, line+"\n"); err != nil {
			return fmt.Errorf("writing tool list output: %w", err)
		}
	}
	return nil
}

func toolListNames(entries []toolListEntry) []string {
	seen := make(map[string]struct{}, len(entries))
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

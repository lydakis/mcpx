package httpheaders

import (
	"sort"
	"strings"
)

// Set writes a header value using case-insensitive key matching.
// If an equivalent key already exists with different casing, it is replaced.
func Set(headers map[string]string, name, value string) map[string]string {
	name = strings.TrimSpace(name)
	if name == "" {
		return headers
	}

	if headers == nil {
		headers = make(map[string]string, 1)
	}
	if existing, ok := lookupKeyFold(headers, name); ok && existing != name {
		delete(headers, existing)
	}
	headers[name] = value
	return headers
}

// Merge applies src entries into dst using case-insensitive key matching.
// When overwrite is false, existing dst entries win.
// When overwrite is true, src entries replace existing keys even if the casing differs.
func Merge(dst map[string]string, src map[string]string, overwrite bool) map[string]string {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]string, len(src))
	}

	keys := sortedKeys(src)
	for _, key := range keys {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}

		if existing, ok := lookupKeyFold(dst, name); ok {
			if !overwrite {
				continue
			}
			delete(dst, existing)
		}
		dst[name] = src[key]
	}
	return dst
}

func sortedKeys(src map[string]string) []string {
	keys := make([]string, 0, len(src))
	for key := range src {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		li := strings.ToLower(strings.TrimSpace(keys[i]))
		lj := strings.ToLower(strings.TrimSpace(keys[j]))
		if li == lj {
			return keys[i] < keys[j]
		}
		return li < lj
	})
	return keys
}

func lookupKeyFold(headers map[string]string, name string) (string, bool) {
	for key := range headers {
		if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(name)) {
			return key, true
		}
	}
	return "", false
}

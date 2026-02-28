package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeToolListPayloadRejectsLegacyText(t *testing.T) {
	raw := []byte("search_repositories\tSearch repositories\nlist_issues\tList issues\n")
	if _, err := decodeToolListPayload(raw); err == nil {
		t.Fatal("decodeToolListPayload() error = nil, want non-nil")
	}
}

func TestDecodeToolListPayloadParsesEmptyJSONListAsEmptySlice(t *testing.T) {
	entries, err := decodeToolListPayload([]byte("[]\n"))
	if err != nil {
		t.Fatalf("decodeToolListPayload() error = %v", err)
	}
	if entries == nil {
		t.Fatal("entries = nil, want empty slice")
	}

	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("json.Marshal(entries) error = %v", err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("json.Marshal(entries) = %q, want %q", string(encoded), "[]")
	}
}

func TestWriteToolListTextRendersNameAndDescription(t *testing.T) {
	entries := []toolListEntry{
		{Name: "list_issues", Description: "List issues"},
		{Name: "search_repositories"},
	}

	var out bytes.Buffer
	if err := writeToolListText(&out, entries); err != nil {
		t.Fatalf("writeToolListText() error = %v", err)
	}

	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(got) != 2 {
		t.Fatalf("writeToolListText() lines = %d, want 2 (output=%q)", len(got), out.String())
	}
	if fields := strings.Fields(got[0]); len(fields) < 2 || fields[0] != "list_issues" || strings.Join(fields[1:], " ") != "List issues" {
		t.Fatalf("first line = %q, want name and description columns", got[0])
	}
	if strings.TrimSpace(got[1]) != "search_repositories" {
		t.Fatalf("second line = %q, want %q", got[1], "search_repositories")
	}
}

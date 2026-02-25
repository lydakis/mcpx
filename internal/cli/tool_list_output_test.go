package cli

import (
	"bytes"
	"encoding/json"
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

	want := "list_issues\tList issues\nsearch_repositories\n"
	if out.String() != want {
		t.Fatalf("writeToolListText() = %q, want %q", out.String(), want)
	}
}

package httpheaders

import "testing"

func TestSetReplacesEquivalentKeyCaseInsensitively(t *testing.T) {
	headers := map[string]string{
		"authorization": "Bearer old",
	}
	got := Set(headers, "Authorization", "Bearer new")

	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 (got=%#v)", len(got), got)
	}
	if got["Authorization"] != "Bearer new" {
		t.Fatalf(`got["Authorization"] = %q, want %q`, got["Authorization"], "Bearer new")
	}
	if _, exists := got["authorization"]; exists {
		t.Fatalf("got = %#v, want lowercase key removed", got)
	}
}

func TestMergeSkipsEquivalentKeyWhenOverwriteDisabled(t *testing.T) {
	dst := map[string]string{
		"authorization": "Bearer explicit",
	}
	src := map[string]string{
		"Authorization": "Bearer fallback",
	}

	got := Merge(dst, src, false)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 (got=%#v)", len(got), got)
	}
	if got["authorization"] != "Bearer explicit" {
		t.Fatalf(`got["authorization"] = %q, want %q`, got["authorization"], "Bearer explicit")
	}
	if _, exists := got["Authorization"]; exists {
		t.Fatalf("got = %#v, want no duplicate Authorization key", got)
	}
}

func TestMergeOverwritesEquivalentKeyWhenEnabled(t *testing.T) {
	dst := map[string]string{
		"authorization": "Bearer old",
	}
	src := map[string]string{
		"Authorization": "Bearer new",
	}

	got := Merge(dst, src, true)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 (got=%#v)", len(got), got)
	}
	if got["Authorization"] != "Bearer new" {
		t.Fatalf(`got["Authorization"] = %q, want %q`, got["Authorization"], "Bearer new")
	}
	if _, exists := got["authorization"]; exists {
		t.Fatalf("got = %#v, want lowercase key removed", got)
	}
}

func TestSetInitializesMapAndIgnoresBlankHeaderName(t *testing.T) {
	got := Set(nil, " X-Trace-Id ", "abc123")
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 (got=%#v)", len(got), got)
	}
	if got["X-Trace-Id"] != "abc123" {
		t.Fatalf(`got["X-Trace-Id"] = %q, want %q`, got["X-Trace-Id"], "abc123")
	}

	same := Set(got, "   ", "ignored")
	if len(same) != 1 || same["X-Trace-Id"] != "abc123" {
		t.Fatalf("Set(blank) mutated headers unexpectedly: %#v", same)
	}
}

func TestMergeAllocatesDestinationAndSkipsBlankSourceKeys(t *testing.T) {
	src := map[string]string{
		"  ":            "ignored",
		"Content-Type ": "application/json",
	}
	got := Merge(nil, src, true)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 (got=%#v)", len(got), got)
	}
	if got["Content-Type"] != "application/json" {
		t.Fatalf(`got["Content-Type"] = %q, want %q`, got["Content-Type"], "application/json")
	}
}

func TestSortedKeysAndLookupKeyFold(t *testing.T) {
	src := map[string]string{
		" zeta": "1",
		"Alpha": "2",
		"beta":  "3",
	}
	keys := sortedKeys(src)
	if len(keys) != 3 {
		t.Fatalf("len(keys) = %d, want 3", len(keys))
	}
	if keys[0] != "Alpha" || keys[1] != "beta" || keys[2] != " zeta" {
		t.Fatalf("sortedKeys(src) = %#v, want [Alpha beta \" zeta\"]", keys)
	}

	if got, ok := lookupKeyFold(src, "  ALPHA "); !ok || got != "Alpha" {
		t.Fatalf("lookupKeyFold(ALPHA) = (%q, %v), want (%q, true)", got, ok, "Alpha")
	}
	if _, ok := lookupKeyFold(src, "missing"); ok {
		t.Fatal("lookupKeyFold(missing) = ok, want false")
	}
}

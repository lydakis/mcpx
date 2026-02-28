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

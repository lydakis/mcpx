package processenv

import (
	"reflect"
	"testing"
)

func TestMergeReplacesOverriddenKeysExactlyOnce(t *testing.T) {
	base := []string{"A=old", "PWD=/old", "UNCHANGED=yes", "A=duplicate", "NO_EQUALS"}
	got := Merge(base, map[string]string{"PWD": "/work/project", "A": "new"})
	want := []string{"UNCHANGED=yes", "NO_EQUALS", "A=new", "PWD=/work/project"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Merge() = %#v, want %#v", got, want)
	}
	if base[0] != "A=old" {
		t.Fatalf("Merge() mutated base = %#v", base)
	}
}

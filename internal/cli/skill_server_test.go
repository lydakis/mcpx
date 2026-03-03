package cli

import (
	"strings"
	"testing"
)

func TestServerSkillNameKeepsStableServerNames(t *testing.T) {
	got := serverSkillName("github")
	if got != "mcpx-github" {
		t.Fatalf("serverSkillName(github) = %q, want %q", got, "mcpx-github")
	}

	got = serverSkillName("github-enterprise_2")
	if got != "mcpx-github-enterprise_2" {
		t.Fatalf("serverSkillName(github-enterprise_2) = %q, want %q", got, "mcpx-github-enterprise_2")
	}
}

func TestServerSkillNameAvoidsCollisionsForDistinctServerIDs(t *testing.T) {
	lower := serverSkillName("github")
	upper := serverSkillName("GitHub")
	punct := serverSkillName("github!")

	if upper == lower {
		t.Fatalf("serverSkillName(GitHub) = %q, must differ from %q", upper, lower)
	}
	if punct == lower {
		t.Fatalf("serverSkillName(github!) = %q, must differ from %q", punct, lower)
	}
	if !strings.HasPrefix(upper, "mcpx-github-") {
		t.Fatalf("serverSkillName(GitHub) = %q, want prefix %q", upper, "mcpx-github-")
	}
	if !strings.HasPrefix(punct, "mcpx-github-") {
		t.Fatalf("serverSkillName(github!) = %q, want prefix %q", punct, "mcpx-github-")
	}
	if serverSkillName("GitHub") != upper {
		t.Fatalf("serverSkillName(GitHub) must be stable across calls")
	}
}

func TestServerSkillNameAddsHashForFullySanitizedInputs(t *testing.T) {
	got := serverSkillName("!!!")
	if got == "mcpx-server" {
		t.Fatalf("serverSkillName(!!!) = %q, want hashed suffix", got)
	}
	if !strings.HasPrefix(got, "mcpx-server-") {
		t.Fatalf("serverSkillName(!!!) = %q, want prefix %q", got, "mcpx-server-")
	}
}

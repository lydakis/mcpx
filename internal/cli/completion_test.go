package cli

import (
	"bytes"
	"testing"

	"github.com/lydakis/mcpx/internal/ipc"
)

func TestRunCompletionCommandBash(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	code := runCompletionCommand([]string{"bash"}, &out, &errOut)
	if code != ipc.ExitOK {
		t.Fatalf("runCompletionCommand() code = %d, want %d", code, ipc.ExitOK)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("mcpx __complete servers")) {
		t.Fatalf("bash completion missing internal server query: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("_mcpx_has_skill_server()")) {
		t.Fatalf("bash completion missing skill server guard helper: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("if [[ \"$first\" == \"skill\" ]] && ! _mcpx_has_skill_server; then")) {
		t.Fatalf("bash completion missing conditional skill branch: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("if ! _mcpx_has_skill_server; then")) {
		t.Fatalf("bash completion missing conditional skill root entry: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("--no-claude-link")) {
		t.Fatalf("bash completion missing skill install flags: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("complete -F _mcpx_completion mcpx")) {
		t.Fatalf("bash completion missing complete hook: %q", out.String())
	}
}

func TestRunCompletionCommandUnknownShell(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	code := runCompletionCommand([]string{"powershell"}, &out, &errOut)
	if code != ipc.ExitUsageErr {
		t.Fatalf("runCompletionCommand() code = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if !bytes.Contains(errOut.Bytes(), []byte("unknown shell for completion")) {
		t.Fatalf("stderr = %q, want unknown shell error", errOut.String())
	}
}

func TestRunInternalCompletionRequiresQueryType(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	code := runInternalCompletion(nil, &out, &errOut)
	if code != ipc.ExitUsageErr {
		t.Fatalf("runInternalCompletion() code = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if !bytes.Contains(errOut.Bytes(), []byte("usage")) {
		t.Fatalf("stderr = %q, want usage error", errOut.String())
	}
}

func TestRunCompletionCommandZshGuardsSkillBuiltIn(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	code := runCompletionCommand([]string{"zsh"}, &out, &errOut)
	if code != ipc.ExitOK {
		t.Fatalf("runCompletionCommand() code = %d, want %d", code, ipc.ExitOK)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("_mcpx_has_skill_server()")) {
		t.Fatalf("zsh completion missing skill server guard helper: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("if [[ \"${words[2]}\" == \"skill\" ]] && ! _mcpx_has_skill_server; then")) {
		t.Fatalf("zsh completion missing conditional skill branch: %q", out.String())
	}
}

func TestRunCompletionCommandFishGuardsSkillBuiltIn(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	code := runCompletionCommand([]string{"fish"}, &out, &errOut)
	if code != ipc.ExitOK {
		t.Fatalf("runCompletionCommand() code = %d, want %d", code, ipc.ExitOK)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("function __mcpx_has_skill_server")) {
		t.Fatalf("fish completion missing skill server guard helper: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("test \"$w[2]\" = skill; and not __mcpx_has_skill_server")) {
		t.Fatalf("fish completion missing conditional skill branch: %q", out.String())
	}
}

func TestToolFlagCompletionsHandlesCollisionsAndBooleanNegation(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cache":   map[string]any{"type": "boolean"},
			"query":   map[string]any{"type": "string"},
			"verbose": map[string]any{"type": "string"},
			"dry_run": map[string]any{"type": "boolean"},
		},
	}

	flags := toolFlagCompletions(input)
	want := []string{
		"--tool-cache",
		"--tool-no-cache",
		"--tool-verbose",
		"--query",
		"--dry_run",
		"--no-dry_run",
		"--cache",
		"--verbose",
		"--help",
		"--no-cache",
	}
	for _, flag := range want {
		if !contains(flags, flag) {
			t.Fatalf("toolFlagCompletions() missing %q in %v", flag, flags)
		}
	}
}

func TestParseToolListOutputExtractsUniqueSortedNames(t *testing.T) {
	got := parseToolListOutput([]byte("b\tsecond\n\nc\nb\tdup\na\tfirst\n"))
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len(parseToolListOutput()) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseToolListOutput()[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestParseToolListOutputPreservesNativeToolNames(t *testing.T) {
	got := parseToolListOutput([]byte("search_repositories\tsnake\nsearch-repositories\tkebab\n"))
	want := []string{"search-repositories", "search_repositories"}
	if len(got) != len(want) {
		t.Fatalf("len(parseToolListOutput()) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseToolListOutput()[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func contains(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

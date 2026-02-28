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
	if !bytes.Contains(out.Bytes(), []byte("_mcpx_has_add_server()")) {
		t.Fatalf("bash completion missing add server guard helper: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("if ! _mcpx_has_add_server; then")) {
		t.Fatalf("bash completion missing conditional add root entry: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("if [[ \"$first\" == \"add\" ]] && ! _mcpx_has_add_server; then")) {
		t.Fatalf("bash completion missing guarded add subcommand branch: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("--name --header --overwrite --help -h")) {
		t.Fatalf("bash completion missing add flags: %q", out.String())
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
	if !bytes.Contains(out.Bytes(), []byte("--kiro-link")) {
		t.Fatalf("bash completion missing kiro skill install flag: %q", out.String())
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
	if !bytes.Contains(out.Bytes(), []byte("_mcpx_has_add_server()")) {
		t.Fatalf("zsh completion missing add server guard helper: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("servers+=(completion --help -h --version -V --json)")) {
		t.Fatalf("zsh completion missing root command list: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("servers+=(add)")) {
		t.Fatalf("zsh completion missing conditional add root command: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("if [[ \"${words[2]}\" == \"add\" ]] && ! _mcpx_has_add_server; then")) {
		t.Fatalf("zsh completion missing guarded add subcommand branch: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("flags=(--name --header --overwrite --help -h)")) {
		t.Fatalf("zsh completion missing add flags: %q", out.String())
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
	if !bytes.Contains(out.Bytes(), []byte("function __mcpx_has_add_server")) {
		t.Fatalf("fish completion missing add server guard helper: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("completion --help -h --version -V --json")) {
		t.Fatalf("fish completion missing root command list: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("and not __mcpx_has_add_server")) {
		t.Fatalf("fish completion missing guarded add conditions: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("test \"$w[2]\" = add; and not __mcpx_has_add_server")) {
		t.Fatalf("fish completion missing guarded add subcommand branch: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("--name --header --overwrite --help -h")) {
		t.Fatalf("fish completion missing add flags: %q", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("test \"$w[2]\" != add; or __mcpx_has_add_server")) {
		t.Fatalf("fish completion missing guarded add exclusion in tool completion path: %q", out.String())
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
			"json":    map[string]any{"type": "boolean"},
			"query":   map[string]any{"type": "string"},
			"verbose": map[string]any{"type": "string"},
			"dry_run": map[string]any{"type": "boolean"},
		},
	}

	flags := toolFlagCompletions(input)
	want := []string{
		"--tool-cache",
		"--tool-no-cache",
		"--tool-json",
		"--tool-no-json",
		"--tool-verbose",
		"--query",
		"--dry_run",
		"--no-dry_run",
		"--cache",
		"--json",
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

func TestToolListNamesExtractUniqueSortedNames(t *testing.T) {
	got := toolListNames([]toolListEntry{
		{Name: "b"},
		{Name: "c"},
		{Name: "b"},
		{Name: "a"},
	})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len(toolListNames()) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("toolListNames()[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestDecodeToolListPayloadParsesJSONEntries(t *testing.T) {
	entries, err := decodeToolListPayload([]byte(`[{"name":"search_repositories"},{"name":"search-repositories"}]`))
	if err != nil {
		t.Fatalf("decodeToolListPayload() error = %v", err)
	}
	got := toolListNames(entries)
	want := []string{"search-repositories", "search_repositories"}
	if len(got) != len(want) {
		t.Fatalf("len(toolListNames()) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("toolListNames()[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
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

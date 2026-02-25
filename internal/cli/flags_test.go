package cli

import (
	"bytes"
	"testing"
	"time"
)

func TestParseFlagsSupportsPositionalJSONObject(t *testing.T) {
	got, err := parseFlags([]string{`{"query":"mcp","page":2}`})
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}

	if got["query"] != "mcp" {
		t.Fatalf("query = %v, want mcp", got["query"])
	}

	page, ok := got["page"].(float64)
	if !ok || page != 2 {
		t.Fatalf("page = %#v, want 2", got["page"])
	}
}

func TestParseToolCallArgsExtractsCacheTTL(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{"--cache=30s", "--query=mcp"}, bytes.NewBuffer(nil), true)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}

	if parsed.cacheTTL == nil || *parsed.cacheTTL != 30*time.Second {
		t.Fatalf("cacheTTL = %v, want 30s", parsed.cacheTTL)
	}
	if parsed.toolArgs["query"] != "mcp" {
		t.Fatalf("query = %v, want mcp", parsed.toolArgs["query"])
	}
}

func TestParseToolCallArgsNoCacheWithSeparator(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{"--no-cache", "--", "--cache=true"}, bytes.NewBuffer(nil), true)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}

	if parsed.cacheTTL == nil || *parsed.cacheTTL != 0 {
		t.Fatalf("cacheTTL = %v, want 0", parsed.cacheTTL)
	}

	if parsed.toolArgs["cache"] != "true" {
		t.Fatalf("tool cache = %v, want %q", parsed.toolArgs["cache"], "true")
	}
}

func TestParseToolCallArgsRejectsConflictingCacheFlags(t *testing.T) {
	if _, err := parseToolCallArgs([]string{"--cache=30s", "--no-cache"}, bytes.NewBuffer(nil), true); err == nil {
		t.Fatal("parseToolCallArgs() error = nil, want non-nil")
	}
}

func TestParseToolCallArgsGlobalToolCollisionWithToolPrefix(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{"--cache=30s", "--tool-cache=true"}, bytes.NewBuffer(nil), true)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}

	if parsed.cacheTTL == nil || *parsed.cacheTTL != 30*time.Second {
		t.Fatalf("cacheTTL = %v, want 30s", parsed.cacheTTL)
	}
	if parsed.toolArgs["cache"] != "true" {
		t.Fatalf("tool cache arg = %v, want %q", parsed.toolArgs["cache"], "true")
	}
}

func TestParseToolCallArgsSeparatorTreatsGlobalNamesAsToolArgs(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{"--", "--cache=true", "--help"}, bytes.NewBuffer(nil), true)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}
	if parsed.cacheTTL != nil {
		t.Fatalf("cacheTTL = %v, want nil", parsed.cacheTTL)
	}
	if parsed.help {
		t.Fatal("help = true, want false")
	}
	if parsed.toolArgs["cache"] != "true" {
		t.Fatalf("tool cache arg = %v, want %q", parsed.toolArgs["cache"], "true")
	}
	if parsed.toolArgs["help"] != true {
		t.Fatalf("tool help arg = %v, want true", parsed.toolArgs["help"])
	}
}

func TestParseToolCallArgsSeparatorNormalizesToolPrefixFlags(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{"--", "--tool-cache=true"}, bytes.NewBuffer(nil), true)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}

	if parsed.toolArgs["cache"] != "true" {
		t.Fatalf("tool cache arg = %v, want %q", parsed.toolArgs["cache"], "true")
	}
	if _, ok := parsed.toolArgs["tool-cache"]; ok {
		t.Fatalf("tool-cache key should not be present: %#v", parsed.toolArgs)
	}
}

func TestParseToolCallArgsDoesNotCoerceExplicitFlagValues(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{"--id=00123", "--enabled=false", "--score", "1.5"}, bytes.NewBuffer(nil), true)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}

	if parsed.toolArgs["id"] != "00123" {
		t.Fatalf("id = %v, want %q", parsed.toolArgs["id"], "00123")
	}
	if parsed.toolArgs["enabled"] != "false" {
		t.Fatalf("enabled = %v, want %q", parsed.toolArgs["enabled"], "false")
	}
	if parsed.toolArgs["score"] != "1.5" {
		t.Fatalf("score = %v, want %q", parsed.toolArgs["score"], "1.5")
	}
}

func TestParseToolCallArgsReadsJSONFromStdinWhenNoFlags(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{}, bytes.NewBufferString(`{"query":"mcp","page":3}`), false)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}
	if parsed.toolArgs["query"] != "mcp" {
		t.Fatalf("query = %v, want mcp", parsed.toolArgs["query"])
	}
	page, ok := parsed.toolArgs["page"].(float64)
	if !ok || page != 3 {
		t.Fatalf("page = %#v, want 3", parsed.toolArgs["page"])
	}
}

func TestParseToolCallArgsDoesNotReadStdinWhenFlagsProvided(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{"--query=mcp"}, bytes.NewBufferString(`{not-json}`), false)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}
	if parsed.toolArgs["query"] != "mcp" {
		t.Fatalf("query = %v, want mcp", parsed.toolArgs["query"])
	}
}

func TestParseToolCallArgsDoesNotReadStdinWhenOnlyGlobalFlagsProvided(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{"--verbose"}, bytes.NewBufferString(`{not-json}`), false)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}
	if len(parsed.toolArgs) != 0 {
		t.Fatalf("toolArgs = %#v, want empty", parsed.toolArgs)
	}
}

func TestParseToolCallArgsSupportsVerboseQuietAndHelp(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{"-v", "--quiet", "--help"}, bytes.NewBuffer(nil), true)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}
	if !parsed.verbose {
		t.Fatal("verbose = false, want true")
	}
	if !parsed.quiet {
		t.Fatal("quiet = false, want true")
	}
	if !parsed.help {
		t.Fatal("help = false, want true")
	}
}

func TestParseToolCallArgsParsesExplicitJSONHelpFlag(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{"--help", "--json"}, bytes.NewBuffer(nil), true)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}
	if !parsed.help {
		t.Fatal("help = false, want true")
	}
	if !parsed.helpJSON {
		t.Fatal("helpJSON = false, want true")
	}
	if len(parsed.toolArgs) != 0 {
		t.Fatalf("toolArgs = %#v, want empty", parsed.toolArgs)
	}
}

func TestParseToolCallArgsParsesJSONHelpFlagBeforeHelp(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{"--json", "--help"}, bytes.NewBuffer(nil), true)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}
	if !parsed.help {
		t.Fatal("help = false, want true")
	}
	if !parsed.helpJSON {
		t.Fatal("helpJSON = false, want true")
	}
}

func TestParseToolCallArgsRejectsJSONFlagWithoutHelp(t *testing.T) {
	if _, err := parseToolCallArgs([]string{"--json"}, bytes.NewBuffer(nil), true); err == nil {
		t.Fatal("parseToolCallArgs() error = nil, want non-nil")
	}
}

func TestParseToolCallArgsDoesNotTreatToolJSONAsJSONHelpFlag(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{"--help", "--tool-json"}, bytes.NewBuffer(nil), true)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}
	if !parsed.help {
		t.Fatal("help = false, want true")
	}
	if parsed.helpJSON {
		t.Fatal("helpJSON = true, want false")
	}
	if parsed.toolArgs["json"] != true {
		t.Fatalf("json tool arg = %#v, want true", parsed.toolArgs["json"])
	}
}

func TestParseToolCallArgsPositionalJSONDoesNotTriggerJSONHelpFlag(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{"--help", `{"json":true}`}, bytes.NewBuffer(nil), true)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}
	if !parsed.help {
		t.Fatal("help = false, want true")
	}
	if parsed.helpJSON {
		t.Fatal("helpJSON = true, want false")
	}
	if parsed.toolArgs["json"] != true {
		t.Fatalf("json tool arg = %#v, want true", parsed.toolArgs["json"])
	}
}

func TestParseToolCallArgsPreservesNoPrefixedLiteralFlagAndArrayFlags(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{"--no-dry-run", "--tag=a", "--tag=b"}, bytes.NewBuffer(nil), true)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}
	if parsed.toolArgs["no-dry-run"] != true {
		t.Fatalf("no-dry-run = %v, want true", parsed.toolArgs["no-dry-run"])
	}
	tags, ok := parsed.toolArgs["tag"].([]any)
	if !ok {
		t.Fatalf("tag = %#v, want []any", parsed.toolArgs["tag"])
	}
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Fatalf("tag values = %#v, want [a b]", tags)
	}
}

func TestParseToolCallArgsRejectsPositionalJSONMixedWithToolFlags(t *testing.T) {
	_, err := parseToolCallArgs([]string{`{"query":"mcp"}`, "--page=2"}, bytes.NewBuffer(nil), true)
	if err == nil {
		t.Fatal("parseToolCallArgs() error = nil, want non-nil")
	}
}

func TestParseToolCallArgsAllowsPositionalJSONWithGlobalFlags(t *testing.T) {
	parsed, err := parseToolCallArgs([]string{"--cache=30s", `{"query":"mcp"}`}, bytes.NewBuffer(nil), true)
	if err != nil {
		t.Fatalf("parseToolCallArgs() error = %v", err)
	}
	if parsed.cacheTTL == nil || *parsed.cacheTTL != 30*time.Second {
		t.Fatalf("cacheTTL = %v, want 30s", parsed.cacheTTL)
	}
	if parsed.toolArgs["query"] != "mcp" {
		t.Fatalf("query = %v, want mcp", parsed.toolArgs["query"])
	}
}

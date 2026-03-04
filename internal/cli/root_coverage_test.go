package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/lydakis/mcpx/internal/ipc"
)

func TestListServersFromDaemonSendError(t *testing.T) {
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()

	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	code := listServersFromDaemon(stubDaemonClient{
		sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			if req.Type != "list_servers" {
				return nil, errors.New("unexpected request type")
			}
			return nil, errors.New("daemon unavailable")
		},
	}, "/tmp", outputModeText, false)

	if code != ipc.ExitInternal {
		t.Fatalf("listServersFromDaemon(send error) = %d, want %d", code, ipc.ExitInternal)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); !strings.Contains(got, "daemon unavailable") {
		t.Fatalf("stderr = %q, want daemon error", got)
	}
}

func TestListServersFromDaemonJSONNameProjection(t *testing.T) {
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()

	payload, err := json.Marshal([]serverListEntry{
		{Name: "beta", Origin: config.NewServerOrigin(config.ServerOriginKindMCPXConfig, "/tmp/mcpx.toml")},
		{Name: "alpha", Origin: config.NewServerOrigin(config.ServerOriginKindCodexApps, "")},
		{Name: "beta", Origin: config.NewServerOrigin(config.ServerOriginKindFallbackCustom, "")},
	})
	if err != nil {
		t.Fatalf("json.Marshal(payload): %v", err)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	code := listServersFromDaemon(stubDaemonClient{
		sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			if req.Type != "list_servers" {
				return nil, errors.New("unexpected request type")
			}
			return &ipc.Response{ExitCode: ipc.ExitOK, Content: payload}, nil
		},
	}, "/tmp", outputModeJSON, false)

	if code != ipc.ExitOK {
		t.Fatalf("listServersFromDaemon(json names) = %d, want %d", code, ipc.ExitOK)
	}
	var got []string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(stdout): %v", err)
	}
	want := []string{"alpha", "beta"}
	if len(got) != len(want) {
		t.Fatalf("server names len = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("server names[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestListServersFromDaemonVerboseTextUsesDashForBlankSource(t *testing.T) {
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()

	payload := []byte(`[{"name":"alpha","origin":{"kind":"   "}}]`)
	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	code := listServersFromDaemon(stubDaemonClient{
		sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			return &ipc.Response{ExitCode: ipc.ExitOK, Content: payload}, nil
		},
	}, "/tmp", outputModeText, true)

	if code != ipc.ExitOK {
		t.Fatalf("listServersFromDaemon(verbose text) = %d, want %d", code, ipc.ExitOK)
	}
	fields := strings.Fields(out.String())
	if len(fields) < 2 {
		t.Fatalf("stdout fields = %v, want at least [name source] (raw=%q)", fields, out.String())
	}
	if fields[0] != "alpha" || fields[1] != "-" {
		t.Fatalf("stdout fields = %v, want [alpha - ...]", fields)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestListServersFromDaemonJSONWriteErrorReturnsInternal(t *testing.T) {
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()

	var errOut bytes.Buffer
	rootStdout = errWriter{err: errors.New("disk full")}
	rootStderr = &errOut

	code := listServersFromDaemon(stubDaemonClient{
		sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			return &ipc.Response{ExitCode: ipc.ExitOK, Content: []byte(`[{"name":"alpha"}]`)}, nil
		},
	}, "/tmp", outputModeJSON, false)

	if code != ipc.ExitInternal {
		t.Fatalf("listServersFromDaemon(json write error) = %d, want %d", code, ipc.ExitInternal)
	}
	if got := errOut.String(); !strings.Contains(got, "writing json output") {
		t.Fatalf("stderr = %q, want json-write context", got)
	}
}

func TestListServersFromDaemonNoEntriesPrintsSetupHint(t *testing.T) {
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()

	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	code := listServersFromDaemon(stubDaemonClient{
		sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			return &ipc.Response{ExitCode: ipc.ExitOK, Content: []byte(`[]`)}, nil
		},
	}, "/tmp", outputModeText, false)

	if code != ipc.ExitOK {
		t.Fatalf("listServersFromDaemon(no entries) = %d, want %d", code, ipc.ExitOK)
	}
	if got := out.String(); !strings.Contains(got, "No MCP servers configured.") {
		t.Fatalf("stdout = %q, want empty-config message", got)
	}
	if got := out.String(); !strings.Contains(got, config.ExampleConfigPath()) {
		t.Fatalf("stdout = %q, want config path hint", got)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestListToolsCanonicalizesExplicitSourceBeforeRequest(t *testing.T) {
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()

	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	source := "./relative/server.json"
	cwd := t.TempDir()
	wantServerKey := filepath.Clean(filepath.Join(cwd, source))

	code := listTools(stubDaemonClient{
		sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			if req.Type != "list_tools" {
				return nil, errors.New("unexpected request type")
			}
			if req.Server != wantServerKey {
				return nil, errors.New("unexpected canonicalized server key")
			}
			if !req.Verbose {
				return nil, errors.New("expected verbose request")
			}
			return &ipc.Response{ExitCode: ipc.ExitOK, Content: []byte(`[]`)}, nil
		},
	}, source, cwd, true, outputModeText, true)

	if code != ipc.ExitOK {
		t.Fatalf("listTools(canonicalized source) = %d, want %d", code, ipc.ExitOK)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestListToolsReturnsInternalOnInvalidPayload(t *testing.T) {
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()

	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	code := listTools(stubDaemonClient{
		sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			return &ipc.Response{ExitCode: ipc.ExitOK, Content: []byte(`not-json`)}, nil
		},
	}, "github", "/tmp", false, outputModeText, false)

	if code != ipc.ExitInternal {
		t.Fatalf("listTools(invalid payload) = %d, want %d", code, ipc.ExitInternal)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); !strings.Contains(got, "invalid daemon response for tool list") {
		t.Fatalf("stderr = %q, want decode error", got)
	}
}

func TestListToolsJSONWriteErrorReturnsInternal(t *testing.T) {
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()

	var errOut bytes.Buffer
	rootStdout = errWriter{err: errors.New("write failed")}
	rootStderr = &errOut

	code := listTools(stubDaemonClient{
		sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			return &ipc.Response{ExitCode: ipc.ExitOK, Content: []byte(`[{"name":"ping"}]`)}, nil
		},
	}, "github", "/tmp", false, outputModeJSON, false)

	if code != ipc.ExitInternal {
		t.Fatalf("listTools(json write error) = %d, want %d", code, ipc.ExitInternal)
	}
	if got := errOut.String(); !strings.Contains(got, "writing json output") {
		t.Fatalf("stderr = %q, want json-write context", got)
	}
}

func TestListToolsPropagatesDaemonExitAndStderr(t *testing.T) {
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()

	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	code := listTools(stubDaemonClient{
		sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			return &ipc.Response{ExitCode: ipc.ExitUsageErr, Stderr: "tool listing failed"}, nil
		},
	}, "github", "/tmp", false, outputModeText, false)

	if code != ipc.ExitUsageErr {
		t.Fatalf("listTools(daemon error) = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); got != "tool listing failed\n" {
		t.Fatalf("stderr = %q, want %q", got, "tool listing failed\\n")
	}
}

func TestCallToolParseErrorReturnsUsage(t *testing.T) {
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()

	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	code := callTool(stubDaemonClient{
		sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			return nil, errors.New("unexpected daemon call")
		},
	}, "github", "search", []string{"--json"}, "/tmp", false)

	if code != ipc.ExitUsageErr {
		t.Fatalf("callTool(parse error) = %d, want %d", code, ipc.ExitUsageErr)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); !strings.Contains(got, "--json is only supported with --help") {
		t.Fatalf("stderr = %q, want parse error", got)
	}
}

func TestCallToolQuietSuppressesSendError(t *testing.T) {
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()

	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	code := callTool(stubDaemonClient{
		sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			if req.Type != "call_tool" {
				return nil, errors.New("unexpected request type")
			}
			return nil, errors.New("daemon timeout")
		},
	}, "github", "search", []string{"--quiet", "{}"}, "/tmp", false)

	if code != ipc.ExitInternal {
		t.Fatalf("callTool(quiet send error) = %d, want %d", code, ipc.ExitInternal)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty in quiet mode", errOut.String())
	}
}

func TestCallToolHelpRoutesToSchemaRequest(t *testing.T) {
	oldOut := rootStdout
	oldErr := rootStderr
	defer func() {
		rootStdout = oldOut
		rootStderr = oldErr
	}()

	var out bytes.Buffer
	var errOut bytes.Buffer
	rootStdout = &out
	rootStderr = &errOut

	var requests int
	code := callTool(stubDaemonClient{
		sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			requests++
			if req.Type != "tool_schema" {
				return nil, errors.New("expected tool_schema request")
			}
			if req.Server != "github" || req.Tool != "search" {
				return nil, errors.New("unexpected server/tool")
			}
			return &ipc.Response{
				ExitCode: ipc.ExitOK,
				Content:  []byte(`{"name":"search","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}`),
			}, nil
		},
	}, "github", "search", []string{"--help"}, "/tmp", false)

	if code != ipc.ExitOK {
		t.Fatalf("callTool(--help) = %d, want %d", code, ipc.ExitOK)
	}
	if requests != 1 {
		t.Fatalf("daemon requests = %d, want 1", requests)
	}
	if got := out.String(); !strings.Contains(got, "Usage: mcpx github search [FLAGS]") {
		t.Fatalf("stdout = %q, want rendered help usage", got)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestWriteToolResponseQuietNonOKSuppressesContent(t *testing.T) {
	resp := &ipc.Response{
		ExitCode: ipc.ExitUsageErr,
		Content:  []byte("bad input"),
	}
	var out bytes.Buffer
	var errOut bytes.Buffer

	writeToolResponse(resp, true, &out, &errOut)

	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}
}

func TestWriteToolResponseNonQuietNonOKWritesToStderr(t *testing.T) {
	resp := &ipc.Response{
		ExitCode: ipc.ExitUsageErr,
		Content:  []byte("bad input"),
	}
	var out bytes.Buffer
	var errOut bytes.Buffer

	writeToolResponse(resp, false, &out, &errOut)

	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", out.String())
	}
	if got := errOut.String(); got != "bad input" {
		t.Fatalf("stderr = %q, want %q", got, "bad input")
	}
}

func TestWriteCallResponseQuietSkipsStderrButWritesSuccessContent(t *testing.T) {
	resp := &ipc.Response{
		ExitCode: ipc.ExitOK,
		Stderr:   "warning",
		Content:  []byte("ok\n"),
	}
	var out bytes.Buffer
	var errOut bytes.Buffer

	writeCallResponse(resp, true, &out, &errOut)

	if got := out.String(); got != "ok\n" {
		t.Fatalf("stdout = %q, want %q", got, "ok\\n")
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty in quiet mode", errOut.String())
	}
}

func TestSendServerRequestWithEphemeralFallbackPassesNilRequest(t *testing.T) {
	var sawNil bool
	resp, err := sendServerRequestWithEphemeralFallback(stubDaemonClient{
		sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			sawNil = req == nil
			return &ipc.Response{ExitCode: ipc.ExitOK}, nil
		},
	}, nil, false)
	if err != nil {
		t.Fatalf("sendServerRequestWithEphemeralFallback(nil req) error = %v", err)
	}
	if !sawNil {
		t.Fatal("daemon request was non-nil, want nil passthrough")
	}
	if resp == nil || resp.ExitCode != ipc.ExitOK {
		t.Fatalf("resp = %#v, want ExitOK response", resp)
	}
}

func TestSendServerRequestWithEphemeralFallbackDoesNotRetryWhenEphemeralPresent(t *testing.T) {
	var calls int
	resp, err := sendServerRequestWithEphemeralFallback(stubDaemonClient{
		sendFn: func(req *ipc.Request) (*ipc.Response, error) {
			calls++
			if req.Ephemeral == nil {
				return nil, errors.New("missing ephemeral request payload")
			}
			return &ipc.Response{
				ExitCode:  ipc.ExitUsageErr,
				ErrorCode: ipc.ErrorCodeUnknownServer,
			}, nil
		},
	}, &ipc.Request{
		Type:      "call_tool",
		Server:    "missing",
		Ephemeral: &ipc.EphemeralServer{Server: config.ServerConfig{Command: "echo"}},
	}, false)
	if err != nil {
		t.Fatalf("sendServerRequestWithEphemeralFallback(ephemeral) error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("daemon calls = %d, want 1", calls)
	}
	if resp == nil || resp.ExitCode != ipc.ExitUsageErr {
		t.Fatalf("resp = %#v, want usage error response", resp)
	}
}

package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPromptForInputBuildsTypedElicitationResponse(t *testing.T) {
	payload := []byte(`{"resultType":"input_required","inputRequests":{"confirm":{"method":"elicitation/create","params":{"message":"Deploy?","requestedSchema":{"type":"object"}}}},"requestState":"opaque"}`)
	var stderr bytes.Buffer
	responses, state, err := promptForInput(payload, strings.NewReader(`{"approved":true}`+"\n"), &stderr)
	if err != nil {
		t.Fatalf("promptForInput() error = %v", err)
	}
	if state != "opaque" {
		t.Fatalf("state = %q, want opaque", state)
	}
	var decoded mcp.InputResponseMap
	if err := json.Unmarshal(responses, &decoded); err != nil {
		t.Fatalf("unmarshal responses: %v", err)
	}
	got, ok := decoded["confirm"].(*mcp.ElicitResult)
	if !ok || got.Action != "accept" || got.Content["approved"] != true {
		t.Fatalf("response = %#v", decoded["confirm"])
	}
	if !strings.Contains(stderr.String(), "Deploy?") {
		t.Fatalf("prompt = %q", stderr.String())
	}
}

func TestPromptForInputRejectsSampling(t *testing.T) {
	payload := []byte(`{"resultType":"input_required","inputRequests":{"sample":{"method":"sampling/createMessage","params":{"messages":[],"maxTokens":1}}},"requestState":"opaque"}`)
	_, _, err := promptForInput(payload, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "use --input-responses") {
		t.Fatalf("promptForInput() error = %v", err)
	}
}

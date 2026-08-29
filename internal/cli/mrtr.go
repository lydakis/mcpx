package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxInteractiveRounds = 10

// promptForInput fulfills only elicitation requests. Sampling and roots would
// turn mcpx into an agent host, so they remain explicit machine-driven retries.
func promptForInput(payload []byte, stdin io.Reader, stderr io.Writer) (json.RawMessage, string, error) {
	var result mcp.CallToolResult
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, "", fmt.Errorf("decoding input-required result: %w", err)
	}
	if !result.NeedsInput() {
		return nil, "", fmt.Errorf("server response is not input-required")
	}
	if result.RequestState == "" {
		return nil, "", fmt.Errorf("server omitted requestState for input-required result")
	}
	if len(result.InputRequests) == 0 {
		return nil, "", fmt.Errorf("server requested a retry without user input; retry with --request-state and --input-responses '{}'")
	}

	reader := bufio.NewReader(stdin)
	responses := make(mcp.InputResponseMap, len(result.InputRequests))
	ids := make([]string, 0, len(result.InputRequests))
	for id := range result.InputRequests {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		request, ok := result.InputRequests[id].(*mcp.ElicitParams)
		if !ok {
			return nil, "", fmt.Errorf("interactive mode cannot fulfill %T request %q; use --input-responses", result.InputRequests[id], id)
		}
		fmt.Fprintf(stderr, "%s\n", strings.TrimSpace(request.Message))
		if request.URL != "" {
			fmt.Fprintf(stderr, "Open: %s\n", request.URL)
		}
		if request.RequestedSchema != nil {
			schema, _ := json.Marshal(request.RequestedSchema)
			fmt.Fprintf(stderr, "Schema: %s\n", schema)
		}
		fmt.Fprint(stderr, "Response JSON, accept, decline, or cancel: ")
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, "", fmt.Errorf("reading interactive response: %w", err)
		}
		line = strings.TrimSpace(line)
		switch strings.ToLower(line) {
		case "accept":
			responses[id] = &mcp.ElicitResult{Action: "accept", Content: map[string]any{}}
		case "decline", "cancel":
			responses[id] = &mcp.ElicitResult{Action: strings.ToLower(line)}
		default:
			var content map[string]any
			if err := json.Unmarshal([]byte(line), &content); err != nil {
				return nil, "", fmt.Errorf("response for %q must be a JSON object or accept, decline, or cancel: %w", id, err)
			}
			responses[id] = &mcp.ElicitResult{Action: "accept", Content: content}
		}
	}

	data, err := json.Marshal(responses)
	if err != nil {
		return nil, "", fmt.Errorf("encoding interactive responses: %w", err)
	}
	return data, result.RequestState, nil
}

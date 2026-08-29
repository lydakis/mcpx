package mcppool

// testToolSchema keeps test fixtures concise while the production adapter
// accepts arbitrary JSON Schema values from the official SDK.
type testToolSchema struct {
	Type                 string         `json:"type,omitempty"`
	Properties           map[string]any `json:"properties,omitempty"`
	Required             []string       `json:"required,omitempty"`
	AdditionalProperties any            `json:"additionalProperties,omitempty"`
}

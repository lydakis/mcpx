package mcppool

import (
	"context"
	"fmt"

	"github.com/lydakis/mcpx/internal/config"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

func connectHTTP(ctx context.Context, scfg config.ServerConfig) (*connection, error) {
	var opts []transport.StreamableHTTPCOption
	if len(scfg.Headers) > 0 {
		opts = append(opts, transport.WithHTTPHeaders(scfg.Headers))
	}

	c, err := mcpclient.NewStreamableHttpClient(scfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("creating HTTP client: %w", err)
	}

	if err := c.Start(ctx); err != nil {
		c.Close()
		return nil, fmt.Errorf("starting HTTP client: %w", err)
	}

	if _, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: "2025-11-25",
			ClientInfo: mcp.Implementation{
				Name:    "mcpx",
				Version: "0.1.0",
			},
			Capabilities: mcp.ClientCapabilities{},
		},
	}); err != nil {
		c.Close()
		return nil, fmt.Errorf("initializing: %w", err)
	}

	return &connection{
		listTools: func(ctx context.Context) ([]mcp.Tool, error) {
			result, err := c.ListTools(ctx, mcp.ListToolsRequest{})
			if err != nil {
				return nil, err
			}
			return result.Tools, nil
		},
		callTool: func(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
			return c.CallTool(ctx, mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name:      name,
					Arguments: args,
				},
			})
		},
		close: func() error {
			return c.Close()
		},
	}, nil
}

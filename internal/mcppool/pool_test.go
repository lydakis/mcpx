package mcppool

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestListToolsErrorInvalidatesConnection(t *testing.T) {
	var closed bool
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return nil, errors.New("boom")
		},
		close: func() error {
			closed = true
			return nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.ListTools(context.Background(), "github"); err == nil {
		t.Fatal("ListTools() error = nil, want non-nil")
	}

	p.mu.Lock()
	_, ok := p.conns["github"]
	p.mu.Unlock()
	if ok {
		t.Fatal("connection was not evicted after list error")
	}
	if !closed {
		t.Fatal("connection close was not called after list error")
	}
}

func TestCallToolErrorInvalidatesConnection(t *testing.T) {
	var closed bool
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{{Name: "search"}}, nil
		},
		callTool: func(context.Context, string, map[string]any) (*mcp.CallToolResult, error) {
			return nil, errors.New("boom")
		},
		close: func() error {
			closed = true
			return nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.CallTool(context.Background(), "github", "search", []byte(`{"q":"mcp"}`)); err == nil {
		t.Fatal("CallTool() error = nil, want non-nil")
	}

	p.mu.Lock()
	_, ok := p.conns["github"]
	p.mu.Unlock()
	if ok {
		t.Fatal("connection was not evicted after call error")
	}
	if !closed {
		t.Fatal("connection close was not called after call error")
	}
}

func TestToolInfoByNameMatchesOnlyExactNames(t *testing.T) {
	listCalls := 0
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			listCalls++
			return []mcp.Tool{
				{Name: "search_repositories"},
				{Name: "list-issues"},
			}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.ToolInfoByName(context.Background(), "github", "search_repositories"); err != nil {
		t.Fatalf("ToolInfoByName(exact snake) error = %v", err)
	}

	if _, err := p.ToolInfoByName(context.Background(), "github", "list-issues"); err != nil {
		t.Fatalf("ToolInfoByName(exact kebab) error = %v", err)
	}

	if _, err := p.ToolInfoByName(context.Background(), "github", "search-repositories"); err == nil {
		t.Fatal("ToolInfoByName(alias kebab) error = nil, want non-nil")
	}

	if _, err := p.ToolInfoByName(context.Background(), "github", "list_issues"); err == nil {
		t.Fatal("ToolInfoByName(alias snake) error = nil, want non-nil")
	}

	if _, err := p.ToolInfoByName(context.Background(), "github", "missing-tool"); err == nil {
		t.Fatal("ToolInfoByName(missing) error = nil, want non-nil")
	}

	if listCalls < 1 {
		t.Fatalf("listTools calls = %d, want >= 1", listCalls)
	}
}

func TestToolInfoByNameRefreshesCachedIndexOnMiss(t *testing.T) {
	listCalls := 0
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			listCalls++
			if listCalls == 1 {
				return []mcp.Tool{{Name: "search_repositories"}}, nil
			}
			return []mcp.Tool{
				{Name: "search_repositories"},
				{Name: "list-issues"},
			}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.ToolInfoByName(context.Background(), "github", "search_repositories"); err != nil {
		t.Fatalf("ToolInfoByName(initial) error = %v", err)
	}

	if _, err := p.ToolInfoByName(context.Background(), "github", "list-issues"); err != nil {
		t.Fatalf("ToolInfoByName(refreshed) error = %v", err)
	}

	if listCalls != 2 {
		t.Fatalf("listTools calls = %d, want 2", listCalls)
	}
}

func TestCallToolInvokesExactToolName(t *testing.T) {
	var calledWith string
	listCalls := 0
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			listCalls++
			return []mcp.Tool{
				{Name: "search_repositories"},
			}, nil
		},
		callTool: func(_ context.Context, name string, _ map[string]any) (*mcp.CallToolResult, error) {
			calledWith = name
			return &mcp.CallToolResult{}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.CallTool(context.Background(), "github", "search_repositories", []byte(`{"q":"mcp"}`)); err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	if calledWith != "search_repositories" {
		t.Fatalf("CallTool() invoked %q, want %q", calledWith, "search_repositories")
	}
	if listCalls != 1 {
		t.Fatalf("listTools calls = %d, want 1", listCalls)
	}
}

func TestToolInfoByNameReusesCachedToolIndex(t *testing.T) {
	listCalls := 0
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			listCalls++
			return []mcp.Tool{{Name: "search"}}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.ToolInfoByName(context.Background(), "github", "search"); err != nil {
		t.Fatalf("ToolInfoByName(first) error = %v", err)
	}
	if _, err := p.ToolInfoByName(context.Background(), "github", "search"); err != nil {
		t.Fatalf("ToolInfoByName(second) error = %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("listTools calls = %d, want 1", listCalls)
	}
}

func TestToolInfoByNameConcurrentColdCacheListsOnce(t *testing.T) {
	var listCalls int32
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			atomic.AddInt32(&listCalls, 1)
			time.Sleep(30 * time.Millisecond)
			return []mcp.Tool{{Name: "search"}}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	const workers = 6
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := p.ToolInfoByName(context.Background(), "github", "search")
			errs <- err
		}()
	}

	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("ToolInfoByName() error = %v", err)
		}
	}

	if got := atomic.LoadInt32(&listCalls); got != 1 {
		t.Fatalf("listTools calls = %d, want 1", got)
	}
}

func TestCallToolWithInfoSkipsToolListing(t *testing.T) {
	var calledWith string
	var calledArgs map[string]any

	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			t.Fatal("listTools should not be called by CallToolWithInfo")
			return nil, nil
		},
		callTool: func(_ context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
			calledWith = name
			calledArgs = args
			return &mcp.CallToolResult{}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	info := &ToolInfo{
		Name:        "search_repositories",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
	}

	if _, err := p.CallToolWithInfo(context.Background(), "github", info, []byte(`{"query":"mcp"}`)); err != nil {
		t.Fatalf("CallToolWithInfo() error = %v", err)
	}

	if calledWith != "search_repositories" {
		t.Fatalf("CallToolWithInfo() invoked %q, want %q", calledWith, "search_repositories")
	}
	if calledArgs["query"] != "mcp" {
		t.Fatalf("CallToolWithInfo() args = %#v, want query=mcp", calledArgs)
	}
}

func TestCallToolReusesCachedToolInfoAcrossCalls(t *testing.T) {
	listCalls := 0
	callCount := 0
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			listCalls++
			return []mcp.Tool{
				{Name: "search_repositories"},
			}, nil
		},
		callTool: func(_ context.Context, _ string, _ map[string]any) (*mcp.CallToolResult, error) {
			callCount++
			return &mcp.CallToolResult{}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.CallTool(context.Background(), "github", "search_repositories", []byte(`{"q":"mcp"}`)); err != nil {
		t.Fatalf("CallTool(first) error = %v", err)
	}
	if _, err := p.CallTool(context.Background(), "github", "search_repositories", []byte(`{"q":"mcp-go"}`)); err != nil {
		t.Fatalf("CallTool(second) error = %v", err)
	}

	if listCalls != 1 {
		t.Fatalf("listTools calls = %d, want 1", listCalls)
	}
	if callCount != 2 {
		t.Fatalf("callTool calls = %d, want 2", callCount)
	}
}

func TestCallToolWithInfoSerializesRequestsPerConnection(t *testing.T) {
	var inFlight int32
	var maxInFlight int32

	conn := &connection{
		callTool: func(_ context.Context, _ string, _ map[string]any) (*mcp.CallToolResult, error) {
			n := atomic.AddInt32(&inFlight, 1)
			for {
				currentMax := atomic.LoadInt32(&maxInFlight)
				if n <= currentMax {
					break
				}
				if atomic.CompareAndSwapInt32(&maxInFlight, currentMax, n) {
					break
				}
			}
			time.Sleep(40 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
			return &mcp.CallToolResult{}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	const workers = 4
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := p.CallToolWithInfo(context.Background(), "github", &ToolInfo{Name: "search"}, nil)
			errs <- err
		}()
	}

	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("CallToolWithInfo() error = %v", err)
		}
	}

	if got := atomic.LoadInt32(&maxInFlight); got != 1 {
		t.Fatalf("max concurrent callTool invocations = %d, want 1", got)
	}
}

func TestResetReturnsWithoutWaitingForBusyConnection(t *testing.T) {
	closed := make(chan struct{}, 1)
	conn := &connection{
		close: func() error {
			closed <- struct{}{}
			return nil
		},
	}

	// Simulate an in-flight request holding the per-connection lock.
	conn.reqMu.Lock()

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {Command: "echo"}}},
		conns: map[string]*connection{"github": conn},
	}

	done := make(chan struct{})
	go func() {
		p.Reset(&config.Config{Servers: map[string]config.ServerConfig{}})
		close(done)
	}()

	select {
	case <-done:
		// Reset should return quickly even while reqMu is held.
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Reset() blocked waiting for busy connection")
	}

	// Once in-flight work is released, deferred close should run.
	conn.reqMu.Unlock()

	select {
	case <-closed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("connection close did not run after busy lock was released")
	}
}

func TestListToolsIncludesOutputSchema(t *testing.T) {
	conn := &connection{
		listTools: func(context.Context) ([]mcp.Tool, error) {
			return []mcp.Tool{
				{
					Name: "search",
					InputSchema: mcp.ToolInputSchema{
						Type: "object",
					},
					OutputSchema: mcp.ToolOutputSchema{
						Type: "object",
						Properties: map[string]any{
							"items": map[string]any{"type": "array"},
						},
					},
				},
			}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	tools, err := p.ListTools(context.Background(), "github")
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}
	if len(tools[0].OutputSchema) == 0 {
		t.Fatal("OutputSchema is empty, want declared schema")
	}

	var parsed map[string]any
	if err := json.Unmarshal(tools[0].OutputSchema, &parsed); err != nil {
		t.Fatalf("unmarshal output schema: %v", err)
	}
	if parsed["type"] != "object" {
		t.Fatalf("output type = %v, want object", parsed["type"])
	}
}

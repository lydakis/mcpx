package mcppool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lydakis/mcpx/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestListToolsErrorInvalidatesConnection(t *testing.T) {
	closed := make(chan struct{}, 1)
	conn := &connection{
		listTools: func(context.Context) ([]*mcp.Tool, error) {
			return nil, errors.New("boom")
		},
		close: func() error {
			closed <- struct{}{}
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
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("connection close was not called after list error")
	}
}

func TestListAllToolPagesFollowsCursorsAndUsesShortestTTL(t *testing.T) {
	var cursors []string
	listPage := func(_ context.Context, params *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
		cursor := ""
		if params != nil {
			cursor = params.Cursor
		}
		cursors = append(cursors, cursor)
		switch cursor {
		case "":
			return &mcp.ListToolsResult{
				Cacheable:  mcp.Cacheable{TTLMs: 60_000},
				NextCursor: "page-2",
				Tools:      []*mcp.Tool{{Name: "first"}},
			}, nil
		case "page-2":
			return &mcp.ListToolsResult{
				Cacheable: mcp.Cacheable{TTLMs: 5_000},
				Tools:     []*mcp.Tool{{Name: "second"}},
			}, nil
		default:
			return nil, errors.New("unexpected cursor")
		}
	}

	receivedAt := time.Unix(1_000, 0)
	tools, cachePolicy, err := listAllToolPagesWithClock(context.Background(), listPage, func() time.Time { return receivedAt })
	if err != nil {
		t.Fatalf("listAllToolPages() error = %v", err)
	}
	if got, want := []string{tools[0].Name, tools[1].Name}, []string{"first", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tool names = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(cursors, []string{"", "page-2"}) {
		t.Fatalf("cursors = %#v, want first and second page", cursors)
	}
	if want := receivedAt.Add(5 * time.Second); !cachePolicy.deadline.Equal(want) {
		t.Fatalf("catalog deadline = %s, want %s", cachePolicy.deadline, want)
	}
}

func TestListAllToolPagesMeasuresTTLFromEachPageReceipt(t *testing.T) {
	current := time.Unix(1_000, 0)
	page := 0
	listPage := func(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
		page++
		if page == 1 {
			return &mcp.ListToolsResult{
				Cacheable:  mcp.Cacheable{TTLMs: 1_000},
				NextCursor: "page-2",
			}, nil
		}
		current = current.Add(5 * time.Second)
		return &mcp.ListToolsResult{Cacheable: mcp.Cacheable{TTLMs: 10_000}}, nil
	}

	_, cachePolicy, err := listAllToolPagesWithClock(context.Background(), listPage, func() time.Time { return current })
	if err != nil {
		t.Fatalf("listAllToolPagesWithClock() error = %v", err)
	}
	if want := time.Unix(1_001, 0); !cachePolicy.deadline.Equal(want) {
		t.Fatalf("catalog deadline = %s, want first-page deadline %s", cachePolicy.deadline, want)
	}
	if cachePolicy.deadline.After(current) {
		t.Fatalf("catalog deadline = %s, want expired at assembly time %s", cachePolicy.deadline, current)
	}
	conn := &connection{indexed: true}
	setToolCacheDeadline(conn, cachePolicy.deadline, cachePolicy.cacheable, true)
	conn.toolMu.RLock()
	valid := toolCacheValidLocked(conn, current)
	conn.toolMu.RUnlock()
	if valid {
		t.Fatal("assembled catalog remained cache-valid after its first page expired")
	}
}

func TestListAllToolPagesRejectsCursorCycle(t *testing.T) {
	listPage := func(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
		return &mcp.ListToolsResult{NextCursor: "same"}, nil
	}
	_, _, err := listAllToolPages(context.Background(), listPage)
	if err == nil || !strings.Contains(err.Error(), "repeated cursor") {
		t.Fatalf("listAllToolPages() error = %v, want repeated cursor", err)
	}
}

func TestListAllToolPagesRejectsUnboundedUniqueCursors(t *testing.T) {
	calls := 0
	listPage := func(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
		calls++
		return &mcp.ListToolsResult{NextCursor: fmt.Sprintf("page-%d", calls)}, nil
	}
	_, _, err := listAllToolPages(context.Background(), listPage)
	if err == nil || !strings.Contains(err.Error(), "page limit") {
		t.Fatalf("listAllToolPages() error = %v, want page limit", err)
	}
	if calls != maxToolCatalogPages {
		t.Fatalf("list page calls = %d, want %d", calls, maxToolCatalogPages)
	}
}

func TestListAllToolPagesRejectsOversizedCatalog(t *testing.T) {
	listPage := func(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
		return &mcp.ListToolsResult{Tools: make([]*mcp.Tool, maxToolCatalogItems+1)}, nil
	}
	_, _, err := listAllToolPages(context.Background(), listPage)
	if err == nil || !strings.Contains(err.Error(), "tool limit") {
		t.Fatalf("listAllToolPages() error = %v, want tool limit", err)
	}
}

func TestCallToolErrorInvalidatesConnection(t *testing.T) {
	closed := make(chan struct{}, 1)
	conn := &connection{
		listTools: func(context.Context) ([]*mcp.Tool, error) {
			return []*mcp.Tool{{Name: "search"}}, nil
		},
		callTool: func(context.Context, string, map[string]any) (*mcp.CallToolResult, error) {
			return nil, errors.New("boom")
		},
		close: func() error {
			closed <- struct{}{}
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
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("connection close was not called after call error")
	}
}

func TestToolInfoByNameMatchesOnlyExactNames(t *testing.T) {
	listCalls := 0
	conn := &connection{
		listTools: func(context.Context) ([]*mcp.Tool, error) {
			listCalls++
			return []*mcp.Tool{
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
		listTools: func(context.Context) ([]*mcp.Tool, error) {
			listCalls++
			if listCalls == 1 {
				return []*mcp.Tool{{Name: "search_repositories"}}, nil
			}
			return []*mcp.Tool{
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
		listTools: func(context.Context) ([]*mcp.Tool, error) {
			listCalls++
			return []*mcp.Tool{
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
		listTools: func(context.Context) ([]*mcp.Tool, error) {
			listCalls++
			return []*mcp.Tool{{Name: "search"}}, nil
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

func TestListToolsReusesCachedToolIndex(t *testing.T) {
	listCalls := 0
	conn := &connection{
		listTools: func(context.Context) ([]*mcp.Tool, error) {
			listCalls++
			return []*mcp.Tool{
				{
					Name:        "search",
					InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
					OutputSchema: json.RawMessage(
						`{"type":"object","properties":{"items":{"type":"array"}}}`,
					),
				},
				{Name: "list"},
			}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	first, err := p.ListTools(context.Background(), "github")
	if err != nil {
		t.Fatalf("ListTools(first) error = %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("len(first) = %d, want 2", len(first))
	}

	// Ensure callers cannot mutate cached state via returned slices.
	first[0].Name = "mutated"
	first[0].InputSchema[0] = 'X'
	first[0].OutputSchema[0] = 'Y'

	second, err := p.ListTools(context.Background(), "github")
	if err != nil {
		t.Fatalf("ListTools(second) error = %v", err)
	}
	if len(second) != 2 {
		t.Fatalf("len(second) = %d, want 2", len(second))
	}
	if second[0].Name == "mutated" {
		t.Fatal("ListTools(second) returned mutated cached tool data")
	}
	if len(second[0].InputSchema) == 0 || second[0].InputSchema[0] == 'X' {
		t.Fatal("ListTools(second) returned mutated cached input schema")
	}
	if len(second[0].OutputSchema) == 0 || second[0].OutputSchema[0] == 'Y' {
		t.Fatal("ListTools(second) returned mutated cached output schema")
	}
	if listCalls != 1 {
		t.Fatalf("listTools calls = %d, want 1", listCalls)
	}
}

func TestListToolsDoesNotOutliveModernNoCacheHint(t *testing.T) {
	listCalls := 0
	conn := &connection{}
	conn.listTools = func(context.Context) ([]*mcp.Tool, error) {
		listCalls++
		setToolCachePolicy(conn, 0, true)
		return []*mcp.Tool{{Name: "search"}}, nil
	}
	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	for range 2 {
		if _, err := p.ListTools(context.Background(), "github"); err != nil {
			t.Fatalf("ListTools() error = %v", err)
		}
	}
	if listCalls != 2 {
		t.Fatalf("listTools calls = %d, want 2", listCalls)
	}
}

func TestListToolsExpiresAtProtocolTTL(t *testing.T) {
	listCalls := 0
	conn := &connection{}
	conn.listTools = func(context.Context) ([]*mcp.Tool, error) {
		listCalls++
		setToolCachePolicy(conn, time.Hour, true)
		return []*mcp.Tool{{Name: "search"}}, nil
	}
	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	if _, err := p.ListTools(context.Background(), "github"); err != nil {
		t.Fatalf("ListTools(first) error = %v", err)
	}
	conn.toolMu.Lock()
	conn.toolCacheUntil = time.Now().Add(-time.Second)
	conn.toolMu.Unlock()
	if _, err := p.ListTools(context.Background(), "github"); err != nil {
		t.Fatalf("ListTools(expired) error = %v", err)
	}
	if listCalls != 2 {
		t.Fatalf("listTools calls = %d, want 2", listCalls)
	}
}

func TestListToolsFiltersUnnamedToolsConsistently(t *testing.T) {
	conn := &connection{
		listTools: func(context.Context) ([]*mcp.Tool, error) {
			return []*mcp.Tool{
				{Name: ""},
				{Name: "search"},
			}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	first, err := p.ListTools(context.Background(), "github")
	if err != nil {
		t.Fatalf("ListTools(first) error = %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("len(first) = %d, want 1", len(first))
	}
	if first[0].Name != "search" {
		t.Fatalf("first tool = %q, want %q", first[0].Name, "search")
	}

	second, err := p.ListTools(context.Background(), "github")
	if err != nil {
		t.Fatalf("ListTools(second) error = %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("len(second) = %d, want 1", len(second))
	}
	if second[0].Name != "search" {
		t.Fatalf("second tool = %q, want %q", second[0].Name, "search")
	}
}

func TestListToolsConcurrentColdCacheListsOnce(t *testing.T) {
	var listCalls int32
	conn := &connection{
		listTools: func(context.Context) ([]*mcp.Tool, error) {
			atomic.AddInt32(&listCalls, 1)
			time.Sleep(30 * time.Millisecond)
			return []*mcp.Tool{{Name: "search"}}, nil
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
			_, err := p.ListTools(context.Background(), "github")
			errs <- err
		}()
	}

	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("ListTools() error = %v", err)
		}
	}

	if got := atomic.LoadInt32(&listCalls); got != 1 {
		t.Fatalf("listTools calls = %d, want 1", got)
	}
}

func TestToolInfoByNameConcurrentColdCacheListsOnce(t *testing.T) {
	var listCalls int32
	conn := &connection{
		listTools: func(context.Context) ([]*mcp.Tool, error) {
			atomic.AddInt32(&listCalls, 1)
			time.Sleep(30 * time.Millisecond)
			return []*mcp.Tool{{Name: "search"}}, nil
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
		listTools: func(context.Context) ([]*mcp.Tool, error) {
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

func TestCallToolWithInfoAndInputPassesTypedRetry(t *testing.T) {
	var gotState string
	var gotResponse mcp.InputResponse
	conn := &connection{
		callToolWithInput: func(_ context.Context, _ string, _ map[string]any, responses mcp.InputResponseMap, state string) (*mcp.CallToolResult, error) {
			gotState = state
			gotResponse = responses["confirm"]
			return &mcp.CallToolResult{}, nil
		},
	}
	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}
	info := &ToolInfo{Name: "deploy", InputSchema: json.RawMessage(`{"type":"object"}`)}
	responses := json.RawMessage(`{"confirm":{"action":"accept","content":{"ok":true}}}`)
	if _, err := p.CallToolWithInfoAndInput(context.Background(), "github", info, nil, responses, "opaque"); err != nil {
		t.Fatalf("CallToolWithInfoAndInput() error = %v", err)
	}
	if gotState != "opaque" {
		t.Fatalf("state = %q, want opaque", gotState)
	}
	if _, ok := gotResponse.(*mcp.ElicitResult); !ok {
		t.Fatalf("response type = %T, want *mcp.ElicitResult", gotResponse)
	}
}

func TestCallToolWithInfoAndInputMarksMalformedResponsesInvalid(t *testing.T) {
	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{},
	}
	info := &ToolInfo{Name: "deploy", InputSchema: json.RawMessage(`{"type":"object"}`)}
	_, err := p.CallToolWithInfoAndInput(
		context.Background(), "github", info, nil,
		json.RawMessage(`{"confirm":{"content":{"ok":true}}}`), "opaque",
	)
	if err == nil || !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("CallToolWithInfoAndInput() error = %v, want ErrInvalidParams", err)
	}
}

func TestCallToolReusesCachedToolInfoAcrossCalls(t *testing.T) {
	listCalls := 0
	callCount := 0
	conn := &connection{
		listTools: func(context.Context) ([]*mcp.Tool, error) {
			listCalls++
			return []*mcp.Tool{
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

func TestCloseAllWaitsForCurrentAndAlreadyRetiringConnections(t *testing.T) {
	retiredStarted := make(chan struct{})
	retiredRelease := make(chan struct{})
	retired := &connection{close: func() error {
		close(retiredStarted)
		<-retiredRelease
		return nil
	}}
	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"retired": {Command: "echo"}}},
		conns: map[string]*connection{"retired": retired},
	}

	p.Reset(&config.Config{Servers: map[string]config.ServerConfig{"current": {Command: "echo"}}})
	select {
	case <-retiredStarted:
	case <-time.After(time.Second):
		close(retiredRelease)
		t.Fatal("retired connection cleanup did not start")
	}

	currentStarted := make(chan struct{})
	currentRelease := make(chan struct{})
	current := &connection{close: func() error {
		close(currentStarted)
		<-currentRelease
		return nil
	}}
	p.mu.Lock()
	p.conns["current"] = current
	p.mu.Unlock()

	done := make(chan struct{})
	go func() {
		p.CloseAll()
		close(done)
	}()
	select {
	case <-currentStarted:
	case <-time.After(time.Second):
		close(currentRelease)
		close(retiredRelease)
		t.Fatal("current connection cleanup did not start")
	}

	returnedEarly := false
	select {
	case <-done:
		returnedEarly = true
	case <-time.After(50 * time.Millisecond):
	}

	close(currentRelease)
	if !returnedEarly {
		select {
		case <-done:
			returnedEarly = true
		case <-time.After(50 * time.Millisecond):
		}
	}
	close(retiredRelease)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("CloseAll() did not return after every connection drained")
	}
	if returnedEarly {
		t.Fatal("CloseAll() returned before every current and retired connection drained")
	}
}

func TestBeginCredentialTransitionDrainsBusyConnectionAndFencesReconnects(t *testing.T) {
	closed := make(chan struct{}, 1)
	conn := &connection{close: func() error {
		closed <- struct{}{}
		return nil
	}}
	conn.reqMu.Lock()

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"remote": {Command: "echo"}}},
		conns: map[string]*connection{"remote": conn},
	}

	done := make(chan error, 1)
	go func() {
		done <- p.BeginCredentialTransition([]string{"remote"}, "transition-token")
	}()

	deadline := time.Now().Add(time.Second)
	for {
		p.mu.Lock()
		fenced := p.transitions["remote"] == "transition-token"
		p.mu.Unlock()
		if fenced {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("credential transition did not fence the server")
		}
		time.Sleep(time.Millisecond)
	}

	if _, err := p.getOrCreate(context.Background(), "remote"); err == nil || !strings.Contains(err.Error(), "credential transition") {
		t.Fatalf("getOrCreate() error = %v, want credential-transition fence", err)
	}
	select {
	case err := <-done:
		t.Fatalf("BeginCredentialTransition() returned before request drain: %v", err)
	default:
	}

	conn.reqMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("BeginCredentialTransition() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("BeginCredentialTransition() did not return after request drain")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("credential transition did not close the drained connection")
	}

	p.EndCredentialTransition("transition-token")
	p.mu.Lock()
	_, fenced := p.transitions["remote"]
	p.mu.Unlock()
	if fenced {
		t.Fatal("EndCredentialTransition() left the server fenced")
	}
}

func TestBeginCredentialTransitionWaitsForBusyConnectionRetiredByReset(t *testing.T) {
	conn := &connection{close: func() error { return nil }}
	conn.reqMu.Lock()
	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"remote": {Command: "echo"}}},
		conns: map[string]*connection{"remote": conn},
	}

	resetDone := make(chan struct{})
	go func() {
		p.Reset(&config.Config{Servers: map[string]config.ServerConfig{"remote": {Command: "echo"}}})
		close(resetDone)
	}()
	select {
	case <-resetDone:
	case <-time.After(time.Second):
		t.Fatal("Reset() blocked on the busy connection")
	}

	transitionDone := make(chan error, 1)
	go func() {
		transitionDone <- p.BeginCredentialTransition([]string{"remote"}, "transition-token")
	}()
	select {
	case err := <-transitionDone:
		t.Fatalf("BeginCredentialTransition() bypassed retired writer: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	conn.reqMu.Unlock()
	select {
	case err := <-transitionDone:
		if err != nil {
			t.Fatalf("BeginCredentialTransition() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("BeginCredentialTransition() did not finish after retired writer drained")
	}
}

func TestBeginCredentialTransitionWaitsForRemovedAliasRetiredByReset(t *testing.T) {
	conn := &connection{close: func() error { return nil }}
	conn.reqMu.Lock()
	p := &Pool{
		cfg: &config.Config{Servers: map[string]config.ServerConfig{
			"old-alias": {URL: "https://example.com/mcp", OAuth: true},
		}},
		conns: map[string]*connection{"old-alias": conn},
	}
	p.Reset(&config.Config{Servers: map[string]config.ServerConfig{
		"new-alias": {URL: "https://example.com/mcp", OAuth: true},
	}})

	done := make(chan error, 1)
	go func() {
		done <- p.BeginCredentialTransition([]string{"new-alias"}, "transition-token")
	}()
	select {
	case err := <-done:
		t.Fatalf("BeginCredentialTransition() bypassed removed alias writer: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	conn.reqMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("BeginCredentialTransition() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("BeginCredentialTransition() did not finish after removed alias drained")
	}
}

func TestBeginCredentialTransitionDoesNotWaitForUnrelatedRetiree(t *testing.T) {
	conn := &connection{close: func() error { return nil }}
	conn.reqMu.Lock()
	p := &Pool{
		cfg: &config.Config{Servers: map[string]config.ServerConfig{
			"unrelated": {Command: "echo"},
		}},
		conns: map[string]*connection{"unrelated": conn},
	}
	p.Reset(&config.Config{Servers: map[string]config.ServerConfig{
		"remote": {URL: "https://example.com/mcp", OAuth: true},
	}})

	done := make(chan error, 1)
	go func() {
		done <- p.BeginCredentialTransition([]string{"remote"}, "transition-token")
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("BeginCredentialTransition() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		conn.reqMu.Unlock()
		<-done
		t.Fatal("BeginCredentialTransition() waited for an unrelated retired connection")
	}

	conn.reqMu.Unlock()
}

func TestCredentialTransitionFencesRenamedCredentialAlias(t *testing.T) {
	p := New(&config.Config{Servers: map[string]config.ServerConfig{
		"primary": {URL: "https://example.com/mcp", OAuth: true},
	}})
	if err := p.BeginCredentialTransition([]string{"primary"}, "transition-token"); err != nil {
		t.Fatalf("BeginCredentialTransition() error = %v", err)
	}
	defer p.EndCredentialTransition("transition-token")
	p.SetConfig(&config.Config{Servers: map[string]config.ServerConfig{
		"renamed": {URL: "https://example.com/mcp", OAuth: true},
	}})

	if !p.CredentialTransitionPending("renamed") {
		t.Fatal("renamed alias sharing the credential was not fenced")
	}
	if _, err := p.getOrCreate(context.Background(), "renamed"); err == nil || !strings.Contains(err.Error(), "credential transition") {
		t.Fatalf("getOrCreate(renamed) error = %v, want credential-transition fence", err)
	}
}

func TestConnectionClosePrecedesCredentialWriteQuiescence(t *testing.T) {
	for _, test := range []struct {
		name  string
		close func(*connection) error
	}{
		{name: "nonblocking", close: func(conn *connection) error { closeConnection(conn); return nil }},
		{name: "blocking", close: closeConnectionAndWait},
	} {
		t.Run(test.name, func(t *testing.T) {
			var order []string
			conn := &connection{
				close: func() error {
					order = append(order, "close")
					return nil
				},
				quiesce: func() { order = append(order, "quiesce") },
			}
			if err := test.close(conn); err != nil {
				t.Fatalf("close error = %v", err)
			}
			if got, want := strings.Join(order, ","), "close,quiesce"; got != want {
				t.Fatalf("close order = %q, want %q", got, want)
			}
		})
	}
}

func TestSetConfigSwapsConfigWithoutDroppingConnections(t *testing.T) {
	conn := &connection{}
	initial := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {Command: "echo", Args: []string{"old"}},
		},
	}
	updated := &config.Config{
		Servers: map[string]config.ServerConfig{
			"github": {Command: "echo", Args: []string{"new"}},
		},
	}

	p := &Pool{
		cfg:   initial,
		conns: map[string]*connection{"github": conn},
	}

	p.SetConfig(updated)

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cfg != updated {
		t.Fatal("pool config pointer was not updated")
	}
	if got := p.conns["github"]; got != conn {
		t.Fatal("existing connection was dropped when swapping config")
	}
}

func TestListToolsIncludesOutputSchema(t *testing.T) {
	conn := &connection{
		listTools: func(context.Context) ([]*mcp.Tool, error) {
			return []*mcp.Tool{
				{
					Name: "search",
					InputSchema: testToolSchema{
						Type: "object",
					},
					OutputSchema: testToolSchema{
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

func TestToolSchemaReturnsInputSchemaForNamedTool(t *testing.T) {
	t.Parallel()

	listCalls := 0
	conn := &connection{
		listTools: func(context.Context) ([]*mcp.Tool, error) {
			listCalls++
			return []*mcp.Tool{
				{
					Name:        "search",
					InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
				},
			}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	got, err := p.ToolSchema(context.Background(), "github", "search")
	if err != nil {
		t.Fatalf("ToolSchema() error = %v", err)
	}
	if string(got) != `{"type":"object","properties":{"q":{"type":"string"}}}` {
		t.Fatalf("ToolSchema() = %s, want raw input schema", got)
	}
	if listCalls != 1 {
		t.Fatalf("listTools calls = %d, want 1", listCalls)
	}
}

func TestToolSchemaReturnsLookupErrorForMissingTool(t *testing.T) {
	t.Parallel()

	conn := &connection{
		listTools: func(context.Context) ([]*mcp.Tool, error) {
			return []*mcp.Tool{{Name: "search"}}, nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	_, err := p.ToolSchema(context.Background(), "github", "missing")
	if err == nil {
		t.Fatal("ToolSchema() error = nil, want non-nil")
	}
}

func TestCloseRemovesConnectionAndInvokesCloseHook(t *testing.T) {
	t.Parallel()

	closed := make(chan struct{}, 1)
	conn := &connection{
		close: func() error {
			closed <- struct{}{}
			return nil
		},
	}

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{"github": conn},
	}

	p.Close("github")

	p.mu.Lock()
	_, exists := p.conns["github"]
	p.mu.Unlock()
	if exists {
		t.Fatal("Close() did not remove connection from pool")
	}

	select {
	case <-closed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close() did not invoke connection close hook")
	}
}

func TestCloseIsNoopForUnknownServer(t *testing.T) {
	t.Parallel()

	p := &Pool{
		cfg:   &config.Config{Servers: map[string]config.ServerConfig{"github": {}}},
		conns: map[string]*connection{},
	}

	p.Close("missing")
}

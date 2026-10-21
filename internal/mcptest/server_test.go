package mcptest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connect(t *testing.T) (*mcp.ClientSession, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(Handler())
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, srv
}

func text(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if len(r.Content) != 1 {
		t.Fatalf("content = %d items, want 1", len(r.Content))
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want *mcp.TextContent", r.Content[0])
	}
	return tc.Text
}

func TestToolsOverHTTP(t *testing.T) {
	cs, srv := connect(t)
	ctx := context.Background()

	if got := cs.InitializeResult().ServerInfo; got.Name != "things-test" || got.Version != "0.1.0" {
		t.Errorf("server info = %+v", got)
	}

	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(list.Tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(list.Tools))
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range list.Tools {
		byName[tool.Name] = tool
	}

	lt := byName["list_things"]
	if lt == nil {
		t.Fatal("list_things missing")
	}
	if lt.Description != "List the things." || lt.Annotations == nil ||
		!lt.Annotations.ReadOnlyHint || lt.Annotations.Title != "List things" ||
		lt.Annotations.OpenWorldHint == nil || *lt.Annotations.OpenWorldHint {
		t.Errorf("list_things = %q %+v", lt.Description, lt.Annotations)
	}

	dt := byName["delete_thing"]
	if dt == nil {
		t.Fatal("delete_thing missing")
	}
	a := dt.Annotations
	if dt.Description != "Delete a thing by name." || a == nil ||
		a.DestructiveHint == nil || !*a.DestructiveHint || a.ReadOnlyHint ||
		a.IdempotentHint || a.Title != "Delete thing" ||
		a.OpenWorldHint == nil || *a.OpenWorldHint {
		t.Errorf("delete_thing = %q %+v", dt.Description, a)
	}
	schema, ok := dt.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("delete_thing input schema = %T", dt.InputSchema)
	}
	props, _ := schema["properties"].(map[string]any)
	name, _ := props["name"].(map[string]any)
	if name["type"] != "string" || name["description"] != "Name of the thing to delete" {
		t.Errorf("name property = %v", name)
	}
	if req, _ := schema["required"].([]any); len(req) != 1 || req[0] != "name" {
		t.Errorf("required = %v, want [name]", schema["required"])
	}

	// list_things returns structured content.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_things"})
	if err != nil || res.IsError {
		t.Fatalf("list_things: %v %+v", err, res)
	}
	sc, _ := res.StructuredContent.(map[string]any)
	things, _ := sc["things"].([]any)
	if len(things) != 3 || things[0] != "apple" || things[1] != "banana" || things[2] != "cherry" {
		t.Errorf("structured content = %v", res.StructuredContent)
	}

	// delete_thing success.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "delete_thing", Arguments: map[string]any{"name": "apple"}})
	if err != nil || res.IsError {
		t.Fatalf("delete apple: %v %+v", err, res)
	}
	if got := text(t, res); got != "deleted apple" {
		t.Errorf("delete apple text = %q", got)
	}

	// Unknown name: error result.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "delete_thing", Arguments: map[string]any{"name": "kiwi"}})
	if err != nil || !res.IsError {
		t.Fatalf("delete kiwi: err=%v res=%+v, want error result", err, res)
	}
	if got := text(t, res); got != "no thing named kiwi" {
		t.Errorf("delete kiwi text = %q", got)
	}

	// Missing name: error result.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "delete_thing", Arguments: map[string]any{}})
	if err == nil && (res == nil || !res.IsError) {
		t.Errorf("delete without name: want an error, got %+v", res)
	}

	// Stateless: a GET (standalone stream) is not allowed.
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", resp.StatusCode)
	}
	if resp.Header.Get("Mcp-Session-Id") != "" {
		t.Error("stateless server set a session id")
	}
}

func TestDeleteMissingNameMessage(t *testing.T) {
	cs, _ := connect(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "delete_thing", Arguments: map[string]any{}})
	var msg string
	switch {
	case err != nil:
		msg = err.Error()
	case res.IsError:
		msg = text(t, res)
	default:
		t.Fatal("no error")
	}
	if !strings.Contains(msg, "name") {
		t.Errorf("error %q does not mention name", msg)
	}
}

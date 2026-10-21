// Package mcptest is a throwaway MCP server with two tools, used to see how
// Claude and ChatGPT handle OAuth and tool approvals. Nothing persists.
package mcptest

import (
	"context"
	"net/http"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var things = []string{"apple", "banana", "cherry"}

type listInput struct{}

type listOutput struct {
	Things []string `json:"things"`
}

type deleteInput struct {
	Name string `json:"name" jsonschema:"Name of the thing to delete"`
}

func ptr[T any](v T) *T { return &v }

// NewServer returns a server with list_things and delete_thing.
func NewServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "things-test", Version: "0.1.0"}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_things",
		Description: "List the things.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "List things",
			ReadOnlyHint:  true,
			OpenWorldHint: ptr(false),
		},
	}, func(context.Context, *mcp.CallToolRequest, listInput) (*mcp.CallToolResult, listOutput, error) {
		return nil, listOutput{Things: slices.Clone(things)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_thing",
		Description: "Delete a thing by name.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Delete thing",
			DestructiveHint: ptr(true),
			IdempotentHint:  false,
			OpenWorldHint:   ptr(false),
		},
	}, func(_ context.Context, _ *mcp.CallToolRequest, in deleteInput) (*mcp.CallToolResult, any, error) {
		if !slices.Contains(things, in.Name) {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "no thing named " + in.Name}},
			}, nil, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "deleted " + in.Name}},
		}, nil, nil
	})

	return s
}

// Handler serves the server over stateless streamable HTTP (one server per
// request, JSON responses), which suits Lambda behind API Gateway.
func Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return NewServer() },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)
}

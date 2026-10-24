package curated

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ptr[T any](v T) *T { return &v }

// NewServer returns Server B: six tools over the audiofile API.
func NewServer(api API, logger *log.Logger) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "audiofile-curated", Version: "0.1.0"}, nil)
	t := &tools{api: api}
	addTool(s, logger, findTracksTool, t.findTracks)
	addTool(s, logger, buildMixTool, t.buildWorkoutMix)
	addTool(s, logger, listPlaylistsTool, t.listPlaylists)
	addTool(s, logger, showPlaylistTool, t.showPlaylist)
	addTool(s, logger, editPlaylistTool, t.editPlaylist)
	addTool(s, logger, deletePlaylistTool, t.deletePlaylist)
	return s
}

// Handler serves the server over stateless streamable HTTP (one server per
// request, JSON responses), which suits Lambda behind API Gateway.
func Handler(api API, logger *log.Logger) http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return NewServer(api, logger) },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)
}

var uuidRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// logCall writes one line per tool call: the name and the input JSON with
// ids shortened to 8 characters.
func logCall(l *log.Logger, name string, in any) {
	if l == nil {
		return
	}
	b, err := json.Marshal(in)
	if err != nil {
		b = []byte("{}")
	}
	short := uuidRe.ReplaceAllStringFunc(string(b), func(id string) string { return id[:8] })
	l.Printf("tool %q %s", name, short)
}

// addTool registers a tool with explicit schemas (see schemaFor) and logs
// each call.
func addTool[In, Out any](s *mcp.Server, l *log.Logger, tool *mcp.Tool, h func(context.Context, In) (*mcp.CallToolResult, Out, error)) {
	// Copy first: the tool definitions are shared package-level values and
	// NewServer runs once per HTTP request.
	tt := *tool
	tt.InputSchema = schemaFor[In]()
	tt.OutputSchema = schemaFor[Out]()
	mcp.AddTool(s, &tt, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		logCall(l, tt.Name, in)
		res, out, err := h(ctx, in)
		if res != nil && res.IsError {
			// The SDK checks Out against the output schema even for errors, and an
			// empty Out can't pass. Returning a Go error makes it an IsError result.
			var msg string
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					msg += tc.Text
				}
			}
			var zero Out
			return nil, zero, errors.New(msg)
		}
		return res, out, err
	})
}

// schemaFor infers a schema for T and collapses every multi-type
// ["null","x"] (from slices and pointers) to a single type, since some
// clients can't read type arrays.
func schemaFor[T any]() *jsonschema.Schema {
	sc, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	flatten(sc)
	return sc
}

func flatten(sc *jsonschema.Schema) {
	if sc == nil {
		return
	}
	if len(sc.Types) > 0 {
		for _, ty := range sc.Types {
			if ty != "null" {
				sc.Type = ty
				break
			}
		}
		sc.Types = nil
	}
	for _, p := range sc.Properties {
		flatten(p)
	}
	flatten(sc.Items)
	flatten(sc.AdditionalProperties)
	for _, a := range sc.AnyOf {
		flatten(a)
	}
}

// toolError is a tool result the model can read and act on.
func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

func textResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

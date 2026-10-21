// Command mcptest is the throwaway test MCP server Lambda. The mcp-host API
// Gateway sends REST (v1) proxy events. The event carries the bearer token, so
// nothing here logs the request or its headers.
package main

import (
	"net/http"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/marianina8/audiofile-api/internal/mcptest"
)

func main() {
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcptest.Handler())
	lambda.Start(httpadapter.New(mux).ProxyWithContext)
}

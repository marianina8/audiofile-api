// Command curated is Server B: the curated audiofile MCP server Lambda. The
// mcp-host API Gateway sends REST (v1) proxy events. The event carries the
// caller's bearer token, which is never forwarded or logged; the server calls
// the API with its own cached machine token. Set PORT to run it locally.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/marianina8/audiofile-api/internal/curated"
)

func main() {
	// 10s per call; tool calls as a whole stop 2s before the Lambda deadline.
	hc := &http.Client{Timeout: 10 * time.Second}
	secret := curated.SSMSecret("/audiofile/machine-client-secret")
	if s := os.Getenv("MACHINE_CLIENT_SECRET"); s != "" {
		secret = func(context.Context) (string, error) { return s, nil }
	}
	tok := curated.NewMachineToken(os.Getenv("TOKEN_URL"), os.Getenv("MACHINE_CLIENT_ID"), secret, hc)
	client := curated.NewClient(os.Getenv("API_BASE_URL"), tok, hc)
	logger := log.New(os.Stdout, "", 0)

	mux := http.NewServeMux()
	mux.Handle("/mcp", curated.Handler(client, logger))

	if port := os.Getenv("PORT"); port != "" {
		logger.Printf("curated MCP server listening on :%s/mcp", port)
		log.Fatal(http.ListenAndServe(":"+port, mux))
	}
	lambda.Start(httpadapter.New(mux).ProxyWithContext)
}

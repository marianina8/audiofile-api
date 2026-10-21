// Command mcpauthorizer is the mcp-host TOKEN authorizer Lambda (spec §8 B).
package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/marianina8/audiofile-api/internal/authorizer"
	"github.com/marianina8/audiofile-api/internal/jwtverify"
)

func main() {
	issuer := strings.TrimRight(os.Getenv("ISSUER_URL"), "/")
	if issuer == "" {
		log.Fatal("environment variable ISSUER_URL is required")
	}
	// One verifier per container, so the JWKS cache survives across invocations.
	v := jwtverify.New(issuer, issuer+"/.well-known/jwks.json")
	lambda.Start(func(ctx context.Context, req events.APIGatewayCustomAuthorizerRequest) (events.APIGatewayCustomAuthorizerResponse, error) {
		return authorizer.Handle(ctx, v, req)
	})
}

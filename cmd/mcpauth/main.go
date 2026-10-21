// Command mcpauth is the OAuth bridge Lambda (spec §8 A, stack mcp-auth).
package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/marianina8/audiofile-api/internal/jwtverify"
	"github.com/marianina8/audiofile-api/internal/oauth"
)

func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("environment variable %s is required", name)
	}
	return v
}

func main() {
	awsCfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("load AWS config: %v", err)
	}
	cognitoIssuer := strings.TrimRight(mustEnv("COGNITO_ISSUER"), "/")
	cfg := oauth.Config{
		Issuer:            mustEnv("ISSUER_URL"),
		CognitoDomain:     mustEnv("COGNITO_DOMAIN"),
		CognitoClientID:   mustEnv("COGNITO_CLIENT_ID"),
		ChatGPTCallbackID: os.Getenv("CHATGPT_CALLBACK_ID"),
		Resources:         oauth.SSMResources(ssm.NewFromConfig(awsCfg), mustEnv("RESOURCES_SSM_PREFIX"), nil),
		Verifier:          jwtverify.New(cognitoIssuer, cognitoIssuer+"/.well-known/jwks.json"),
	}
	st := oauth.NewDynamoState(dynamodb.NewFromConfig(awsCfg), mustEnv("STATE_TABLE"))
	lambda.Start(httpadapter.NewV2(oauth.New(cfg, st)).ProxyWithContext)
}

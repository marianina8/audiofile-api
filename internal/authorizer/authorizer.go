// Package authorizer is the mcp-host API Gateway TOKEN authorizer (spec §8 B).
// It keeps all auth at the gateway so MCP server code needs none.
package authorizer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/aws/aws-lambda-go/events"

	"github.com/marianina8/audiofile-api/internal/jwtverify"
)

const (
	// Stage is the only REST API stage mcp-host deploys.
	Stage = "v1"
	// RequiredGroup is always required; the bridge's refresh path relies on it.
	RequiredGroup = "mcp-users"
	mcpPath       = "/mcp"
)

var (
	// errUnauthorized is the exact message API Gateway maps to a 401.
	errUnauthorized = errors.New("Unauthorized")
	// ErrUnavailable is returned when the JWKS cannot be loaded. Any error
	// other than "Unauthorized" makes API Gateway answer 500, so clients
	// retry instead of re-running OAuth. Errors are never cached.
	ErrUnavailable = errors.New("authorizer temporarily unavailable")
)

// target is the parsed part of a method ARN:
// arn:{partition}:execute-api:{region}:{account}:{apiId}/{stage}/{method}/{path...}
type target struct {
	partition, region, account, apiID, stage, path string
}

func parseMethodArn(arn string) (target, error) {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) != 6 || parts[0] != "arn" || parts[2] != "execute-api" ||
		parts[1] == "" || parts[3] == "" || parts[4] == "" {
		return target{}, errors.New("not an execute-api method ARN")
	}
	seg := strings.SplitN(parts[5], "/", 4)
	if len(seg) != 4 || seg[0] == "" || seg[1] == "" || seg[2] == "" {
		return target{}, errors.New("method ARN missing api, stage, method or path")
	}
	t := target{partition: parts[1], region: parts[3], account: parts[4], apiID: seg[0], stage: seg[1], path: "/" + seg[3]}
	if t.stage != Stage {
		return target{}, fmt.Errorf("unexpected stage %q", t.stage)
	}
	if t.path != mcpPath && !strings.HasPrefix(t.path, mcpPath+"/") {
		return target{}, errors.New("path is not under /mcp")
	}
	return t, nil
}

func (t target) audience() string {
	return fmt.Sprintf("https://%s.execute-api.%s.amazonaws.com/%s%s", t.apiID, t.region, t.stage, mcpPath)
}

// Audience returns the MCP URL a token must be bound to for methodArn:
// https://{apiId}.execute-api.{region}.amazonaws.com/{stage}/mcp. It fails
// unless the stage is v1 and the path is /mcp or below it.
func Audience(methodArn string) (string, error) {
	t, err := parseMethodArn(methodArn)
	if err != nil {
		return "", err
	}
	return t.audience(), nil
}

func policy(principal, effect string, resources []string) events.APIGatewayCustomAuthorizerResponse {
	return events.APIGatewayCustomAuthorizerResponse{
		PrincipalID: principal,
		PolicyDocument: events.APIGatewayCustomAuthorizerPolicy{
			Version: "2012-10-17",
			Statement: []events.IAMPolicyStatement{{
				Action:   []string{"execute-api:Invoke"},
				Effect:   effect,
				Resource: resources,
			}},
		},
	}
}

// outcome logs the decision and its reason class; never the token.
func outcome(result, reason string) {
	log.Printf("authorizer outcome=%s reason=%s", result, reason)
}

// Handle verifies the bearer token for the method ARN's MCP URL. A missing,
// malformed or invalid token, or one bound to another audience, returns the
// "Unauthorized" error (401). A token without the mcp-users group, or a
// method ARN outside v1 /mcp, gets a Deny policy (403). A JWKS outage
// returns ErrUnavailable (500). A valid token gets Allow on this API's
// /mcp routes, with sub, client_id and scope as context for the backend.
func Handle(ctx context.Context, v *jwtverify.Verifier, req events.APIGatewayCustomAuthorizerRequest) (events.APIGatewayCustomAuthorizerResponse, error) {
	t, err := parseMethodArn(req.MethodArn)
	if err != nil {
		outcome("deny", "method_arn")
		return policy("unknown", "Deny", []string{req.MethodArn}), nil
	}

	// Same rule as the gateway's IdentityValidationExpression ^[Bb]earer ...
	scheme, token, ok := strings.Cut(req.AuthorizationToken, " ")
	if !ok || (scheme != "Bearer" && scheme != "bearer") || strings.TrimSpace(token) == "" {
		outcome("unauthorized", "missing_token")
		return events.APIGatewayCustomAuthorizerResponse{}, errUnauthorized
	}

	claims, err := v.Verify(ctx, strings.TrimSpace(token), t.audience(), RequiredGroup)
	switch {
	case err == nil:
	case errors.Is(err, jwtverify.ErrGroup):
		outcome("deny", "group")
		return policy("unknown", "Deny", []string{req.MethodArn}), nil
	case errors.Is(err, jwtverify.ErrUnavailable):
		outcome("error", "jwks_unavailable")
		return events.APIGatewayCustomAuthorizerResponse{}, ErrUnavailable
	case errors.Is(err, jwtverify.ErrAudience):
		outcome("unauthorized", "audience")
		return events.APIGatewayCustomAuthorizerResponse{}, errUnauthorized
	default:
		outcome("unauthorized", "invalid_token")
		return events.APIGatewayCustomAuthorizerResponse{}, errUnauthorized
	}

	base := fmt.Sprintf("arn:%s:execute-api:%s:%s:%s/%s/*%s", t.partition, t.region, t.account, t.apiID, t.stage, mcpPath)
	resp := policy(claims.Subject, "Allow", []string{base, base + "/*"})
	resp.Context = map[string]any{
		"sub":       claims.Subject,
		"client_id": claims.ClientID,
		"scope":     claims.Scope,
	}
	outcome("allow", "valid")
	return resp, nil
}

// Package oauth is the OAuth 2.1 authorization server ("bridge") that sits in
// front of Amazon Cognito so remote MCP servers can be connected from Claude
// and ChatGPT. See spec §8 A.
//
// Logging rule for this package: log only kinds and outcomes (for example
// "client=dcr outcome=registered"). Never log codes, tokens, secrets, PKCE
// verifiers, or request bodies.
package oauth

import (
	"context"
	"net/http"
	"time"

	"github.com/marianina8/audiofile-api/internal/jwtverify"
)

// Config configures the bridge.
type Config struct {
	// Issuer is the bridge's base URL, with no trailing slash (one is trimmed).
	// cmd/mcpauth must set it explicitly (ISSUER_URL). When empty, the issuer
	// is derived from the request as https://<Host>; that fallback exists for
	// local tests only, because the Host header is client-controlled outside
	// API Gateway.
	Issuer string
	// CognitoDomain is the user pool's managed login base URL, for example
	// https://<prefix>.auth.<region>.amazoncognito.com (no trailing slash; one
	// is trimmed).
	CognitoDomain string
	// CognitoClientID is the bridge's public (no secret) Cognito app client.
	// Its only callback URL is <Issuer>/callback.
	CognitoClientID string
	// ChatGPTCallbackID enables https://chatgpt.com/connector/oauth/{id} as a
	// redirect URI. Empty disables it.
	ChatGPTCallbackID string
	// Resources lists the registered MCP server URLs (allowed `resource`
	// values). See SSMResources.
	Resources func(ctx context.Context) ([]string, error)
	// Verifier checks the Cognito access token after the code exchange
	// (and after a refresh that names a resource). *jwtverify.Verifier
	// satisfies it.
	Verifier TokenVerifier
	// HTTPClient is used for outbound calls (CIMD documents and Cognito). Nil
	// means a default client. Its Timeout and CheckRedirect are overridden.
	HTTPClient *http.Client
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// TokenVerifier verifies a Cognito access token for an audience and group.
// Errors wrap jwtverify.ErrInvalid, ErrAudience, ErrGroup or ErrUnavailable.
type TokenVerifier interface {
	Verify(ctx context.Context, token, audience, requiredGroup string) (jwtverify.Claims, error)
}

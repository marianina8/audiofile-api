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
)

// Config configures the bridge.
type Config struct {
	// Issuer is the bridge's base URL, with no trailing slash (one is trimmed).
	// cmd/mcpauth must set it explicitly (ISSUER_URL). When empty, the issuer
	// is derived from the request as https://<Host>; that fallback exists for
	// local tests only, because the Host header is client-controlled outside
	// API Gateway.
	Issuer string
	// CognitoDomain is the user pool's managed login domain (used by T22).
	CognitoDomain string
	// CognitoClientID is the bridge's confidential Cognito app client (T22).
	CognitoClientID string
	// ChatGPTCallbackID enables https://chatgpt.com/connector/oauth/{id} as a
	// redirect URI. Empty disables it.
	ChatGPTCallbackID string
	// Resources lists the registered MCP server URLs (allowed `resource`
	// values; used by T22).
	Resources func(ctx context.Context) ([]string, error)
	// HTTPClient is used for outbound fetches (CIMD documents). Nil means a
	// default client. Its Timeout and CheckRedirect are overridden for CIMD.
	HTTPClient *http.Client
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

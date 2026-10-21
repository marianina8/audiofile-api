package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	upstreamTimeout  = 5 * time.Second
	upstreamMaxBytes = 64 << 10
)

// errUpstreamInvalidGrant means Cognito answered the token request with
// invalid_grant (expired, revoked or unknown code or refresh token).
var errUpstreamInvalidGrant = errors.New("cognito: invalid_grant")

// cognitoTokens is Cognito's token response. The id_token is ignored and
// never returned to MCP clients.
type cognitoTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// upstreamHTTPClient copies base (or a default client) with a 5 s timeout and
// redirects disabled.
func upstreamHTTPClient(base *http.Client) *http.Client {
	var c http.Client
	if base != nil {
		c = *base
	}
	c.Timeout = upstreamTimeout
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

func (s *server) cognitoBase() string { return strings.TrimRight(s.cfg.CognitoDomain, "/") }

// cognitoToken posts form to Cognito's /oauth2/token as the bridge's public
// app client. Errors never include response bodies.
func (s *server) cognitoToken(ctx context.Context, form url.Values) (cognitoTokens, error) {
	form.Set("client_id", s.cfg.CognitoClientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cognitoBase()+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return cognitoTokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := s.upstream.Do(req)
	if err != nil {
		return cognitoTokens{}, fmt.Errorf("cognito token request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, upstreamMaxBytes))
	if err != nil {
		return cognitoTokens{}, fmt.Errorf("cognito token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error == "invalid_grant" {
			return cognitoTokens{}, errUpstreamInvalidGrant
		}
		return cognitoTokens{}, fmt.Errorf("cognito token status %d", resp.StatusCode)
	}
	var t cognitoTokens
	if err := json.Unmarshal(body, &t); err != nil || t.AccessToken == "" {
		return cognitoTokens{}, errors.New("cognito token response has no access_token")
	}
	return t, nil
}

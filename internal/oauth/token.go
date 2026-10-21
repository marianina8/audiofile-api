package oauth

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/url"

	"github.com/marianina8/audiofile-api/internal/jwtverify"
)

const tokenMaxBodyBytes = 16 << 10

// tokenSuccess is the RFC 6749 §5.1 response. The Cognito id_token is never
// returned.
type tokenSuccess struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// handleToken is the token endpoint for public clients (auth method none).
func (s *server) handleToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/x-www-form-urlencoded" {
		s.tokenError(w, http.StatusBadRequest, "invalid_request", "Content-Type must be application/x-www-form-urlencoded")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, tokenMaxBodyBytes)
	if err := r.ParseForm(); err != nil {
		s.tokenError(w, http.StatusBadRequest, "invalid_request", "request body is not a valid form")
		return
	}
	f := r.PostForm
	for k, v := range f {
		if len(v) > 1 {
			s.tokenError(w, http.StatusBadRequest, "invalid_request", k+" appears more than once")
			return
		}
	}
	// The bridge supports only public clients. A client that tries to
	// authenticate (ChatGPT's CIMD prefers private_key_jwt) fails loudly.
	if f.Has("client_assertion") || f.Has("client_assertion_type") || f.Has("client_secret") || r.Header.Get("Authorization") != "" {
		if r.Header.Get("Authorization") != "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="token"`)
		}
		s.tokenError(w, http.StatusUnauthorized, "invalid_client", "only token_endpoint_auth_method none is supported")
		return
	}
	switch f.Get("grant_type") {
	case "authorization_code":
		s.tokenFromCode(w, r, f)
	case "refresh_token":
		s.tokenFromRefresh(w, r, f)
	case "":
		s.tokenError(w, http.StatusBadRequest, "invalid_request", "grant_type is required")
	default:
		s.tokenError(w, http.StatusBadRequest, "unsupported_grant_type", "")
	}
}

func (s *server) tokenFromCode(w http.ResponseWriter, r *http.Request, f url.Values) {
	code, clientID, redirectURI, verifier := f.Get("code"), f.Get("client_id"), f.Get("redirect_uri"), f.Get("code_verifier")
	if code == "" || clientID == "" || redirectURI == "" || verifier == "" {
		s.tokenError(w, http.StatusBadRequest, "invalid_request", "code, client_id, redirect_uri and code_verifier are required")
		return
	}
	// Take first: any failed attempt below also burns the code.
	var rec codeRecord
	if err := s.st.Take(r.Context(), kindCode, codeKey(code), &rec); err != nil {
		if errors.Is(err, ErrNotFound) {
			s.tokenError(w, http.StatusBadRequest, "invalid_grant", "code is invalid, expired or already used")
			return
		}
		slog.Error("oauth: token", "grant", "authorization_code", "outcome", "store_failed")
		s.tokenError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	switch {
	case !equal(clientID, rec.ClientID):
		s.tokenError(w, http.StatusBadRequest, "invalid_grant", "client_id does not match the code")
		return
	case !equal(redirectURI, rec.RedirectURI):
		s.tokenError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match the code")
		return
	case !pkceMatches(verifier, rec.CodeChallenge):
		s.tokenError(w, http.StatusBadRequest, "invalid_grant", "code_verifier does not match the code_challenge")
		return
	case f.Has("resource") && !equal(f.Get("resource"), rec.Resource):
		s.tokenError(w, http.StatusBadRequest, "invalid_grant", "resource does not match the code")
		return
	}
	expiresIn := rec.AccessExpiry - s.now().Unix()
	if expiresIn <= 0 {
		s.tokenError(w, http.StatusBadRequest, "invalid_grant", "code is expired")
		return
	}
	slog.Info("oauth: token", "grant", "authorization_code", "outcome", "issued")
	writeJSON(w, http.StatusOK, tokenSuccess{
		AccessToken:  rec.AccessToken,
		TokenType:    "Bearer",
		ExpiresIn:    expiresIn,
		RefreshToken: rec.RefreshToken,
		Scope:        rec.Scope,
	})
}

// tokenFromRefresh proxies a refresh to Cognito. Cognito refresh tokens
// belong to the bridge's Cognito client, not to the MCP client, so the
// refresh is not bound to client_id (acceptable with one user). When the
// client names a resource, the new access token must be for it and the user
// must still be in mcp-users.
func (s *server) tokenFromRefresh(w http.ResponseWriter, r *http.Request, f url.Values) {
	refresh, clientID, resource := f.Get("refresh_token"), f.Get("client_id"), f.Get("resource")
	if refresh == "" || clientID == "" {
		s.tokenError(w, http.StatusBadRequest, "invalid_request", "refresh_token and client_id are required")
		return
	}
	tok, err := s.cognitoToken(r.Context(), url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
	})
	if err != nil {
		if errors.Is(err, errUpstreamInvalidGrant) {
			s.tokenError(w, http.StatusBadRequest, "invalid_grant", "refresh_token is invalid or expired")
			return
		}
		slog.Warn("oauth: token", "grant", "refresh_token", "outcome", "cognito_failed")
		s.tokenError(w, http.StatusBadGateway, "server_error", "the identity provider did not answer")
		return
	}
	var scope string
	if resource != "" {
		claims, err := s.cfg.Verifier.Verify(r.Context(), tok.AccessToken, resource, mcpGroup)
		if err != nil {
			if errors.Is(err, jwtverify.ErrUnavailable) {
				slog.Warn("oauth: token", "grant", "refresh_token", "outcome", "jwks_unavailable")
				s.tokenError(w, http.StatusBadGateway, "server_error", "could not verify the refreshed token")
				return
			}
			s.tokenError(w, http.StatusBadRequest, "invalid_grant", "refreshed token is not valid for this resource or user")
			return
		}
		scope = claims.Scope
	}
	slog.Info("oauth: token", "grant", "refresh_token", "outcome", "issued")
	writeJSON(w, http.StatusOK, tokenSuccess{
		AccessToken:  tok.AccessToken,
		TokenType:    "Bearer",
		ExpiresIn:    tok.ExpiresIn,
		RefreshToken: tok.RefreshToken,
		Scope:        scope,
	})
}

func (s *server) tokenError(w http.ResponseWriter, status int, code, description string) {
	slog.Info("oauth: token", "outcome", code)
	writeOAuthError(w, status, code, description)
}

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

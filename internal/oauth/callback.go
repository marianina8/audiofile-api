package oauth

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/marianina8/audiofile-api/internal/jwtverify"
)

const (
	kindCode = "code"
	codeTTL  = 5 * time.Minute
)

// codeRecord is a bridge authorization code, stored under codeKey(code) and
// taken (once) at /token. It holds the Cognito tokens; never log it.
type codeRecord struct {
	ClientID      string `json:"client_id"`
	RedirectURI   string `json:"redirect_uri"`
	CodeChallenge string `json:"code_challenge"`
	Resource      string `json:"resource"`
	Scope         string `json:"scope"`
	AccessToken   string `json:"access_token"`
	RefreshToken  string `json:"refresh_token,omitempty"`
	// AccessExpiry is the access token's exp, epoch seconds.
	AccessExpiry int64 `json:"access_expiry"`
}

// handleCallback finishes the Cognito sign-in: it exchanges Cognito's code,
// checks the user's token, mints a single-use bridge code and redirects to the
// app. Until the pending request is found, errors are a 400 page.
func (s *server) handleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	id := q.Get("state")
	if id == "" || len(q["state"]) > 1 {
		s.errorPage(w, http.StatusBadRequest, "callback", "missing_state", "The sign-in response is missing its state.")
		return
	}
	var p pendingAuth
	if err := s.st.Take(ctx, kindPending, id, &p); err != nil {
		if errors.Is(err, ErrNotFound) {
			s.errorPage(w, http.StatusBadRequest, "callback", "unknown_state", "This sign-in has expired or was already used. Start again from the app.")
			return
		}
		s.errorPage(w, http.StatusInternalServerError, "callback", "store_failed", "Could not finish the sign-in. Start again from the app.")
		return
	}
	fail := func(code, desc string) { s.redirectError(w, r, p.RedirectURI, p.State, code, desc) }

	if e := q.Get("error"); e != "" {
		if e == "access_denied" {
			fail("access_denied", "sign-in was denied")
			return
		}
		fail("server_error", "sign-in failed")
		return
	}
	cognitoCode := q.Get("code")
	if cognitoCode == "" {
		fail("server_error", "sign-in returned no code")
		return
	}
	tok, err := s.cognitoToken(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {cognitoCode},
		"redirect_uri":  {s.issuer(r) + "/callback"},
		"code_verifier": {p.Verifier},
	})
	if err != nil {
		slog.Warn("oauth: callback", "outcome", "cognito_exchange_failed")
		fail("server_error", "could not complete sign-in")
		return
	}
	claims, err := s.cfg.Verifier.Verify(ctx, tok.AccessToken, p.Resource, mcpGroup)
	if err != nil {
		if errors.Is(err, jwtverify.ErrGroup) {
			fail("access_denied", "this user may not use MCP servers")
			return
		}
		slog.Warn("oauth: callback", "outcome", "token_verify_failed")
		fail("server_error", "could not verify the sign-in")
		return
	}

	code, err := randomToken()
	if err != nil {
		slog.Error("oauth: callback", "outcome", "random_failed")
		fail("server_error", "")
		return
	}
	scope := claims.Scope
	if scope == "" {
		scope = p.Scope
	}
	rec := codeRecord{
		ClientID:      p.ClientID,
		RedirectURI:   p.RedirectURI,
		CodeChallenge: p.CodeChallenge,
		Resource:      p.Resource,
		Scope:         scope,
		AccessToken:   tok.AccessToken,
		RefreshToken:  tok.RefreshToken,
		AccessExpiry:  claims.Expiry.Unix(),
	}
	if err := s.st.Put(ctx, kindCode, codeKey(code), rec, codeTTL); err != nil {
		slog.Error("oauth: callback", "outcome", "store_failed")
		fail("server_error", "")
		return
	}
	slog.Info("oauth: callback", "outcome", "code_issued")
	params := url.Values{"code": {code}}
	if p.State != "" {
		params.Set("state", p.State)
	}
	s.redirectToClient(w, r, p.RedirectURI, params)
}

package oauth

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	kindPending = "pending"
	pendingTTL  = 10 * time.Minute
	maxStateLen = 1024
	mcpGroup    = "mcp-users"
)

// pendingAuth is an /authorize request waiting for the Cognito sign-in. It is
// keyed by the random state sent to Cognito and taken (once) at /callback.
type pendingAuth struct {
	ClientID      string `json:"client_id"`
	RedirectURI   string `json:"redirect_uri"`
	State         string `json:"state,omitempty"`
	CodeChallenge string `json:"code_challenge"`
	Resource      string `json:"resource"`
	Scope         string `json:"scope"`
	// Verifier is the bridge's own PKCE verifier for the Cognito exchange.
	Verifier string `json:"verifier"`
}

// handleAuthorize validates the app's request, saves it as pending and sends
// the browser to Cognito managed login. Until client_id, redirect_uri and
// resource are validated, errors are a 400 page and never a redirect.
func (s *server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	for _, k := range []string{"client_id", "redirect_uri", "resource"} {
		if len(q[k]) > 1 {
			s.errorPage(w, http.StatusBadRequest, "authorize", "duplicate_param", k+" appears more than once.")
			return
		}
	}
	clientID, redirectURI, resource := q.Get("client_id"), q.Get("redirect_uri"), q.Get("resource")
	if clientID == "" {
		s.errorPage(w, http.StatusBadRequest, "authorize", "missing_client_id", "client_id is required.")
		return
	}
	client, err := s.resolveClient(ctx, clientID)
	if err != nil {
		if errors.Is(err, ErrInvalidClient) {
			s.errorPage(w, http.StatusBadRequest, "authorize", "invalid_client", "Unknown or invalid client_id.")
			return
		}
		s.errorPage(w, http.StatusInternalServerError, "authorize", "client_lookup_failed", "Could not look up the client. Try again.")
		return
	}
	if redirectURI == "" || !redirectRegistered(client.RedirectURIs, redirectURI) ||
		!AllowedRedirect(redirectURI, s.cfg.ChatGPTCallbackID) {
		s.errorPage(w, http.StatusBadRequest, "authorize", "invalid_redirect_uri", "redirect_uri is missing, not registered for this client, or not allowed.")
		return
	}
	if resource == "" {
		s.errorPage(w, http.StatusBadRequest, "authorize", "missing_resource", "resource is required.")
		return
	}
	ok, err := s.resourceRegistered(ctx, resource)
	if err != nil {
		s.errorPage(w, http.StatusServiceUnavailable, "authorize", "resources_unavailable", "Could not load the registered MCP servers. Try again.")
		return
	}
	if !ok {
		s.errorPage(w, http.StatusBadRequest, "authorize", "unknown_resource", "resource is not a registered MCP server.")
		return
	}

	// From here on, errors go back to the validated redirect_uri.
	state := q.Get("state")
	if len(state) > maxStateLen || len(q["state"]) > 1 {
		s.redirectError(w, r, redirectURI, "", "invalid_request", "state is too long or repeated")
		return
	}
	fail := func(code, desc string) { s.redirectError(w, r, redirectURI, state, code, desc) }
	for k, v := range q {
		if len(v) > 1 {
			fail("invalid_request", k+" appears more than once")
			return
		}
	}
	if q.Get("response_type") != "code" {
		fail("unsupported_response_type", "response_type must be code")
		return
	}
	if q.Get("code_challenge_method") != "S256" {
		fail("invalid_request", "code_challenge_method must be S256")
		return
	}
	challenge := q.Get("code_challenge")
	if !challengePattern.MatchString(challenge) {
		fail("invalid_request", "code_challenge must be a base64url S256 challenge")
		return
	}

	pendingID, err1 := randomToken()
	verifier, err2 := randomToken()
	if err1 != nil || err2 != nil {
		slog.Error("oauth: authorize", "outcome", "random_failed")
		fail("server_error", "")
		return
	}
	scope := grantedScope(q.Get("scope"))
	p := pendingAuth{
		ClientID:      clientID,
		RedirectURI:   redirectURI,
		State:         state,
		CodeChallenge: challenge,
		Resource:      resource,
		Scope:         scope,
		Verifier:      verifier,
	}
	if err := s.st.Put(ctx, kindPending, pendingID, p, pendingTTL); err != nil {
		slog.Error("oauth: authorize", "outcome", "store_failed")
		fail("server_error", "")
		return
	}

	// resource goes on the authorize request only: Cognito binds it to the
	// grant, and sending it again at /oauth2/token fails (resource-binding.md).
	cq := url.Values{
		"response_type":         {"code"},
		"client_id":             {s.cfg.CognitoClientID},
		"redirect_uri":          {s.issuer(r) + "/callback"},
		"scope":                 {scope},
		"state":                 {pendingID},
		"code_challenge":        {s256(verifier)},
		"code_challenge_method": {"S256"},
		"resource":              {resource},
	}
	slog.Info("oauth: authorize", "outcome", "redirect_to_cognito")
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, s.cognitoBase()+"/oauth2/authorize?"+cq.Encode(), http.StatusFound)
}

// grantedScope intersects the requested scope with "openid email". openid is
// always included (Cognito needs it for email); no request means both.
func grantedScope(requested string) string {
	fields := strings.Fields(requested)
	if len(fields) == 0 {
		return "openid email"
	}
	for _, f := range fields {
		if f == "email" {
			return "openid email"
		}
	}
	for _, f := range fields {
		if f == "openid" {
			return "openid"
		}
	}
	return "openid email"
}

// redirectToClient sends the browser to the client's validated redirect_uri
// with params plus iss (RFC 9207). Allowlisted redirect URIs have no query.
func (s *server) redirectToClient(w http.ResponseWriter, r *http.Request, redirectURI string, params url.Values) {
	params.Set("iss", s.issuer(r))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, redirectURI+"?"+params.Encode(), http.StatusFound)
}

// redirectError redirects an RFC 6749 §4.1.2.1 error to the client.
func (s *server) redirectError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, description string) {
	slog.Info("oauth: "+strings.TrimPrefix(r.URL.Path, "/"), "outcome", code)
	params := url.Values{"error": {code}}
	if description != "" {
		params.Set("error_description", description)
	}
	if state != "" {
		params.Set("state", state)
	}
	s.redirectToClient(w, r, redirectURI, params)
}

// errorPage renders an error the browser shows instead of a redirect. It is
// plain text, so nothing from the request can inject markup.
func (s *server) errorPage(w http.ResponseWriter, status int, endpoint, outcome, message string) {
	slog.Info("oauth: "+endpoint, "outcome", outcome)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write([]byte("Sign-in error: " + message + "\n"))
}

package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"

	"github.com/marianina8/audiofile-api/internal/jwtverify"
)

const (
	flowIssuer      = "https://auth.example.test"
	flowResource    = "https://mcp.example.test/mcp"
	flowCognitoID   = "bridge-cognito-client"
	claudeCallback  = "https://claude.ai/api/mcp/auth_callback"
	handlerBudget   = 100 * time.Millisecond
	claudeCodeCIMD  = "/oauth/claude-code-client-metadata"
	testVerifierStr = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk-and-more"
)

// fakeCognito mimics the parts of Cognito managed login the bridge uses:
// /oauth2/authorize (signs the user in at once), /oauth2/token and the JWKS.
// Like the real pool in the spike, a token request that carries `resource`
// fails with invalid_grant.
type fakeCognito struct {
	*httptest.Server
	t    *testing.T
	priv jwk.Key
	pub  jwk.Key

	mu               sync.Mutex
	groups           []string
	callback         string
	codes            map[string]fakeGrant
	refresh          map[string]string // refresh token -> resource
	authorizeQueries []url.Values
	tokenForms       []url.Values
}

type fakeGrant struct {
	challenge, resource string
}

func newFakeCognito(t *testing.T, callback string) *fakeCognito {
	t.Helper()
	raw, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := jwk.Import(raw)
	if err != nil {
		t.Fatal(err)
	}
	_ = priv.Set(jwk.KeyIDKey, "k1")
	_ = priv.Set(jwk.AlgorithmKey, jwa.RS256())
	pub, err := priv.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeCognito{
		t: t, priv: priv, pub: pub, callback: callback,
		groups:  []string{"mcp-users"},
		codes:   map[string]fakeGrant{},
		refresh: map[string]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth2/authorize", f.authorize)
	mux.HandleFunc("POST /oauth2/token", f.token)
	mux.HandleFunc("GET /.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		set := jwk.NewSet()
		_ = set.AddKey(f.pub)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeCognito) setGroups(g ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groups = g
}

func (f *fakeCognito) lastAuthorize() url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authorizeQueries[len(f.authorizeQueries)-1]
}

func (f *fakeCognito) forms() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.tokenForms...)
}

func (f *fakeCognito) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	f.authorizeQueries = append(f.authorizeQueries, q)
	f.mu.Unlock()
	if q.Get("client_id") != flowCognitoID || q.Get("redirect_uri") != f.callback ||
		q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" ||
		q.Get("code_challenge") == "" || q.Get("resource") == "" || q.Get("state") == "" {
		http.Error(w, "bad authorize request", http.StatusBadRequest)
		return
	}
	code := randString(f.t)
	f.mu.Lock()
	f.codes[code] = fakeGrant{challenge: q.Get("code_challenge"), resource: q.Get("resource")}
	f.mu.Unlock()
	http.Redirect(w, r, f.callback+"?"+url.Values{"code": {code}, "state": {q.Get("state")}}.Encode(), http.StatusFound)
}

func (f *fakeCognito) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		f.oauthErr(w, "invalid_request")
		return
	}
	form := r.PostForm
	f.mu.Lock()
	f.tokenForms = append(f.tokenForms, form)
	f.mu.Unlock()
	if form.Has("resource") || form.Get("client_id") != flowCognitoID || form.Has("client_secret") {
		f.oauthErr(w, "invalid_grant")
		return
	}
	var resource string
	switch form.Get("grant_type") {
	case "authorization_code":
		f.mu.Lock()
		g, ok := f.codes[form.Get("code")]
		delete(f.codes, form.Get("code"))
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(form.Get("code_verifier")))
		if !ok || form.Get("redirect_uri") != f.callback ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			f.oauthErr(w, "invalid_grant")
			return
		}
		resource = g.resource
	case "refresh_token":
		f.mu.Lock()
		res, ok := f.refresh[form.Get("refresh_token")]
		f.mu.Unlock()
		if !ok {
			f.oauthErr(w, "invalid_grant")
			return
		}
		resource = res
	default:
		f.oauthErr(w, "unsupported_grant_type")
		return
	}
	resp := map[string]any{
		"access_token": f.mint(resource),
		"id_token":     "fake-id-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
	}
	if form.Get("grant_type") == "authorization_code" {
		rt := randString(f.t)
		f.mu.Lock()
		f.refresh[rt] = resource
		f.mu.Unlock()
		resp["refresh_token"] = rt
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (f *fakeCognito) oauthErr(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func (f *fakeCognito) mint(aud string) string {
	f.mu.Lock()
	groups := f.groups
	f.mu.Unlock()
	tok, err := jwt.NewBuilder().
		Issuer(f.URL).
		Subject("user-123").
		Audience([]string{aud}).
		Expiration(time.Now().Add(time.Hour)).
		IssuedAt(time.Now()).
		Claim("token_use", "access").
		Claim("client_id", flowCognitoID).
		Claim("scope", "openid email").
		Claim("cognito:groups", groups).
		Build()
	if err != nil {
		f.t.Fatal(err)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), f.priv))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(signed)
}

func randString(t *testing.T) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func challengeOf(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// flowEnv is a bridge wired to a fake Cognito and a TLS server that serves
// client metadata documents.
type flowEnv struct {
	t    *testing.T
	h    http.Handler
	cog  *fakeCognito
	cimd *httptest.Server
	// cimdDocs maps a path on the CIMD server to a document; "{self}" in
	// client_id is replaced with the document's URL.
	cimdDocs map[string]map[string]any
}

func newFlowEnv(t *testing.T) *flowEnv {
	t.Helper()
	e := &flowEnv{t: t, cimdDocs: map[string]map[string]any{}}
	e.cog = newFakeCognito(t, flowIssuer+"/callback")
	e.cimd = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doc, ok := e.cimdDocs[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		out := map[string]any{}
		for k, v := range doc {
			out[k] = v
		}
		out["client_id"] = e.cimd.URL + r.URL.Path
		writeDoc(w, out)
	}))
	t.Cleanup(e.cimd.Close)

	s := newServer(Config{
		Issuer:          flowIssuer,
		CognitoDomain:   e.cog.URL,
		CognitoClientID: flowCognitoID,
		Resources: func(context.Context) ([]string, error) {
			return []string{flowResource, "https://other.example.test/mcp"}, nil
		},
		Verifier:   jwtverify.New(e.cog.URL, e.cog.URL+"/.well-known/jwks.json"),
		HTTPClient: e.cimd.Client(),
	}, NewMemoryState())
	host := strings.TrimPrefix(e.cimd.URL, "https://")
	s.allowCIMDHost = func(u *url.URL) bool { return u.Host == host }
	e.h = s.routes()
	return e
}

// serve runs one bridge request and checks it stays within the latency budget.
func (e *flowEnv) serve(req *http.Request) *httptest.ResponseRecorder {
	e.t.Helper()
	rec := httptest.NewRecorder()
	start := time.Now()
	e.h.ServeHTTP(rec, req)
	if d := time.Since(start); d > handlerBudget {
		e.t.Errorf("%s %s took %v, budget %v", req.Method, req.URL.Path, d, handlerBudget)
	}
	return rec
}

func (e *flowEnv) register(redirects ...string) string {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{"redirect_uris": redirects})
	rec := postRegister(e.t, e.h, "application/json", string(body))
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reg); err != nil {
		e.t.Fatal(err)
	}
	return reg.ClientID
}

func (e *flowEnv) authorize(q url.Values) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.serve(httptest.NewRequest(http.MethodGet, "/authorize?"+q.Encode(), nil))
}

func authorizeParams(clientID, redirectURI, verifier string) url.Values {
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challengeOf(verifier)},
		"code_challenge_method": {"S256"},
		"resource":              {flowResource},
		"state":                 {"app-state-123"},
		"scope":                 {"openid email profile"},
	}
}

// signIn follows the bridge's redirect to the fake Cognito and returns the
// /callback request Cognito redirects the browser to.
func (e *flowEnv) signIn(authorizeRec *httptest.ResponseRecorder) *http.Request {
	e.t.Helper()
	if authorizeRec.Code != http.StatusFound {
		e.t.Fatalf("authorize: %d %s", authorizeRec.Code, authorizeRec.Body)
	}
	loc := authorizeRec.Header().Get("Location")
	if !strings.HasPrefix(loc, e.cog.URL+"/oauth2/authorize?") {
		e.t.Fatalf("authorize redirected to %q, want the Cognito authorize endpoint", loc)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(loc)
	if err != nil {
		e.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		e.t.Fatalf("fake Cognito authorize: %d", resp.StatusCode)
	}
	cb, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || !strings.HasPrefix(cb.String(), flowIssuer+"/callback?") {
		e.t.Fatalf("Cognito redirected to %q", resp.Header.Get("Location"))
	}
	return httptest.NewRequest(http.MethodGet, "/callback?"+cb.RawQuery, nil)
}

// clientRedirect checks rec is a 302 to redirectURI carrying iss, and returns
// the redirect's query.
func (e *flowEnv) clientRedirect(rec *httptest.ResponseRecorder, redirectURI string) url.Values {
	e.t.Helper()
	if rec.Code != http.StatusFound {
		e.t.Fatalf("status = %d, want 302 (body %s)", rec.Code, rec.Body)
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		e.t.Fatal(err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != redirectURI {
		e.t.Fatalf("redirected to %q, want %q", got, redirectURI)
	}
	q := u.Query()
	if q.Get("iss") != flowIssuer {
		e.t.Errorf("redirect iss = %q, want %q", q.Get("iss"), flowIssuer)
	}
	return q
}

// authCode runs /authorize → Cognito → /callback and returns the bridge code.
func (e *flowEnv) authCode(clientID, redirectURI, verifier string) string {
	e.t.Helper()
	cb := e.signIn(e.authorize(authorizeParams(clientID, redirectURI, verifier)))
	q := e.clientRedirect(e.serve(cb), redirectURI)
	if q.Get("error") != "" {
		e.t.Fatalf("callback error %q: %q", q.Get("error"), q.Get("error_description"))
	}
	if q.Get("state") != "app-state-123" {
		e.t.Errorf("state = %q", q.Get("state"))
	}
	if q.Get("code") == "" {
		e.t.Fatal("no code in app redirect")
	}
	return q.Get("code")
}

func (e *flowEnv) token(form url.Values) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return e.serve(req)
}

func codeForm(code, clientID, redirectURI, verifier string) url.Values {
	return url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
		"resource":      {flowResource},
	}
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func (e *flowEnv) tokenOK(rec *httptest.ResponseRecorder) tokenResponse {
	e.t.Helper()
	if rec.Code != http.StatusOK {
		e.t.Fatalf("token: %d %s", rec.Code, rec.Body)
	}
	checkNoStore(e.t, rec)
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		e.t.Fatal(err)
	}
	if _, ok := raw["id_token"]; ok {
		e.t.Error("token response includes the Cognito id_token")
	}
	var tr tokenResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &tr)
	return tr
}

func checkNoStore(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if p := rec.Header().Get("Pragma"); p != "no-cache" {
		t.Errorf("Pragma = %q, want no-cache", p)
	}
}

func tokenError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("status = %d, want %d (body %s)", rec.Code, status, rec.Body)
	}
	checkNoStore(t, rec)
	if got := oauthErrorCode(t, rec); got != code {
		t.Errorf("error = %q, want %q", got, code)
	}
}

func noRedirect(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body %s)", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("redirected to %q, want no redirect", loc)
	}
}

func TestFullFlow(t *testing.T) {
	e := newFlowEnv(t)
	clientID := e.register(claudeCallback, "http://localhost:4444/callback")
	verifier := testVerifierStr

	code := e.authCode(clientID, claudeCallback, verifier)

	// What the bridge sent to Cognito.
	aq := e.cog.lastAuthorize()
	if aq.Get("resource") != flowResource {
		t.Errorf("Cognito authorize resource = %q", aq.Get("resource"))
	}
	if aq.Get("scope") != "openid email" {
		t.Errorf("Cognito authorize scope = %q, want openid email", aq.Get("scope"))
	}
	if aq.Get("state") == "app-state-123" || aq.Get("code_challenge") == challengeOf(verifier) {
		t.Error("bridge forwarded the app's state or PKCE challenge to Cognito")
	}

	tr := e.tokenOK(e.token(codeForm(code, clientID, claudeCallback, verifier)))
	if tr.TokenType != "Bearer" || tr.AccessToken == "" || tr.RefreshToken == "" {
		t.Errorf("token response = %+v", tr)
	}
	if tr.ExpiresIn < 3500 || tr.ExpiresIn > 3600 {
		t.Errorf("expires_in = %d", tr.ExpiresIn)
	}
	if tr.Scope != "openid email" {
		t.Errorf("scope = %q", tr.Scope)
	}
	for _, f := range e.cog.forms() {
		if f.Has("resource") {
			t.Errorf("bridge sent resource to Cognito /oauth2/token (%s)", f.Get("grant_type"))
		}
	}

	t.Run("code reuse is invalid_grant", func(t *testing.T) {
		tokenError(t, e.token(codeForm(code, clientID, claudeCallback, verifier)), http.StatusBadRequest, "invalid_grant")
	})

	t.Run("wrong code_verifier is invalid_grant and burns the code", func(t *testing.T) {
		c := e.authCode(clientID, claudeCallback, verifier)
		tokenError(t, e.token(codeForm(c, clientID, claudeCallback, verifier+"x")), http.StatusBadRequest, "invalid_grant")
		tokenError(t, e.token(codeForm(c, clientID, claudeCallback, verifier)), http.StatusBadRequest, "invalid_grant")
	})

	t.Run("wrong redirect_uri is invalid_grant", func(t *testing.T) {
		c := e.authCode(clientID, claudeCallback, verifier)
		tokenError(t, e.token(codeForm(c, clientID, "http://localhost:4444/callback", verifier)), http.StatusBadRequest, "invalid_grant")
	})

	t.Run("wrong client_id is invalid_grant", func(t *testing.T) {
		c := e.authCode(clientID, claudeCallback, verifier)
		other := e.register(claudeCallback)
		tokenError(t, e.token(codeForm(c, other, claudeCallback, verifier)), http.StatusBadRequest, "invalid_grant")
	})

	t.Run("different resource at token is invalid_grant", func(t *testing.T) {
		c := e.authCode(clientID, claudeCallback, verifier)
		f := codeForm(c, clientID, claudeCallback, verifier)
		f.Set("resource", "https://other.example.test/mcp")
		tokenError(t, e.token(f), http.StatusBadRequest, "invalid_grant")
	})

	t.Run("resource may be omitted at token", func(t *testing.T) {
		c := e.authCode(clientID, claudeCallback, verifier)
		f := codeForm(c, clientID, claudeCallback, verifier)
		f.Del("resource")
		e.tokenOK(e.token(f))
	})

	t.Run("unregistered resource is 400 with no redirect", func(t *testing.T) {
		q := authorizeParams(clientID, claudeCallback, verifier)
		q.Set("resource", "https://evil.example/mcp")
		noRedirect(t, e.authorize(q))
		q.Del("resource")
		noRedirect(t, e.authorize(q))
	})

	t.Run("bad client or redirect_uri is 400 with no redirect", func(t *testing.T) {
		q := authorizeParams("AAAAAAAAAAAAAAAAAAAAAA", claudeCallback, verifier)
		noRedirect(t, e.authorize(q))
		q = authorizeParams(clientID, "https://evil.example/cb", verifier)
		noRedirect(t, e.authorize(q))
		// Allowlisted but not registered for this client.
		q = authorizeParams(clientID, "http://localhost:5555/callback", verifier)
		noRedirect(t, e.authorize(q))
		q = authorizeParams(clientID, claudeCallback, verifier)
		q.Add("redirect_uri", claudeCallback)
		noRedirect(t, e.authorize(q))
	})

	t.Run("plain PKCE redirects with invalid_request", func(t *testing.T) {
		q := authorizeParams(clientID, claudeCallback, verifier)
		q.Set("code_challenge_method", "plain")
		rq := e.clientRedirect(e.authorize(q), claudeCallback)
		if rq.Get("error") != "invalid_request" || rq.Get("state") != "app-state-123" {
			t.Errorf("redirect query = %v", rq)
		}
		q.Del("code_challenge_method")
		if rq := e.clientRedirect(e.authorize(q), claudeCallback); rq.Get("error") != "invalid_request" {
			t.Errorf("missing method: %v", rq)
		}
	})

	t.Run("other authorize errors redirect", func(t *testing.T) {
		q := authorizeParams(clientID, claudeCallback, verifier)
		q.Set("response_type", "token")
		if rq := e.clientRedirect(e.authorize(q), claudeCallback); rq.Get("error") != "unsupported_response_type" {
			t.Errorf("response_type token: %v", rq)
		}
		q = authorizeParams(clientID, claudeCallback, verifier)
		q.Set("code_challenge", "short")
		if rq := e.clientRedirect(e.authorize(q), claudeCallback); rq.Get("error") != "invalid_request" {
			t.Errorf("bad challenge: %v", rq)
		}
	})

	t.Run("user without the group gets access_denied", func(t *testing.T) {
		e.cog.setGroups("someone-else")
		defer e.cog.setGroups("mcp-users")
		cb := e.signIn(e.authorize(authorizeParams(clientID, claudeCallback, verifier)))
		rq := e.clientRedirect(e.serve(cb), claudeCallback)
		if rq.Get("error") != "access_denied" || rq.Get("state") != "app-state-123" || rq.Get("code") != "" {
			t.Errorf("redirect query = %v", rq)
		}
	})

	t.Run("callback pending is single use", func(t *testing.T) {
		cb := e.signIn(e.authorize(authorizeParams(clientID, claudeCallback, verifier)))
		e.clientRedirect(e.serve(cb), claudeCallback)
		replay := httptest.NewRequest(http.MethodGet, cb.URL.String(), nil)
		noRedirect(t, e.serve(replay))
		noRedirect(t, e.serve(httptest.NewRequest(http.MethodGet, "/callback?code=x&state=unknown", nil)))
	})

	t.Run("Cognito error at callback redirects with iss", func(t *testing.T) {
		cb := e.signIn(e.authorize(authorizeParams(clientID, claudeCallback, verifier)))
		q := cb.URL.Query()
		q.Del("code")
		q.Set("error", "access_denied")
		rq := e.clientRedirect(e.serve(httptest.NewRequest(http.MethodGet, "/callback?"+q.Encode(), nil)), claudeCallback)
		if rq.Get("error") != "access_denied" {
			t.Errorf("redirect query = %v", rq)
		}
	})

	t.Run("JSON body to token is 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(`{"grant_type":"authorization_code"}`))
		req.Header.Set("Content-Type", "application/json")
		tokenError(t, e.serve(req), http.StatusBadRequest, "invalid_request")
	})

	t.Run("client_assertion is invalid_client", func(t *testing.T) {
		c := e.authCode(clientID, claudeCallback, verifier)
		f := codeForm(c, clientID, claudeCallback, verifier)
		f.Set("client_assertion_type", "urn:ietf:params:oauth:client-assertion-type:jwt-bearer")
		f.Set("client_assertion", "eyJhbGciOiJSUzI1NiJ9.e30.sig")
		rec := e.token(f)
		tokenError(t, rec, http.StatusUnauthorized, "invalid_client")
		if !strings.Contains(rec.Body.String(), "only token_endpoint_auth_method none is supported") {
			t.Errorf("body = %s", rec.Body)
		}
	})

	t.Run("unsupported grant type", func(t *testing.T) {
		tokenError(t, e.token(url.Values{"grant_type": {"client_credentials"}}), http.StatusBadRequest, "unsupported_grant_type")
	})

	t.Run("refresh round-trips through Cognito", func(t *testing.T) {
		before := len(e.cog.forms())
		rec := e.token(url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {tr.RefreshToken},
			"client_id":     {clientID},
			"resource":      {flowResource},
		})
		got := e.tokenOK(rec)
		if got.AccessToken == "" || got.TokenType != "Bearer" || got.ExpiresIn != 3600 {
			t.Errorf("refresh response = %+v", got)
		}
		forms := e.cog.forms()[before:]
		if len(forms) != 1 || forms[0].Get("grant_type") != "refresh_token" ||
			forms[0].Get("client_id") != flowCognitoID || forms[0].Has("resource") {
			t.Errorf("Cognito refresh forms = %v", forms)
		}

		rec = e.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"unknown"}, "client_id": {clientID}})
		tokenError(t, rec, http.StatusBadRequest, "invalid_grant")

		// A refreshed token for a different resource is refused.
		rec = e.token(url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {tr.RefreshToken},
			"client_id":     {clientID},
			"resource":      {"https://other.example.test/mcp"},
		})
		tokenError(t, rec, http.StatusBadRequest, "invalid_grant")
	})
}

// claudeCodeDoc is Claude Code's client metadata document as published at
// https://claude.ai/oauth/claude-code-client-metadata; client_id is replaced
// with the test server's URL.
var claudeCodeDoc = map[string]any{
	"client_name":                "Claude Code",
	"client_uri":                 "https://claude.ai",
	"redirect_uris":              []string{"http://localhost/callback", "http://127.0.0.1/callback"},
	"grant_types":                []string{"authorization_code", "refresh_token"},
	"response_types":             []string{"code"},
	"token_endpoint_auth_method": "none",
}

func TestFullFlow_ClaudeCodeCIMDLoopback(t *testing.T) {
	e := newFlowEnv(t)
	e.cimdDocs[claudeCodeCIMD] = claudeCodeDoc
	clientID := e.cimd.URL + claudeCodeCIMD
	const redirect = "http://localhost:53122/callback"

	code := e.authCode(clientID, redirect, testVerifierStr)
	tr := e.tokenOK(e.token(codeForm(code, clientID, redirect, testVerifierStr)))
	if tr.AccessToken == "" {
		t.Error("no access token")
	}

	for _, bad := range []string{"http://localhost:53122/other", "http://localhost/callback", "http://evil.com:53122/callback"} {
		noRedirect(t, e.authorize(authorizeParams(clientID, bad, testVerifierStr)))
	}
}

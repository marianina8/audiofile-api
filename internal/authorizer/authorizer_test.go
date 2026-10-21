package authorizer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"

	"github.com/marianina8/audiofile-api/internal/jwtverify"
)

const (
	issuer    = "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_TEST"
	methodArn = "arn:aws:execute-api:us-east-1:123456789012:abc123/v1/POST/mcp"
	audience  = "https://abc123.execute-api.us-east-1.amazonaws.com/v1/mcp"
	kid       = "kid-1"
)

func newKey(t *testing.T) (jwk.Key, jwk.Key) {
	t.Helper()
	raw, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := jwk.Import(raw)
	if err != nil {
		t.Fatal(err)
	}
	_ = priv.Set(jwk.KeyIDKey, kid)
	_ = priv.Set(jwk.AlgorithmKey, jwa.RS256())
	pub, err := priv.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

func setup(t *testing.T) (*jwtverify.Verifier, jwk.Key) {
	t.Helper()
	captureLog(t)
	priv, pub := newKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		set := jwk.NewSet()
		_ = set.AddKey(pub)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(srv.Close)
	return jwtverify.New(issuer, srv.URL), priv
}

func mint(t *testing.T, priv jwk.Key, aud string, groups []string) string {
	t.Helper()
	tok, err := jwt.NewBuilder().
		Issuer(issuer).
		Subject("user-123").
		Expiration(time.Now().Add(time.Hour)).
		IssuedAt(time.Now().Add(-time.Minute)).
		Claim("aud", aud).
		Claim("token_use", "access").
		Claim("client_id", "client-abc").
		Claim("scope", "openid email").
		Claim("cognito:groups", groups).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), priv))
	if err != nil {
		t.Fatal(err)
	}
	return string(signed)
}

func req(token, arn string) events.APIGatewayCustomAuthorizerRequest {
	return events.APIGatewayCustomAuthorizerRequest{Type: "TOKEN", AuthorizationToken: token, MethodArn: arn}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev); log.SetFlags(flags) })
	return &buf
}

func effect(t *testing.T, resp events.APIGatewayCustomAuthorizerResponse) string {
	t.Helper()
	if len(resp.PolicyDocument.Statement) != 1 {
		t.Fatalf("want one statement, got %+v", resp.PolicyDocument)
	}
	return resp.PolicyDocument.Statement[0].Effect
}

func TestAudience(t *testing.T) {
	cases := []struct{ arn, want string }{
		{methodArn, audience},
		{"arn:aws:execute-api:eu-west-1:123456789012:xyz789/v1/GET/mcp", "https://xyz789.execute-api.eu-west-1.amazonaws.com/v1/mcp"},
		{"arn:aws:execute-api:us-east-1:123456789012:abc123/v1/DELETE/mcp/sub/path", audience},
	}
	for _, tc := range cases {
		got, err := Audience(tc.arn)
		if err != nil || got != tc.want {
			t.Errorf("Audience(%q) = %q, %v; want %q", tc.arn, got, err, tc.want)
		}
	}
}

func TestAudienceRejectsBadArns(t *testing.T) {
	for _, arn := range []string{
		"",
		"not-an-arn",
		"arn:aws:lambda:us-east-1:123456789012:function/x",
		"arn:aws:execute-api:us-east-1:123456789012:abc123",
		"arn:aws:execute-api:us-east-1:123456789012:abc123/v1/POST",
		"arn:aws:execute-api:us-east-1:123456789012:abc123/Prod/POST/mcp",
		"arn:aws:execute-api:us-east-1:123456789012:abc123/v1/POST/other",
		"arn:aws:execute-api:us-east-1:123456789012:abc123/v1/POST/mcpx",
		"arn:aws:execute-api::123456789012:abc123/v1/POST/mcp",
		"arn:aws:execute-api:us-east-1:123456789012:/v1/POST/mcp",
	} {
		if got, err := Audience(arn); err == nil {
			t.Errorf("Audience(%q) = %q, want error", arn, got)
		}
	}
}

func TestUnauthorized(t *testing.T) {
	v, priv := setup(t)
	good := mint(t, priv, audience, []string{"mcp-users"})
	cases := map[string]string{
		"missing":        "",
		"no scheme":      good,
		"wrong scheme":   "Basic " + good,
		"empty bearer":   "Bearer ",
		"garbage":        "Bearer not-a-jwt",
		"wrong audience": "Bearer " + mint(t, priv, "https://other.execute-api.us-east-1.amazonaws.com/v1/mcp", []string{"mcp-users"}),
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			logs := captureLog(t)
			_, err := Handle(context.Background(), v, req(tok, methodArn))
			if err == nil || err.Error() != "Unauthorized" {
				t.Fatalf("got %v, want Unauthorized", err)
			}
			if raw := strings.TrimPrefix(tok, "Bearer "); raw != "" && strings.Contains(logs.String(), raw) {
				t.Fatal("log contains token")
			}
		})
	}
}

func TestNoGroupDenies(t *testing.T) {
	v, priv := setup(t)
	resp, err := Handle(context.Background(), v, req("Bearer "+mint(t, priv, audience, []string{"other"}), methodArn))
	if err != nil {
		t.Fatal(err)
	}
	if e := effect(t, resp); e != "Deny" {
		t.Fatalf("effect = %s, want Deny", e)
	}
	if resp.Context != nil {
		t.Fatalf("deny must not carry context: %v", resp.Context)
	}
}

func TestWrongStageOrPathDenies(t *testing.T) {
	v, priv := setup(t)
	tok := "Bearer " + mint(t, priv, audience, []string{"mcp-users"})
	for _, arn := range []string{
		"arn:aws:execute-api:us-east-1:123456789012:abc123/Prod/POST/mcp",
		"arn:aws:execute-api:us-east-1:123456789012:abc123/v1/GET/.well-known/oauth-protected-resource",
		"garbage",
	} {
		resp, err := Handle(context.Background(), v, req(tok, arn))
		if err != nil {
			t.Fatalf("%s: %v", arn, err)
		}
		if e := effect(t, resp); e != "Deny" {
			t.Fatalf("%s: effect = %s, want Deny", arn, e)
		}
	}
}

func TestValidAllows(t *testing.T) {
	v, priv := setup(t)
	tok := mint(t, priv, audience, []string{"mcp-users"})
	logs := captureLog(t)
	resp, err := Handle(context.Background(), v, req("Bearer "+tok, methodArn))
	if err != nil {
		t.Fatal(err)
	}
	if resp.PrincipalID != "user-123" {
		t.Fatalf("principalId = %q", resp.PrincipalID)
	}
	if e := effect(t, resp); e != "Allow" {
		t.Fatalf("effect = %s, want Allow", e)
	}
	st := resp.PolicyDocument.Statement[0]
	want := []string{
		"arn:aws:execute-api:us-east-1:123456789012:abc123/v1/*/mcp",
		"arn:aws:execute-api:us-east-1:123456789012:abc123/v1/*/mcp/*",
	}
	if strings.Join(st.Resource, ",") != strings.Join(want, ",") {
		t.Fatalf("resource = %v, want %v", st.Resource, want)
	}
	if strings.Join(st.Action, ",") != "execute-api:Invoke" || resp.PolicyDocument.Version != "2012-10-17" {
		t.Fatalf("policy = %+v", resp.PolicyDocument)
	}
	wantCtx := map[string]any{"sub": "user-123", "client_id": "client-abc", "scope": "openid email"}
	if len(resp.Context) != len(wantCtx) {
		t.Fatalf("context = %v, want %v", resp.Context, wantCtx)
	}
	for k, v := range wantCtx {
		if resp.Context[k] != v {
			t.Fatalf("context[%s] = %v, want %v", k, resp.Context[k], v)
		}
	}
	if strings.Contains(logs.String(), tok) {
		t.Fatal("log contains token")
	}
	if !strings.Contains(logs.String(), "outcome=allow") {
		t.Fatalf("log = %q, want outcome=allow", logs.String())
	}
}

func TestJWKSOutageIsNotUnauthorized(t *testing.T) {
	priv, _ := newKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	v := jwtverify.New(issuer, srv.URL)
	tok := mint(t, priv, audience, []string{"mcp-users"})
	logs := captureLog(t)
	_, err := Handle(context.Background(), v, req("Bearer "+tok, methodArn))
	if err == nil || err.Error() == "Unauthorized" {
		t.Fatalf("got %v, want a non-Unauthorized error (API Gateway 500)", err)
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), tok) || strings.Contains(logs.String(), tok) {
		t.Fatal("token leaked")
	}
}

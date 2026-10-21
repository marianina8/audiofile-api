package jwtverify

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

const (
	testIssuer   = "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_test"
	testAudience = "https://mcp.example.com/mcp"
	testKid      = "kid-1"
)

type jwksServer struct {
	*httptest.Server
	hits atomic.Int32
	keys atomic.Value // []jwk.Key (public)
}

func newKey(t *testing.T, kid string) (jwk.Key, jwk.Key) {
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

func newJWKS(t *testing.T, pubs ...jwk.Key) *jwksServer {
	t.Helper()
	s := &jwksServer{}
	s.keys.Store(pubs)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		set := jwk.NewSet()
		for _, k := range s.keys.Load().([]jwk.Key) {
			_ = set.AddKey(k)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(s.Close)
	return s
}

type tokOpts struct {
	issuer  string
	aud     any
	noAud   bool
	groups  []string
	use     string
	exp     time.Time
	nbf     time.Time
	hmac    bool
	kid     string
	omitKid bool
}

func mint(t *testing.T, priv jwk.Key, o tokOpts) string {
	t.Helper()
	if o.issuer == "" {
		o.issuer = testIssuer
	}
	if o.aud == nil {
		o.aud = testAudience
	}
	if o.groups == nil {
		o.groups = []string{"mcp-users"}
	}
	if o.use == "" {
		o.use = "access"
	}
	if o.exp.IsZero() {
		o.exp = time.Now().Add(time.Hour)
	}
	b := jwt.NewBuilder().
		Issuer(o.issuer).
		Subject("user-123").
		Expiration(o.exp).
		IssuedAt(time.Now().Add(-time.Minute)).
		Claim("token_use", o.use).
		Claim("client_id", "client-abc").
		Claim("scope", "audiofile/read audiofile/write").
		Claim("cognito:groups", o.groups)
	if !o.noAud {
		b = b.Claim("aud", o.aud)
	}
	if !o.nbf.IsZero() {
		b = b.NotBefore(o.nbf)
	}
	tok, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	key := priv
	alg := jwa.RS256()
	if o.hmac {
		key, err = jwk.Import([]byte("0123456789abcdef0123456789abcdef"))
		if err != nil {
			t.Fatal(err)
		}
		alg = jwa.HS256()
	}
	kid := o.kid
	if kid == "" {
		kid = testKid
	}
	key, _ = key.Clone()
	if o.omitKid {
		_ = key.Remove(jwk.KeyIDKey)
	} else {
		_ = key.Set(jwk.KeyIDKey, kid)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(alg, key))
	if err != nil {
		t.Fatal(err)
	}
	return string(signed)
}

func setup(t *testing.T) (*Verifier, jwk.Key, *jwksServer) {
	t.Helper()
	priv, pub := newKey(t, testKid)
	srv := newJWKS(t, pub)
	return New(testIssuer, srv.URL), priv, srv
}

func TestVerifyValid(t *testing.T) {
	v, priv, _ := setup(t)
	c, err := v.Verify(context.Background(), mint(t, priv, tokOpts{}), testAudience, "mcp-users")
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "user-123" || c.ClientID != "client-abc" || c.TokenUse != "access" ||
		c.Scope != "audiofile/read audiofile/write" ||
		len(c.Audience) != 1 || c.Audience[0] != testAudience ||
		len(c.Groups) != 1 || c.Groups[0] != "mcp-users" ||
		c.Expiry.Before(time.Now()) {
		t.Fatalf("unexpected claims: %+v", c)
	}
}

func TestVerifyAudienceArray(t *testing.T) {
	v, priv, _ := setup(t)
	tok := mint(t, priv, tokOpts{aud: []string{"other", testAudience}})
	if _, err := v.Verify(context.Background(), tok, testAudience, ""); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyErrors(t *testing.T) {
	v, priv, _ := setup(t)
	wrongPriv, _ := newKey(t, testKid)
	cases := []struct {
		name  string
		token string
		aud   string
		group string
		want  error
	}{
		{"wrong aud", mint(t, priv, tokOpts{aud: "https://other.example.com/mcp"}), testAudience, "mcp-users", ErrAudience},
		{"trailing slash aud", mint(t, priv, tokOpts{aud: testAudience + "/"}), testAudience, "", ErrAudience},
		{"no aud match in array", mint(t, priv, tokOpts{aud: []string{"a", "b"}}), testAudience, "", ErrAudience},
		{"missing group", mint(t, priv, tokOpts{groups: []string{"other"}}), testAudience, "mcp-users", ErrGroup},
		{"expired", mint(t, priv, tokOpts{exp: time.Now().Add(-time.Hour)}), testAudience, "mcp-users", ErrInvalid},
		{"other issuer", mint(t, priv, tokOpts{issuer: "https://evil.example.com"}), testAudience, "mcp-users", ErrInvalid},
		{"hs256", mint(t, priv, tokOpts{hmac: true}), testAudience, "mcp-users", ErrInvalid},
		{"id token", mint(t, priv, tokOpts{use: "id"}), testAudience, "mcp-users", ErrInvalid},
		{"same kid different key", mint(t, wrongPriv, tokOpts{}), testAudience, "mcp-users", ErrInvalid},
		{"aud absent", mint(t, priv, tokOpts{noAud: true}), testAudience, "", ErrAudience},
		{"empty expected audience", mint(t, priv, tokOpts{aud: []string{""}}), "", "", ErrAudience},
		{"missing kid", mint(t, priv, tokOpts{omitKid: true}), testAudience, "", ErrInvalid},
		{"alg none", noneToken(t), testAudience, "", ErrInvalid},
		{"future nbf", mint(t, priv, tokOpts{nbf: time.Now().Add(time.Hour)}), testAudience, "", ErrInvalid},
		{"issuer trailing slash", mint(t, priv, tokOpts{issuer: testIssuer + "/"}), testAudience, "", ErrInvalid},
		{"json serialization", `{"payload":"e30","signatures":[]}`, testAudience, "", ErrInvalid},
		{"garbage", "not-a-jwt", testAudience, "", ErrInvalid},
		{"empty", "", testAudience, "", ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := v.Verify(context.Background(), tc.token, tc.aud, tc.group)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if tc.token != "" && strings.Contains(err.Error(), tc.token) {
				t.Fatal("error message contains token")
			}
		})
	}
}

func noneToken(t *testing.T) string {
	t.Helper()
	enc := base64.RawURLEncoding.EncodeToString
	hdr := enc([]byte(`{"alg":"none","kid":"` + testKid + `"}`))
	pl, _ := json.Marshal(map[string]any{
		"iss": testIssuer, "sub": "x", "aud": testAudience, "token_use": "access",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	return hdr + "." + enc(pl) + "."
}

func TestWithinSkewExpiryAccepted(t *testing.T) {
	v, priv, _ := setup(t)
	tok := mint(t, priv, tokOpts{exp: time.Now().Add(-10 * time.Second)})
	if _, err := v.Verify(context.Background(), tok, testAudience, ""); err != nil {
		t.Fatal(err)
	}
}

func TestJWKWithoutAlgVerifies(t *testing.T) {
	priv, pub := newKey(t, testKid)
	_ = pub.Remove(jwk.AlgorithmKey)
	srv := newJWKS(t, pub)
	v := New(testIssuer, srv.URL)
	if _, err := v.Verify(context.Background(), mint(t, priv, tokOpts{}), testAudience, ""); err != nil {
		t.Fatal(err)
	}
}

func TestJWKSOutageThenRecovery(t *testing.T) {
	priv, pub := newKey(t, testKid)
	var down atomic.Bool
	down.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		set := jwk.NewSet()
		_ = set.AddKey(pub)
		_ = json.NewEncoder(w).Encode(set)
	}))
	defer srv.Close()
	v := New(testIssuer, srv.URL)
	v.failGap = 200 * time.Millisecond
	tok := mint(t, priv, tokOpts{})

	start := time.Now()
	_, err := v.Verify(context.Background(), tok, testAudience, "")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}
	if d := time.Since(start); d > fetchTimeout+time.Second {
		t.Fatalf("took %v", d)
	}

	// Concurrent callers during the outage all return promptly.
	var wg sync.WaitGroup
	start = time.Now()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := v.Verify(context.Background(), tok, testAudience, ""); !errors.Is(err, ErrUnavailable) {
				t.Errorf("got %v, want ErrUnavailable", err)
			}
		}()
	}
	wg.Wait()
	if d := time.Since(start); d > fetchTimeout+time.Second {
		t.Fatalf("concurrent calls took %v", d)
	}

	down.Store(false)
	time.Sleep(v.failGap + 50*time.Millisecond)
	if _, err := v.Verify(context.Background(), tok, testAudience, ""); err != nil {
		t.Fatalf("should recover: %v", err)
	}
}

func TestFailureCooldown(t *testing.T) {
	priv, pub := newKey(t, testKid)
	var down atomic.Bool
	down.Store(true)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if down.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		set := jwk.NewSet()
		_ = set.AddKey(pub)
		_ = json.NewEncoder(w).Encode(set)
	}))
	defer srv.Close()
	v := New(testIssuer, srv.URL)
	v.failGap = 500 * time.Millisecond
	tok := mint(t, priv, tokOpts{})

	if _, err := v.Verify(context.Background(), tok, testAudience, ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
	base := hits.Load() // includes registration's background fetch
	for i := 0; i < 20; i++ {
		if _, err := v.Verify(context.Background(), tok, testAudience, ""); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("got %v", err)
		}
	}
	if n := hits.Load(); n != base {
		t.Fatalf("%d fetches during cooldown, want 0", n-base)
	}
	down.Store(false)
	time.Sleep(600 * time.Millisecond)
	if _, err := v.Verify(context.Background(), tok, testAudience, ""); err != nil {
		t.Fatalf("should recover after cooldown: %v", err)
	}
}

func TestHungJWKSBoundedByContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	priv, _ := newKey(t, testKid)
	v := New(testIssuer, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := v.Verify(ctx, mint(t, priv, tokOpts{}), testAudience, "")
	if !errors.Is(err, ErrUnavailable) || time.Since(start) > 2*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
}

func TestNoGroupCheckWhenEmpty(t *testing.T) {
	v, priv, _ := setup(t)
	tok := mint(t, priv, tokOpts{groups: []string{"whatever"}})
	if _, err := v.Verify(context.Background(), tok, testAudience, ""); err != nil {
		t.Fatal(err)
	}
}

func TestKeyRotationRefreshesOnce(t *testing.T) {
	priv1, pub1 := newKey(t, "kid-1")
	srv := newJWKS(t, pub1)
	v := New(testIssuer, srv.URL)
	if _, err := v.Verify(context.Background(), mint(t, priv1, tokOpts{kid: "kid-1"}), testAudience, ""); err != nil {
		t.Fatal(err)
	}

	// Initial load: registration's background fetch plus the explicit one.
	base := srv.hits.Load()
	if base < 1 || base > 2 {
		t.Fatalf("initial load fetched %d times, want 1 or 2", base)
	}

	// Rotate: new key published after the verifier cached the old set.
	priv2, pub2 := newKey(t, "kid-2")
	srv.keys.Store([]jwk.Key{pub1, pub2})
	if _, err := v.Verify(context.Background(), mint(t, priv2, tokOpts{kid: "kid-2"}), testAudience, ""); err != nil {
		t.Fatalf("rotated key should verify after refresh: %v", err)
	}

	if n := srv.hits.Load(); n != base+1 {
		t.Fatalf("rotation fetched %d times total, want %d", n, base+1)
	}

	// Unknown kid storm: refreshes are rate limited.
	privX, _ := newKey(t, "kid-x")
	before := srv.hits.Load()
	for i := 0; i < 10; i++ {
		_, err := v.Verify(context.Background(), mint(t, privX, tokOpts{kid: "kid-x"}), testAudience, "")
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("got %v", err)
		}
	}
	if d := srv.hits.Load() - before; d != 0 {
		t.Fatalf("unknown kid caused %d extra JWKS fetches within the rate limit, want 0", d)
	}
}

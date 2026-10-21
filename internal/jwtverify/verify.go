// Package jwtverify validates Cognito access tokens against the pool's JWKS.
package jwtverify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/lestrrat-go/httprc/v3"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

var (
	// ErrInvalid covers a bad signature, issuer, expiry, algorithm or token_use.
	ErrInvalid = errors.New("invalid token")
	// ErrAudience means the token is valid but not for the expected audience.
	ErrAudience = errors.New("token audience mismatch")
	// ErrGroup means the token is valid but lacks the required group.
	ErrGroup = errors.New("token missing required group")
)

const (
	clockSkew        = 30 * time.Second
	jwksRefresh      = time.Hour
	minUnknownKidGap = time.Minute
)

// Claims are the verified claims of a Cognito access token.
type Claims struct {
	Subject, ClientID, TokenUse, Scope string
	Audience, Groups                   []string
	Expiry                             time.Time
}

// Verifier validates tokens issued by one Cognito user pool.
type Verifier struct {
	issuer, jwksURL string
	refreshGap      time.Duration

	mu          sync.Mutex
	cache       *jwk.Cache
	lastRefresh time.Time
}

// New returns a Verifier for issuer, fetching keys from jwksURL
// (Cognito: <issuer>/.well-known/jwks.json). The JWKS is fetched lazily.
func New(issuer, jwksURL string) *Verifier {
	return &Verifier{issuer: issuer, jwksURL: jwksURL, refreshGap: minUnknownKidGap}
}

// keySet returns the cached JWKS, registering it on first use.
func (v *Verifier) keySet(ctx context.Context) (jwk.Set, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cache == nil {
		// The cache outlives any single request, so it gets its own context.
		c, err := jwk.NewCache(context.Background(), httprc.NewClient())
		if err != nil {
			return nil, err
		}
		v.cache = c
	}
	if !v.cache.IsRegistered(ctx, v.jwksURL) {
		if err := v.cache.Register(ctx, v.jwksURL, jwk.WithConstantInterval(jwksRefresh)); err != nil {
			return nil, err
		}
	}
	return v.cache.Lookup(ctx, v.jwksURL)
}

// refreshOnce re-fetches the JWKS unless it was refreshed within refreshGap.
func (v *Verifier) refreshOnce(ctx context.Context) (jwk.Set, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if time.Since(v.lastRefresh) < v.refreshGap {
		return nil, errors.New("jwks refresh rate limited")
	}
	v.lastRefresh = time.Now()
	return v.cache.Refresh(ctx, v.jwksURL)
}

// Verify checks token's signature (RS256 only), issuer, expiry (30 s skew)
// and token_use=access, then that audience is among its aud values and, if
// requiredGroup is non-empty, that it is among cognito:groups.
func (v *Verifier) Verify(ctx context.Context, token, audience, requiredGroup string) (Claims, error) {
	msg, err := jws.Parse([]byte(token))
	if err != nil || len(msg.Signatures()) != 1 {
		return Claims{}, fmt.Errorf("%w: malformed token", ErrInvalid)
	}
	hdr := msg.Signatures()[0].ProtectedHeaders()
	if alg, ok := hdr.Algorithm(); !ok || alg != jwa.RS256() {
		return Claims{}, fmt.Errorf("%w: unsupported algorithm", ErrInvalid)
	}
	kid, ok := hdr.KeyID()
	if !ok || kid == "" {
		return Claims{}, fmt.Errorf("%w: missing kid", ErrInvalid)
	}

	set, err := v.keySet(ctx)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: loading jwks: %v", ErrInvalid, err)
	}
	key, found := set.LookupKeyID(kid)
	if !found {
		// Possible key rotation: refresh at most once per refreshGap.
		if set, err = v.refreshOnce(ctx); err == nil {
			key, found = set.LookupKeyID(kid)
		}
		if !found {
			return Claims{}, fmt.Errorf("%w: unknown key id", ErrInvalid)
		}
	}

	tok, err := jwt.Parse([]byte(token),
		jwt.WithKey(jwa.RS256(), key),
		jwt.WithIssuer(v.issuer),
		jwt.WithAcceptableSkew(clockSkew),
		jwt.WithValidate(true),
	)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	exp, ok := tok.Expiration()
	if !ok {
		return Claims{}, fmt.Errorf("%w: missing exp", ErrInvalid)
	}

	var c Claims
	c.Expiry = exp
	c.Subject, _ = tok.Subject()
	c.Audience, _ = tok.Audience()
	_ = tok.Get("token_use", &c.TokenUse)
	_ = tok.Get("client_id", &c.ClientID)
	_ = tok.Get("scope", &c.Scope)
	var groups []any
	_ = tok.Get("cognito:groups", &groups)
	for _, g := range groups {
		if s, ok := g.(string); ok {
			c.Groups = append(c.Groups, s)
		}
	}

	if c.TokenUse != "access" {
		return Claims{}, fmt.Errorf("%w: token_use is not access", ErrInvalid)
	}
	if !slices.Contains(c.Audience, audience) {
		return Claims{}, ErrAudience
	}
	if requiredGroup != "" && !slices.Contains(c.Groups, requiredGroup) {
		return Claims{}, ErrGroup
	}
	return c, nil
}

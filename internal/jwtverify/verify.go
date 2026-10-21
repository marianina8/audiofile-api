// Package jwtverify validates Cognito access tokens against the pool's JWKS.
package jwtverify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/lestrrat-go/httprc/v3"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"golang.org/x/sync/singleflight"
)

var (
	// ErrInvalid covers a bad signature, issuer, expiry, algorithm or token_use.
	ErrInvalid = errors.New("invalid token")
	// ErrUnavailable means the JWKS could not be loaded; the token was not judged.
	ErrUnavailable = errors.New("jwks unavailable")
	// ErrAudience means the token is valid but not for the expected audience.
	ErrAudience = errors.New("token audience mismatch")
	// ErrGroup means the token is valid but lacks the required group.
	ErrGroup = errors.New("token missing required group")
)

const (
	clockSkew        = 30 * time.Second
	jwksRefresh      = time.Hour
	fetchTimeout     = 3 * time.Second
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

	sf          singleflight.Group
	mu          sync.Mutex
	cache       *jwk.Cache
	lastRefresh time.Time
}

// New returns a Verifier for issuer, fetching keys from jwksURL
// (Cognito: <issuer>/.well-known/jwks.json). The JWKS is fetched lazily.
func New(issuer, jwksURL string) *Verifier {
	return &Verifier{issuer: issuer, jwksURL: jwksURL, refreshGap: minUnknownKidGap}
}

// ensureCache creates the cache and registers the JWKS URL without fetching
// (WaitReady false), so no network I/O happens under the mutex.
func (v *Verifier) ensureCache(ctx context.Context) (*jwk.Cache, error) {
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
		err := v.cache.Register(ctx, v.jwksURL,
			jwk.WithConstantInterval(jwksRefresh),
			jwk.WithHTTPClient(&http.Client{Timeout: fetchTimeout}),
			jwk.WithWaitReady(false))
		if err != nil {
			return nil, err
		}
	}
	return v.cache, nil
}

// fetch re-fetches the JWKS. Concurrent callers share one bounded fetch, and
// each caller returns as soon as its own ctx is done.
func (v *Verifier) fetch(ctx context.Context, c *jwk.Cache) (jwk.Set, error) {
	ch := v.sf.DoChan("jwks", func() (any, error) {
		fctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		return c.Refresh(fctx, v.jwksURL)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Val.(jwk.Set), nil
	}
}

// keySet returns the cached JWKS, fetching it on first use.
func (v *Verifier) keySet(ctx context.Context) (*jwk.Cache, jwk.Set, error) {
	c, err := v.ensureCache(ctx)
	if err != nil {
		return nil, nil, err
	}
	if set, err := c.Lookup(ctx, v.jwksURL); err == nil {
		return c, set, nil
	}
	set, err := v.fetch(ctx, c)
	return c, set, err
}

// allowRefresh reports whether an unknown-kid refresh may run now, and
// records it. Only the timestamp is guarded; no I/O under the lock.
func (v *Verifier) allowRefresh() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if time.Since(v.lastRefresh) < v.refreshGap {
		return false
	}
	v.lastRefresh = time.Now()
	return true
}

// Verify checks token's signature (RS256 only), issuer, expiry (30 s skew)
// and token_use=access, then that audience is among its aud values and, if
// requiredGroup is non-empty, that it is among cognito:groups.
func (v *Verifier) Verify(ctx context.Context, token, audience, requiredGroup string) (Claims, error) {
	if audience == "" {
		return Claims{}, ErrAudience
	}
	msg, err := jws.Parse([]byte(token), jws.WithCompact())
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

	cache, set, err := v.keySet(ctx)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: loading jwks: %v", ErrUnavailable, err)
	}
	key, found := set.LookupKeyID(kid)
	if !found {
		// Possible key rotation: refresh at most once per refreshGap.
		if v.allowRefresh() {
			set, err = v.fetch(ctx, cache)
			if err != nil {
				return Claims{}, fmt.Errorf("%w: refreshing jwks: %v", ErrUnavailable, err)
			}
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

package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// ErrInvalidClient means the client_id is unknown, malformed, or its metadata
// document could not be fetched or validated.
var ErrInvalidClient = errors.New("invalid_client")

// Client is a resolved OAuth client: a CIMD document or a DCR record.
// RedirectURIs are what the client declared; /authorize must also check each
// against AllowedRedirect.
type Client struct {
	ID           string
	RedirectURIs []string
}

const (
	cimdTimeout   = 3 * time.Second
	cimdMaxBytes  = 64 << 10
	cimdCacheTTL  = 10 * time.Minute
	cimdCacheSize = 64
)

// defaultCIMDHost allows client metadata documents only from claude.ai and
// chatgpt.com on the default HTTPS port. u.Host includes any explicit port,
// so "claude.ai:8443" fails.
func defaultCIMDHost(u *url.URL) bool {
	return u.Host == "claude.ai" || u.Host == "chatgpt.com"
}

// resolveClient looks up clientID as a DCR record (22-char base64url ids) or
// as a Client ID Metadata Document URL. Every failure wraps ErrInvalidClient.
func (s *server) resolveClient(ctx context.Context, clientID string) (Client, error) {
	if clientIDPattern.MatchString(clientID) {
		return s.resolveDCR(ctx, clientID)
	}
	return s.resolveCIMD(ctx, clientID)
}

func (s *server) resolveDCR(ctx context.Context, id string) (Client, error) {
	var rec dcrClient
	if err := s.st.Get(ctx, kindClient, id, &rec); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Client{}, fmt.Errorf("%w: unknown client", ErrInvalidClient)
		}
		return Client{}, err
	}
	// Refresh the TTL on use so active clients never expire.
	if err := s.st.Put(ctx, kindClient, id, rec, dcrClientTTL); err != nil {
		slog.Warn("oauth: client ttl refresh", "client", "dcr", "outcome", "failed")
	}
	return Client{ID: id, RedirectURIs: rec.RedirectURIs}, nil
}

// cimdURL validates a CIMD client_id: an https URL with a path and no
// userinfo, query, fragment, dot segments or unusual encoding, on an allowed
// host.
func (s *server) cimdURL(clientID string) (*url.URL, error) {
	u, err := url.Parse(clientID)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || strings.Contains(clientID, "#") ||
		u.RawPath != "" || len(u.Path) < 2 || u.String() != clientID {
		return nil, fmt.Errorf("%w: client_id is not a valid metadata document URL", ErrInvalidClient)
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return nil, fmt.Errorf("%w: client_id path has dot segments", ErrInvalidClient)
		}
	}
	if !s.allowCIMDHost(u) {
		return nil, fmt.Errorf("%w: client_id host not allowed", ErrInvalidClient)
	}
	return u, nil
}

func (s *server) resolveCIMD(ctx context.Context, clientID string) (Client, error) {
	u, err := s.cimdURL(clientID)
	if err != nil {
		return Client{}, err
	}
	if c, ok := s.cimd.get(clientID); ok {
		return c, nil
	}
	// One fetch per URL at a time. The fetch ignores the first caller's
	// cancellation so a dropped request doesn't fail the others.
	v, err, _ := s.cimdFlight.Do(clientID, func() (any, error) {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cimdTimeout)
		defer cancel()
		c, err := s.fetchCIMD(fctx, u)
		if err != nil {
			slog.Warn("oauth: client resolve", "client", "cimd", "host", u.Host, "outcome", "fetch_failed")
			return Client{}, err
		}
		s.cimd.put(clientID, c)
		return c, nil
	})
	if err != nil {
		return Client{}, err
	}
	c := v.(Client) // shared by every caller in the flight
	return Client{ID: c.ID, RedirectURIs: slices.Clone(c.RedirectURIs)}, nil
}

func (s *server) fetchCIMD(ctx context.Context, u *url.URL) (Client, error) {
	clientID := u.String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientID, nil)
	if err != nil {
		return Client{}, fmt.Errorf("%w: %v", ErrInvalidClient, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.fetch.Do(req)
	if err != nil {
		return Client{}, fmt.Errorf("%w: fetch metadata document: %v", ErrInvalidClient, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Client{}, fmt.Errorf("%w: metadata document status %d", ErrInvalidClient, resp.StatusCode)
	}
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return Client{}, fmt.Errorf("%w: metadata document is not application/json", ErrInvalidClient)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, cimdMaxBytes+1))
	if err != nil {
		return Client{}, fmt.Errorf("%w: read metadata document: %v", ErrInvalidClient, err)
	}
	if len(body) > cimdMaxBytes {
		return Client{}, fmt.Errorf("%w: metadata document over %d bytes", ErrInvalidClient, cimdMaxBytes)
	}
	var doc struct {
		ClientID     string   `json:"client_id"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return Client{}, fmt.Errorf("%w: metadata document is not valid JSON", ErrInvalidClient)
	}
	if doc.ClientID != clientID {
		return Client{}, fmt.Errorf("%w: metadata document client_id does not match its URL", ErrInvalidClient)
	}
	if len(doc.RedirectURIs) == 0 {
		return Client{}, fmt.Errorf("%w: metadata document has no redirect_uris", ErrInvalidClient)
	}
	for _, r := range doc.RedirectURIs {
		if r == "" {
			return Client{}, fmt.Errorf("%w: metadata document has an empty redirect_uri", ErrInvalidClient)
		}
	}
	return Client{ID: clientID, RedirectURIs: doc.RedirectURIs}, nil
}

// cimdHTTPClient copies base (or a default client) with a 3 s timeout and
// redirects disabled: a 3xx is returned as-is and then rejected as non-200.
func cimdHTTPClient(base *http.Client) *http.Client {
	var c http.Client
	if base != nil {
		c = *base
	}
	c.Timeout = cimdTimeout
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// cimdCache holds successfully fetched documents for a fixed TTL, bounded in
// size. When full it drops expired entries, then the one expiring soonest.
type cimdCache struct {
	mu      sync.Mutex
	max     int
	ttl     time.Duration
	now     func() time.Time
	entries map[string]cimdEntry
}

type cimdEntry struct {
	c   Client
	exp time.Time
}

func newCIMDCache(max int, ttl time.Duration, now func() time.Time) *cimdCache {
	return &cimdCache{max: max, ttl: ttl, now: now, entries: map[string]cimdEntry{}}
}

func (c *cimdCache) get(id string) (Client, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok || !c.now().Before(e.exp) {
		return Client{}, false
	}
	return Client{ID: e.c.ID, RedirectURIs: slices.Clone(e.c.RedirectURIs)}, true
}

func (c *cimdCache) put(id string, cl Client) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if _, ok := c.entries[id]; !ok && len(c.entries) >= c.max {
		var oldest string
		for k, e := range c.entries {
			if !now.Before(e.exp) {
				delete(c.entries, k)
				continue
			}
			if oldest == "" || e.exp.Before(c.entries[oldest].exp) {
				oldest = k
			}
		}
		if len(c.entries) >= c.max {
			delete(c.entries, oldest)
		}
	}
	c.entries[id] = cimdEntry{c: cl, exp: now.Add(c.ttl)}
}

func (c *cimdCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

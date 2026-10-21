package oauth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/sync/singleflight"
)

type server struct {
	cfg        Config
	st         State
	now        func() time.Time
	cimd       *cimdCache
	cimdFlight singleflight.Group
	fetch      *http.Client // CIMD fetches: no redirects, short timeout
	upstream   *http.Client // Cognito calls: no redirects, 5 s timeout
	// allowCIMDHost decides which client_id URLs may be fetched. Tests
	// replace it to point at an httptest server.
	allowCIMDHost func(u *url.URL) bool
}

// New returns the bridge's HTTP handler.
func New(cfg Config, st State) http.Handler {
	return newServer(cfg, st).routes()
}

func newServer(cfg Config, st State) *server {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &server{
		cfg:           cfg,
		st:            st,
		now:           now,
		cimd:          newCIMDCache(cimdCacheSize, cimdCacheTTL, now),
		fetch:         cimdHTTPClient(cfg.HTTPClient),
		upstream:      upstreamHTTPClient(cfg.HTTPClient),
		allowCIMDHost: defaultCIMDHost,
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleMetadata)
	mux.HandleFunc("POST /register", s.handleRegister)
	mux.HandleFunc("GET /authorize", s.handleAuthorize)
	mux.HandleFunc("GET /callback", s.handleCallback)
	mux.HandleFunc("POST /token", s.handleToken)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("oauth: write response", "outcome", "encode_failed")
	}
}

// writeOAuthError writes an RFC 6749 / RFC 7591 style error.
func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, struct {
		Error       string `json:"error"`
		Description string `json:"error_description,omitempty"`
	}{code, description})
}

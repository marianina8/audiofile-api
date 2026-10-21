package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"slices"
	"time"
)

const (
	dcrClientTTL    = 180 * 24 * time.Hour
	dcrMaxBodyBytes = 16 << 10
	dcrMaxRedirects = 10
)

// clientIDPattern matches DCR client ids: 16 random bytes, base64url, no padding.
var clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// dcrClient is the stored record for a dynamically registered client.
type dcrClient struct {
	RedirectURIs  []string `json:"redirect_uris"`
	GrantTypes    []string `json:"grant_types"`
	ResponseTypes []string `json:"response_types"`
	IssuedAt      int64    `json:"client_id_issued_at"`
}

// registrationResponse is the RFC 7591 §3.2.1 response.
type registrationResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

// handleRegister is RFC 7591 dynamic client registration for public clients.
// It never creates a Cognito app client.
func (s *server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		s.registerError(w, "invalid_client_metadata", "Content-Type must be application/json")
		return
	}
	var req struct {
		RedirectURIs            []string `json:"redirect_uris"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, dcrMaxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			s.registerError(w, "invalid_client_metadata", "request body too large")
			return
		}
		s.registerError(w, "invalid_client_metadata", "request body is not a JSON object")
		return
	}
	if _, err := dec.Token(); err != io.EOF {
		s.registerError(w, "invalid_client_metadata", "unexpected data after the JSON object")
		return
	}

	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > dcrMaxRedirects {
		s.registerError(w, "invalid_redirect_uri", "redirect_uris must list 1 to 10 URIs")
		return
	}
	for _, u := range req.RedirectURIs {
		if !allowedRegistrationRedirect(u, s.cfg.ChatGPTCallbackID) {
			s.registerError(w, "invalid_redirect_uri", "redirect_uri is not on the allowlist")
			return
		}
	}
	if req.TokenEndpointAuthMethod != "" && req.TokenEndpointAuthMethod != "none" {
		s.registerError(w, "invalid_client_metadata", `token_endpoint_auth_method must be "none"`)
		return
	}
	if req.GrantTypes == nil {
		req.GrantTypes = []string{"authorization_code", "refresh_token"}
	}
	for _, g := range req.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			s.registerError(w, "invalid_client_metadata", "unsupported grant_type")
			return
		}
	}
	if !slices.Contains(req.GrantTypes, "authorization_code") {
		s.registerError(w, "invalid_client_metadata", "grant_types must include authorization_code")
		return
	}
	if req.ResponseTypes == nil {
		req.ResponseTypes = []string{"code"}
	}
	for _, rt := range req.ResponseTypes {
		if rt != "code" {
			s.registerError(w, "invalid_client_metadata", "unsupported response_type")
			return
		}
	}

	id, err := newClientID()
	if err != nil {
		slog.Error("oauth: register", "client", "dcr", "outcome", "id_generation_failed")
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	rec := dcrClient{
		RedirectURIs:  req.RedirectURIs,
		GrantTypes:    req.GrantTypes,
		ResponseTypes: req.ResponseTypes,
		IssuedAt:      s.now().Unix(),
	}
	if err := s.st.Put(r.Context(), kindClient, id, rec, dcrClientTTL); err != nil {
		slog.Error("oauth: register", "client", "dcr", "outcome", "store_failed")
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	slog.Info("oauth: register", "client", "dcr", "outcome", "registered")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, registrationResponse{
		ClientID:                id,
		ClientIDIssuedAt:        rec.IssuedAt,
		RedirectURIs:            rec.RedirectURIs,
		TokenEndpointAuthMethod: "none",
		GrantTypes:              rec.GrantTypes,
		ResponseTypes:           rec.ResponseTypes,
	})
}

func (s *server) registerError(w http.ResponseWriter, code, description string) {
	slog.Info("oauth: register", "client", "dcr", "outcome", code)
	writeOAuthError(w, http.StatusBadRequest, code, description)
}

// newClientID returns 16 random bytes as 22 base64url characters.
func newClientID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

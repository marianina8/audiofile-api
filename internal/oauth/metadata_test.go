package oauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestMetadata(t *testing.T) {
	const iss = "https://auth.example.test"
	h := New(Config{Issuer: iss}, NewMemoryState())

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	req.Host = "attacker.example"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=300" {
		t.Errorf("Cache-Control = %q", cc)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"issuer":                                         iss,
		"authorization_endpoint":                         iss + "/authorize",
		"token_endpoint":                                 iss + "/token",
		"registration_endpoint":                          iss + "/register",
		"response_types_supported":                       []any{"code"},
		"grant_types_supported":                          []any{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []any{"S256"},
		"token_endpoint_auth_methods_supported":          []any{"none"},
		"client_id_metadata_document_supported":          true,
		"authorization_response_iss_parameter_supported": true,
		"scopes_supported":                               []any{"openid", "email"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("metadata mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestMetadata_IssuerTrailingSlashTrimmed(t *testing.T) {
	h := New(Config{Issuer: "https://auth.example.test/"}, NewMemoryState())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))
	var got struct {
		Issuer string `json:"issuer"`
		Token  string `json:"token_endpoint"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Issuer != "https://auth.example.test" || got.Token != "https://auth.example.test/token" {
		t.Errorf("got %+v", got)
	}
}

func TestMetadata_IssuerFromHostWhenUnset(t *testing.T) {
	h := New(Config{}, NewMemoryState())
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	req.Host = "bridge.local:8443"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var got struct {
		Issuer string `json:"issuer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Issuer != "https://bridge.local:8443" {
		t.Errorf("issuer = %q", got.Issuer)
	}
}

func TestRoutes(t *testing.T) {
	h := New(Config{Issuer: "https://auth.example.test"}, NewMemoryState())
	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/authorize", http.StatusNotImplemented},
		{http.MethodGet, "/callback", http.StatusNotImplemented},
		{http.MethodPost, "/token", http.StatusNotImplemented},
		{http.MethodPost, "/.well-known/oauth-authorization-server", http.StatusMethodNotAllowed},
		{http.MethodGet, "/register", http.StatusMethodNotAllowed},
		{http.MethodGet, "/.well-known/openid-configuration", http.StatusNotFound},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
}

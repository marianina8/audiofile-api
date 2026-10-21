package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func postRegister(t *testing.T, h http.Handler, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func oauthErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("error Content-Type = %q", ct)
	}
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("error body %q: %v", rec.Body.String(), err)
	}
	return e.Error
}

func TestRegister(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	st := NewMemoryState()
	st.Now = clock.Now
	h := New(Config{Issuer: "https://auth.example.test", Now: clock.Now}, st)

	t.Run("good input gives 201", func(t *testing.T) {
		rec := postRegister(t, h, "application/json; charset=utf-8",
			`{"redirect_uris":["https://claude.ai/api/mcp/auth_callback","http://localhost:4321/callback"],
			  "client_name":"Claude","token_endpoint_auth_method":"none"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Cache-Control = %q", cc)
		}
		var got struct {
			ClientID        string   `json:"client_id"`
			IssuedAt        int64    `json:"client_id_issued_at"`
			RedirectURIs    []string `json:"redirect_uris"`
			TokenAuthMethod string   `json:"token_endpoint_auth_method"`
			GrantTypes      []string `json:"grant_types"`
			ResponseTypes   []string `json:"response_types"`
			ClientSecret    *string  `json:"client_secret"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if raw, err := base64.RawURLEncoding.DecodeString(got.ClientID); len(got.ClientID) != 22 || err != nil || len(raw) != 16 {
			t.Errorf("client_id %q is not 22-char base64url", got.ClientID)
		}
		if got.IssuedAt != clock.t.Unix() {
			t.Errorf("client_id_issued_at = %d", got.IssuedAt)
		}
		if !reflect.DeepEqual(got.RedirectURIs, []string{"https://claude.ai/api/mcp/auth_callback", "http://localhost:4321/callback"}) {
			t.Errorf("redirect_uris = %v", got.RedirectURIs)
		}
		if got.TokenAuthMethod != "none" || got.ClientSecret != nil {
			t.Errorf("auth method %q, secret %v", got.TokenAuthMethod, got.ClientSecret)
		}
		if !reflect.DeepEqual(got.GrantTypes, []string{"authorization_code", "refresh_token"}) ||
			!reflect.DeepEqual(got.ResponseTypes, []string{"code"}) {
			t.Errorf("grant_types %v response_types %v", got.GrantTypes, got.ResponseTypes)
		}
	})

	t.Run("client ids are distinct", func(t *testing.T) {
		body := `{"redirect_uris":["http://127.0.0.1:9/callback"]}`
		a := postRegister(t, h, "application/json", body)
		b := postRegister(t, h, "application/json", body)
		var ia, ib struct {
			ClientID string `json:"client_id"`
		}
		_ = json.Unmarshal(a.Body.Bytes(), &ia)
		_ = json.Unmarshal(b.Body.Bytes(), &ib)
		if a.Code != 201 || b.Code != 201 || ia.ClientID == ib.ClientID {
			t.Errorf("codes %d %d, ids %q %q", a.Code, b.Code, ia.ClientID, ib.ClientID)
		}
	})

	bad := []struct {
		name, contentType, body, wantErr string
	}{
		{"bad redirect", "application/json", `{"redirect_uris":["https://evil.example/cb"]}`, "invalid_redirect_uri"},
		{"one bad redirect among good", "application/json", `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback","http://localhost:1/evil"]}`, "invalid_redirect_uri"},
		{"missing redirect_uris", "application/json", `{"client_name":"x"}`, "invalid_redirect_uri"},
		{"empty redirect_uris", "application/json", `{"redirect_uris":[]}`, "invalid_redirect_uri"},
		{"non-JSON content type", "application/x-www-form-urlencoded", `redirect_uris=https://claude.ai/api/mcp/auth_callback`, "invalid_client_metadata"},
		{"no content type", "", `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"]}`, "invalid_client_metadata"},
		{"malformed JSON", "application/json", `{"redirect_uris":`, "invalid_client_metadata"},
		{"trailing data", "application/json", `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"]} {}`, "invalid_client_metadata"},
		{"confidential client", "application/json", `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"token_endpoint_auth_method":"client_secret_basic"}`, "invalid_client_metadata"},
		{"unsupported grant", "application/json", `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"grant_types":["authorization_code","client_credentials"]}`, "invalid_client_metadata"},
		{"grant without authorization_code", "application/json", `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"grant_types":["refresh_token"]}`, "invalid_client_metadata"},
		{"unsupported response type", "application/json", `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"response_types":["token"]}`, "invalid_client_metadata"},
		{"too large", "application/json", `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"client_name":"` + strings.Repeat("a", 17*1024) + `"}`, "invalid_client_metadata"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			rec := postRegister(t, h, c.contentType, c.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body)
			}
			if got := oauthErrorCode(t, rec); got != c.wantErr {
				t.Errorf("error = %q, want %q", got, c.wantErr)
			}
		})
	}
}

func TestRegister_ClientResolvesAndTTLRefreshesOnUse(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	st := NewMemoryState()
	st.Now = clock.Now
	s := newServer(Config{Issuer: "https://auth.example.test", Now: clock.Now}, st)

	rec := postRegister(t, s.routes(), "application/json", `{"redirect_uris":["http://localhost:5555/callback"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reg); err != nil {
		t.Fatal(err)
	}

	day := 24 * time.Hour
	clock.advance(179 * day)
	c, err := s.resolveClient(ctx, reg.ClientID)
	if err != nil {
		t.Fatalf("resolve at day 179: %v", err)
	}
	if c.ID != reg.ClientID || !reflect.DeepEqual(c.RedirectURIs, []string{"http://localhost:5555/callback"}) {
		t.Errorf("client = %+v", c)
	}
	clock.advance(179 * day) // 358 days after registration, 179 after last use
	if _, err := s.resolveClient(ctx, reg.ClientID); err != nil {
		t.Fatalf("resolve after refresh: %v", err)
	}
	clock.advance(181 * day)
	if _, err := s.resolveClient(ctx, reg.ClientID); err == nil {
		t.Fatal("resolve after 181 idle days succeeded")
	}
}

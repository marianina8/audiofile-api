package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const cimdPath = "/oauth/client.json"

// cimdServer serves handler over TLS and returns a server whose CIMD host
// check accepts only that test server.
func cimdServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, self string)) (*server, string, *fakeClock) {
	t.Helper()
	var self string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r, self)
	}))
	t.Cleanup(ts.Close)
	self = ts.URL + cimdPath
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	st := NewMemoryState()
	st.Now = clock.Now
	s := newServer(Config{Issuer: "https://auth.example.test", HTTPClient: ts.Client(), Now: clock.Now}, st)
	host := strings.TrimPrefix(ts.URL, "https://")
	s.allowCIMDHost = func(u *url.URL) bool { return u.Host == host }
	return s, self, clock
}

func writeDoc(w http.ResponseWriter, doc any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func TestResolveClient_CIMD(t *testing.T) {
	ctx := context.Background()

	t.Run("matching document resolves and is cached", func(t *testing.T) {
		var hits atomic.Int32
		s, id, clock := cimdServer(t, func(w http.ResponseWriter, r *http.Request, self string) {
			hits.Add(1)
			if r.URL.Path != cimdPath {
				http.NotFound(w, r)
				return
			}
			writeDoc(w, map[string]any{
				"client_id":                  self,
				"client_name":                "Test",
				"redirect_uris":              []string{"http://localhost/callback", "https://claude.ai/api/mcp/auth_callback"},
				"token_endpoint_auth_method": "none",
			})
		})
		c, err := s.resolveClient(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if c.ID != id || !reflect.DeepEqual(c.RedirectURIs, []string{"http://localhost/callback", "https://claude.ai/api/mcp/auth_callback"}) {
			t.Errorf("client = %+v", c)
		}
		clock.advance(9 * time.Minute)
		if _, err := s.resolveClient(ctx, id); err != nil {
			t.Fatal(err)
		}
		if n := hits.Load(); n != 1 {
			t.Errorf("fetches within 10 min = %d, want 1", n)
		}
		clock.advance(2 * time.Minute)
		if _, err := s.resolveClient(ctx, id); err != nil {
			t.Fatal(err)
		}
		if n := hits.Load(); n != 2 {
			t.Errorf("fetches after 11 min = %d, want 2", n)
		}
	})

	failures := []struct {
		name    string
		handler func(w http.ResponseWriter, r *http.Request, self string)
	}{
		{"mismatched client_id", func(w http.ResponseWriter, r *http.Request, self string) {
			writeDoc(w, map[string]any{"client_id": self + "x", "redirect_uris": []string{"http://localhost/callback"}})
		}},
		{"missing redirect_uris", func(w http.ResponseWriter, r *http.Request, self string) {
			writeDoc(w, map[string]any{"client_id": self})
		}},
		{"empty redirect_uris", func(w http.ResponseWriter, r *http.Request, self string) {
			writeDoc(w, map[string]any{"client_id": self, "redirect_uris": []string{}})
		}},
		{"document over 64 KB", func(w http.ResponseWriter, r *http.Request, self string) {
			writeDoc(w, map[string]any{"client_id": self, "redirect_uris": []string{"http://localhost/callback"},
				"client_name": strings.Repeat("a", 65*1024)})
		}},
		{"not JSON content type", func(w http.ResponseWriter, r *http.Request, self string) {
			w.Header().Set("Content-Type", "text/html")
			_ = json.NewEncoder(w).Encode(map[string]any{"client_id": self, "redirect_uris": []string{"http://localhost/callback"}})
		}},
		{"malformed JSON", func(w http.ResponseWriter, r *http.Request, self string) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"client_id":`))
		}},
		{"non-200", func(w http.ResponseWriter, r *http.Request, self string) {
			http.Error(w, "nope", http.StatusNotFound)
		}},
		{"redirect not followed", func(w http.ResponseWriter, r *http.Request, self string) {
			if r.URL.Path == cimdPath {
				http.Redirect(w, r, "/other.json", http.StatusFound)
				return
			}
			writeDoc(w, map[string]any{"client_id": self, "redirect_uris": []string{"http://localhost/callback"}})
		}},
	}
	for _, f := range failures {
		t.Run(f.name, func(t *testing.T) {
			s, id, _ := cimdServer(t, f.handler)
			if _, err := s.resolveClient(ctx, id); !errors.Is(err, ErrInvalidClient) {
				t.Errorf("err = %v, want ErrInvalidClient", err)
			}
		})
	}

	t.Run("server that sleeps 5 s times out", func(t *testing.T) {
		s, id, _ := cimdServer(t, func(w http.ResponseWriter, r *http.Request, self string) {
			select {
			case <-time.After(5 * time.Second):
			case <-r.Context().Done():
				return
			}
			writeDoc(w, map[string]any{"client_id": self, "redirect_uris": []string{"http://localhost/callback"}})
		})
		start := time.Now()
		_, err := s.resolveClient(ctx, id)
		if !errors.Is(err, ErrInvalidClient) {
			t.Errorf("err = %v, want ErrInvalidClient", err)
		}
		if d := time.Since(start); d > 4*time.Second {
			t.Errorf("took %v, want about 3 s", d)
		}
	})

	t.Run("failed fetch is not cached", func(t *testing.T) {
		var ok atomic.Bool
		s, id, _ := cimdServer(t, func(w http.ResponseWriter, r *http.Request, self string) {
			if !ok.Load() {
				http.Error(w, "down", http.StatusServiceUnavailable)
				return
			}
			writeDoc(w, map[string]any{"client_id": self, "redirect_uris": []string{"http://localhost/callback"}})
		})
		if _, err := s.resolveClient(ctx, id); err == nil {
			t.Fatal("want error while down")
		}
		ok.Store(true)
		if _, err := s.resolveClient(ctx, id); err != nil {
			t.Fatalf("after recovery: %v", err)
		}
	})

	t.Run("disallowed host", func(t *testing.T) {
		s, id, _ := cimdServer(t, func(w http.ResponseWriter, r *http.Request, self string) {
			t.Error("disallowed host was fetched")
		})
		s.allowCIMDHost = defaultCIMDHost
		if _, err := s.resolveClient(ctx, id); !errors.Is(err, ErrInvalidClient) {
			t.Errorf("err = %v, want ErrInvalidClient", err)
		}
	})
}

func TestDefaultCIMDHost(t *testing.T) {
	cases := map[string]bool{
		"https://claude.ai/oauth/claude-code-client-metadata": true,
		"https://chatgpt.com/oauth/client.json":               true,
		"https://chatgpt.com/oauth/cb123/client.json":         true,
		"https://claude.ai.evil.com/x":                        false,
		"https://evil.claude.ai/x":                            false,
		"https://claude.ai:8443/x":                            false,
		"https://example.com/x":                               false,
	}
	for raw, want := range cases {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := defaultCIMDHost(u); got != want {
			t.Errorf("defaultCIMDHost(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestResolveClient_RejectsBadIDs(t *testing.T) {
	ctx := context.Background()
	s := newServer(Config{Issuer: "https://auth.example.test", HTTPClient: &http.Client{Transport: failTransport{t}}}, NewMemoryState())
	ids := []string{
		"",
		"http://claude.ai/oauth/claude-code-client-metadata",       // not https
		"https://claude.ai.evil.com/oauth/x",                       // other host
		"https://evil.example/oauth/client.json",                   // other host
		"https://user@claude.ai/oauth/claude-code-client-metadata", // userinfo
		"https://claude.ai/oauth/client.json#frag",                 // fragment
		"https://claude.ai/oauth/client.json?x=1",                  // query
		"https://claude.ai",                                        // no path
		"https://claude.ai/",                                       // root path
		"https://claude.ai/a/../b",                                 // dot segments
		"ftp://claude.ai/x",
		"AAAAAAAAAAAAAAAAAAAAAA", // well-formed DCR id that was never registered
		"not-a-client",
	}
	for _, id := range ids {
		if _, err := s.resolveClient(ctx, id); !errors.Is(err, ErrInvalidClient) {
			t.Errorf("resolveClient(%q) err = %v, want ErrInvalidClient", id, err)
		}
	}
}

// failTransport fails the test if any request is made.
type failTransport struct{ t *testing.T }

func (f failTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("unexpected fetch of %s", r.URL)
	return nil, errors.New("no network in this test")
}

func TestCIMDCacheBounded(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	c := newCIMDCache(3, 10*time.Minute, clock.Now)
	for _, k := range []string{"a", "b", "c", "d"} {
		c.put(k, Client{ID: k})
		clock.advance(time.Second)
	}
	if n := c.len(); n != 3 {
		t.Errorf("len = %d, want 3", n)
	}
	if _, ok := c.get("a"); ok {
		t.Error("oldest entry was not evicted")
	}
	if _, ok := c.get("d"); !ok {
		t.Error("newest entry missing")
	}
}

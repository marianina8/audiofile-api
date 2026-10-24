package curated

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeToken is a token endpoint that hands out machine-tok (then machine-tok-2,
// ...) and counts how often it is asked.
func fakeToken(t *testing.T, expiresIn int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "client_credentials" {
			t.Errorf("token request form = %v, err %v", r.PostForm, err)
		}
		id, secret, ok := r.BasicAuth()
		if !ok || id != "client-1" || secret != "s3cret" {
			t.Errorf("token basic auth = %q/%q ok=%v", id, secret, ok)
		}
		tok := "machine-tok"
		if n > 1 {
			tok = fmt.Sprintf("machine-tok-%d", n)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": tok, "expires_in": expiresIn, "token_type": "Bearer"})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newTestToken(tokenURL string) TokenSource {
	secret := func(context.Context) (string, error) { return "s3cret", nil }
	return NewMachineToken(tokenURL, "client-1", secret, nil)
}

func newTestClient(t *testing.T, api http.Handler, expiresIn int) (*Client, *atomic.Int32) {
	t.Helper()
	apiSrv := httptest.NewServer(api)
	t.Cleanup(apiSrv.Close)
	tokSrv, calls := fakeToken(t, expiresIn)
	return NewClient(apiSrv.URL, newTestToken(tokSrv.URL), nil), calls
}

func TestTracksPagesThroughCursor(t *testing.T) {
	var cursors []string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := r.URL.Query().Get("cursor")
		cursors = append(cursors, cur)
		start, n, next := 0, 100, "page2"
		if cur == "page2" {
			start, n, next = 100, 18, ""
		}
		items := make([]Track, n)
		for i := range items {
			items[i] = Track{ID: "t" + strconv.Itoa(start+i), Status: "ready"}
		}
		resp := map[string]any{"items": items}
		if next != "" {
			resp["next_cursor"] = next
		}
		_ = json.NewEncoder(w).Encode(resp)
	}), 3600)

	tracks, err := c.Tracks(context.Background())
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 118 {
		t.Errorf("len = %d, want 118", len(tracks))
	}
	if len(cursors) != 2 || cursors[0] != "" || cursors[1] != "page2" {
		t.Errorf("cursors = %q, want [\"\" \"page2\"]", cursors)
	}
}

func TestTokenCachedUntilNearExpiry(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	for _, tc := range []struct {
		expiresIn int
		want      int32
	}{{3600, 1}, {30, 3}} {
		c, calls := newTestClient(t, ok, tc.expiresIn)
		for i := 0; i < 3; i++ {
			if _, err := c.Playlists(context.Background()); err != nil {
				t.Fatalf("expires_in %d: %v", tc.expiresIn, err)
			}
		}
		if got := calls.Load(); got != tc.want {
			t.Errorf("expires_in %d: %d token requests, want %d", tc.expiresIn, got, tc.want)
		}
	}
}

func TestRetriesOnceOn401(t *testing.T) {
	var apiCalls atomic.Int32
	var seen []string
	c, tokCalls := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		if apiCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}), 3600)

	if _, err := c.Playlists(context.Background()); err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if tokCalls.Load() != 2 {
		t.Errorf("token requests = %d, want 2", tokCalls.Load())
	}
	if len(seen) != 2 || seen[0] != "Bearer machine-tok" || seen[1] != "Bearer machine-tok-2" {
		t.Errorf("Authorization headers = %q", seen)
	}

	// Two 401s in a row: one retry, then a plain message.
	var n atomic.Int32
	c2, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}), 3600)
	_, err := c2.Playlists(context.Background())
	if err == nil || !strings.Contains(err.Error(), "API rejected the server's credentials") {
		t.Fatalf("err = %v", err)
	}
	if n.Load() != 2 {
		t.Errorf("API calls = %d, want 2 (one retry)", n.Load())
	}
}

func TestSendsBearerAndNeverCallerToken(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer machine-tok" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}), 3600)
	// A caller's token in the context must not be forwarded.
	ctx := context.WithValue(context.Background(), "authorization", "Bearer caller-tok")
	if _, err := c.Playlists(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestErrorsHaveNoURL(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"boom"}}`))
	}), 3600)
	_, err := c.Playlists(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "http") {
		t.Errorf("error leaks URL: %v", err)
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want status and API message", err)
	}
}

func TestDeletePlaylist404IsErrNotFound(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/playlists/nope" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"playlist not found"}}`))
	}), 3600)
	err := c.DeletePlaylist(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestPlayerLink(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/playlists/p1/player" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"url":"https://x/play/abc","expires_at":"2026-10-25T00:00:00Z"}`))
	}), 3600)
	got, err := c.PlayerLink(context.Background(), "p1")
	if err != nil || got != "https://x/play/abc" {
		t.Errorf("PlayerLink = %q, %v", got, err)
	}
}

func TestPlaylistWrites(t *testing.T) {
	type req struct{ method, path, body string }
	var got []req
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 512)
		n, _ := r.Body.Read(buf)
		got = append(got, req{r.Method, r.URL.Path, strings.TrimSpace(string(buf[:n]))})
		if r.Method == http.MethodPost && r.URL.Path == "/playlists" {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"p1","name":"Run","description":"d","track_ids":["a","b"]}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), 3600)
	ctx := context.Background()

	pl, err := c.CreatePlaylist(ctx, "Run", "d", []string{"a", "b"})
	if err != nil || pl.ID != "p1" || len(pl.TrackIDs) != 2 {
		t.Fatalf("CreatePlaylist = %+v, %v", pl, err)
	}
	for _, err := range []error{
		c.AddTracks(ctx, "p1", []string{"c"}),
		c.RemoveTrack(ctx, "p1", "a"),
		c.Reorder(ctx, "p1", []string{"b", "c"}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []req{
		{"POST", "/playlists", `{"description":"d","name":"Run","track_ids":["a","b"]}`},
		{"POST", "/playlists/p1/tracks", `{"track_ids":["c"]}`},
		{"DELETE", "/playlists/p1/tracks/a", ""},
		{"PUT", "/playlists/p1/tracks/order", `{"track_ids":["b","c"]}`},
	}
	if len(got) != len(want) {
		t.Fatalf("requests = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d = %v, want %v", i, got[i], want[i])
		}
	}
}

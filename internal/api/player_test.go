package api

import (
	"bytes"
	"context"
	"errors"
	"github.com/marianina8/audiofile-api/internal/blob"
	"github.com/marianina8/audiofile-api/internal/store"
	"log"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/marianina8/audiofile-api/internal/model"
)

func playerPages(t *testing.T, e *env) map[string]string {
	t.Helper()
	out := map[string]string{}
	for k, v := range e.bl.Objects {
		if strings.HasPrefix(k, "players/") {
			if ct := e.bl.ContentTypes[k]; ct != "text/html; charset=utf-8" {
				t.Errorf("%s content type = %q", k, ct)
			}
			out[k] = string(v)
		}
	}
	return out
}

func TestPlayer(t *testing.T) {
	e := newEnv()
	seedTrack(t, e, "a", func(tr *model.Track) { tr.Title = "Second Song"; tr.Artist = "Band"; tr.BPM = 127.6 })
	seedTrack(t, e, "b", func(tr *model.Track) { tr.Title = "First Song" })
	seedTrack(t, e, "c", func(tr *model.Track) { tr.Title = "Still Processing"; tr.Status = model.StatusProcessing })
	id := newPlaylist(t, e, "b", "a")
	// a non-ready track sneaks into the playlist; it must be skipped
	p, _ := e.st.GetPlaylist(t.Context(), id)
	p.TrackIDs = append(p.TrackIDs, "c")
	_ = e.st.PutPlaylist(t.Context(), p)

	rec := e.do("POST", "/playlists/"+id+"/player", "")
	wantStatus(t, rec, 201)
	m := decode(t, rec)
	u, _ := m["url"].(string)
	pid := strings.TrimPrefix(u, "https://example.com/play/")
	if !pidPattern.MatchString(pid) {
		t.Errorf("url = %v", m["url"])
	}
	if m["expires_at"] != "2026-10-20T13:00:00Z" {
		t.Errorf("expires_at = %v", m["expires_at"])
	}

	pages := playerPages(t, e)
	if len(pages) != 1 {
		t.Fatalf("player objects = %d, want 1", len(pages))
	}
	for key, html := range pages {
		if key != "players/"+pid+".html" {
			t.Errorf("key = %s, want players/%s.html", key, pid)
		}
		if got := e.bl.Metadata[key]["expires-at"]; got != "2026-10-20T13:00:00Z" {
			t.Errorf("expires-at metadata = %q", got)
		}
		i1, i2 := strings.Index(html, "First Song"), strings.Index(html, "Second Song")
		if i1 < 0 || i2 < 0 || i1 > i2 {
			t.Errorf("titles missing or out of order (%d, %d)", i1, i2)
		}
		if strings.Contains(html, "Still Processing") {
			t.Error("non-ready track included")
		}
		if n := strings.Count(html, "?ttl=4500"); n != 2 {
			t.Errorf("track urls with ttl=4500 = %d, want 2", n)
		}
		if !strings.Contains(html, "128") || strings.Contains(html, "127.6") {
			t.Error("bpm not rounded to integer")
		}
		for _, want := range []string{`name="viewport"`, `content="noindex"`, `content="no-referrer"`, `<audio controls`, `preload="auto"`, `new Audio()`, `nextAudio`, `Loading`, `load this track`, `dispose(`, `Tap \u25B6 to continue`, `el.volume = old.volume`} {
			if !strings.Contains(html, want) {
				t.Errorf("page missing %s", want)
			}
		}
	}
}

func TestPlayer_EmptyPlaylist(t *testing.T) {
	e := newEnv()
	id := newPlaylist(t, e)
	wantErrCode(t, e.do("POST", "/playlists/"+id+"/player", ""), 400, "bad_request")
	if len(playerPages(t, e)) != 0 {
		t.Error("page written for empty playlist")
	}
}

func TestPlayer_NoReadyTracks(t *testing.T) {
	e := newEnv()
	seedTrack(t, e, "a", nil)
	id := newPlaylist(t, e, "a")
	tr, _ := e.st.GetTrack(t.Context(), "a")
	tr.Status = model.StatusProcessing
	_ = e.st.PutTrack(t.Context(), tr)
	wantErrCode(t, e.do("POST", "/playlists/"+id+"/player", ""), 400, "bad_request")
}

func TestPlayer_UnknownPlaylist(t *testing.T) {
	e := newEnv()
	wantErrCode(t, e.do("POST", "/playlists/nope/player", ""), 404, "not_found")
}

func TestPlayer_EscapesTitles(t *testing.T) {
	e := newEnv()
	seedTrack(t, e, "a", func(tr *model.Track) { tr.Title = "<script>alert(1)</script>" })
	id := newPlaylist(t, e, "a")
	set := func(name string) {
		p, _ := e.st.GetPlaylist(t.Context(), id)
		p.Name = name
		_ = e.st.PutPlaylist(t.Context(), p)
	}
	set("<b>Mix</b>")
	wantStatus(t, e.do("POST", "/playlists/"+id+"/player", ""), 201)
	for _, html := range playerPages(t, e) {
		if !strings.Contains(html, "&lt;script&gt;") {
			t.Error("title not HTML-escaped")
		}
		if strings.Contains(html, "<script src=") {
			t.Error("external script present")
		}
		if strings.Contains(html, "<script>alert(1)") || strings.Contains(html, "<b>Mix</b>") {
			t.Error("raw markup leaked")
		}
	}
}

func TestPlayer_PublicBaseURL(t *testing.T) {
	e := newEnv()
	seedTrack(t, e, "a", nil)
	id := newPlaylist(t, e, "a")
	e.h = New(Deps{Store: e.st, Blob: e.bl, Now: func() time.Time { return fixedNow },
		NewID: func() string { return "x" }, NewSecret: func() string { return "AAAAAAAAAAAAAAAAAAAAAA" },
		PublicBaseURL: "https://api.test/"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/playlists/"+id+"/player", nil)
	req.Header.Set("X-Forwarded-Host", "evil.test")
	e.h.ServeHTTP(rec, req)
	wantStatus(t, rec, 201)
	if u := decode(t, rec)["url"]; u != "https://api.test/play/AAAAAAAAAAAAAAAAAAAAAA" {
		t.Errorf("url = %v", u)
	}
}

func TestPlayer_IgnoresForwardedHost(t *testing.T) {
	e := newEnv()
	seedTrack(t, e, "a", nil)
	id := newPlaylist(t, e, "a")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/playlists/"+id+"/player", nil)
	req.Host = "abc.execute-api.us-west-2.amazonaws.com"
	req.Header.Set("X-Forwarded-Host", "evil.test")
	e.h.ServeHTTP(rec, req)
	if u, _ := decode(t, rec)["url"].(string); !strings.HasPrefix(u, "https://abc.execute-api.us-west-2.amazonaws.com/play/") {
		t.Errorf("url = %v", u)
	}
}

func newPlayerPage(t *testing.T, e *env) string {
	t.Helper()
	seedTrack(t, e, "a", nil)
	id := newPlaylist(t, e, "a")
	rec := e.do("POST", "/playlists/"+id+"/player", "")
	wantStatus(t, rec, 201)
	return strings.TrimPrefix(decode(t, rec)["url"].(string), "https://example.com")
}

func TestGetPlayerPage(t *testing.T) {
	e := newEnv()
	path := newPlayerPage(t, e)
	rec := e.do("GET", path, "")
	wantStatus(t, rec, 200)
	want := map[string]string{
		"Content-Type":            "text/html; charset=utf-8",
		"Cache-Control":           "no-store",
		"Referrer-Policy":         "no-referrer",
		"X-Robots-Tag":            "noindex",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": playerCSP,
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if !strings.Contains(rec.Body.String(), "<audio controls") {
		t.Error("body is not the player page")
	}
	if !strings.Contains(playerCSP, "frame-ancestors 'none'") || !strings.Contains(playerCSP, "media-src https://*.amazonaws.com") {
		t.Error("csp lost required directives")
	}
}

func TestGetPlayerPage_Expired(t *testing.T) {
	e := newEnv()
	path := newPlayerPage(t, e)
	now := fixedNow
	e.h = New(Deps{Store: e.st, Blob: e.bl, Now: func() time.Time { return now.Add(time.Hour + time.Second) }})
	rec := e.do("GET", path, "")
	wantErrCode(t, rec, 404, "not_found")
	unknown := e.do("GET", "/play/AAAAAAAAAAAAAAAAAAAAAA", "")
	if rec.Body.String() != unknown.Body.String() {
		t.Error("expired and unknown bodies differ")
	}
}

func TestGetPlayerPage_MissingExpiryIs404(t *testing.T) {
	e := newEnv()
	_ = e.bl.Put(t.Context(), "players/AAAAAAAAAAAAAAAAAAAAAA.html", htmlType, []byte("x"))
	wantErrCode(t, e.do("GET", "/play/AAAAAAAAAAAAAAAAAAAAAA", ""), 404, "not_found")
}

func TestGetPlayerPage_BadFormatAndUnknown(t *testing.T) {
	e := newEnv()
	for _, pid := range []string{"short", "AAAAAAAAAAAAAAAAAAAAAAA", "AAAAAAAAAAAAAAAAAAAA!A", "AAAAAAAAAAAAAAAAAAAA%2E%2E"} {
		wantErrCode(t, e.do("GET", "/play/"+pid, ""), 404, "not_found")
	}
	wantErrCode(t, e.do("GET", "/play/AAAAAAAAAAAAAAAAAAAAAA", ""), 404, "not_found")
}

func TestPlayRouteIsPublic(t *testing.T) {
	for _, r := range Routes() {
		if r.Path == "/play/{pid}" && (r.Method != "GET" || r.Scope != "") {
			t.Errorf("route = %+v", r)
		}
	}
}

func TestNewSecret(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s := newSecret()
		if !pidPattern.MatchString(s) {
			t.Fatalf("secret %q is not 22 base64url chars", s)
		}
		if seen[s] {
			t.Fatalf("duplicate secret %q", s)
		}
		seen[s] = true
	}
}

type failingGetBlob struct{ *blob.Fake }

func (failingGetBlob) Get(context.Context, string) ([]byte, map[string]string, error) {
	return nil, nil, errors.New("AccessDenied: secret detail")
}

func TestGetPlayerPage_BlobErrorIs404AndLogged(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	h := New(Deps{Store: store.NewMemory(), Blob: failingGetBlob{blob.NewFake()}, Now: func() time.Time { return fixedNow }})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/play/AAAAAAAAAAAAAAAAAAAAAA", nil))
	wantErrCode(t, rec, 404, "not_found")
	if !strings.Contains(buf.String(), "play: blob get failed") {
		t.Errorf("not logged: %q", buf.String())
	}
	if strings.Contains(buf.String(), "AAAAAAAAAAAAAAAAAAAAAA") || strings.Contains(buf.String(), "secret detail") {
		t.Errorf("log leaks key or error text: %q", buf.String())
	}
}

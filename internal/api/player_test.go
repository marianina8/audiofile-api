package api

import (
	"strings"
	"testing"

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
	if u, _ := m["url"].(string); !strings.Contains(u, "players/"+id+"/") || !strings.HasSuffix(u, "?ttl=3600") {
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
		if !strings.HasPrefix(key, "players/"+id+"/") || !strings.HasSuffix(key, ".html") {
			t.Errorf("key = %s", key)
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
		for _, want := range []string{`name="viewport"`, `content="noindex"`, `content="no-referrer"`, `<audio controls`, `preload="auto"`, `new Audio()`, `nextAudio`, `Loading`, `load this track`} {
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

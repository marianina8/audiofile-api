package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marianina8/audiofile-api/internal/blob"
	"github.com/marianina8/audiofile-api/internal/model"
	"github.com/marianina8/audiofile-api/internal/store"
)

var fixedNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

type env struct {
	h  http.Handler
	st *store.Memory
	bl *blob.Fake
}

func newEnv() *env {
	e := &env{st: store.NewMemory(), bl: blob.NewFake()}
	n := 0
	e.h = New(Deps{
		Store: e.st, Blob: e.bl,
		Now:   func() time.Time { return fixedNow },
		NewID: func() string { n++; return fmt.Sprintf("id%d", n) },
	})
	return e
}

func (e *env) do(method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	e.h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return m
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, code int) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, code, rec.Body.String())
	}
}

func wantErrCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	wantStatus(t, rec, status)
	var b errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil || b.Error.Code != code {
		t.Fatalf("error body = %s, want code %s", rec.Body.String(), code)
	}
}

func TestCreateUpload(t *testing.T) {
	e := newEnv()
	rec := e.do("POST", "/tracks/uploads", `{"filename":"Song.mp3","size_bytes":1234,"content_sha256":"abc"}`)
	wantStatus(t, rec, 201)
	m := decode(t, rec)
	if m["track_id"] != "id1" || m["upload_method"] != "PUT" {
		t.Errorf("body = %v", m)
	}
	if u, _ := m["upload_url"].(string); u != "https://fake/tracks/id1.mp3?ttl=900" {
		t.Errorf("upload_url = %v", m["upload_url"])
	}
	h, _ := m["upload_headers"].(map[string]any)
	if h["Content-Type"] != "audio/mpeg" || h["Content-Length"] != "1234" {
		t.Errorf("upload_headers = %v", m["upload_headers"])
	}
	if m["expires_at"] != "2026-10-20T12:15:00Z" {
		t.Errorf("expires_at = %v", m["expires_at"])
	}
	tr, err := e.st.GetTrack(context.Background(), "id1")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Status != model.StatusProcessing || tr.S3Key != "tracks/id1.mp3" || tr.OriginalFilename != "Song.mp3" ||
		tr.SizeBytes != 1234 || tr.ContentSHA256 != "abc" || tr.CreatedAt != "2026-10-20T12:00:00Z" {
		t.Errorf("track = %+v", tr)
	}
}

func TestCreateUpload_Validation(t *testing.T) {
	cases := map[string]string{
		"not mp3":       `{"filename":"a.wav","size_bytes":10}`,
		"zero size":     `{"filename":"a.mp3","size_bytes":0}`,
		"negative size": `{"filename":"a.mp3","size_bytes":-5}`,
		"too big":       `{"filename":"a.mp3","size_bytes":52428801}`,
		"content type":  `{"filename":"a.mp3","size_bytes":10,"content_type":"audio/wav"}`,
		"bad json":      `{`,
		"unknown field": `{"filename":"a.mp3","size_bytes":10,"x":1}`,
		"missing":       `{}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv()
			wantErrCode(t, e.do("POST", "/tracks/uploads", body), 400, "bad_request")
		})
	}
	e := newEnv()
	wantStatus(t, e.do("POST", "/tracks/uploads", `{"filename":"A.MP3","size_bytes":52428800,"content_type":"audio/mpeg"}`), 201)
}

func seedTrack(t *testing.T, e *env, id string, mut func(*model.Track)) model.Track {
	t.Helper()
	tr := model.Track{ID: id, Status: model.StatusReady, Title: "T " + id, S3Key: "tracks/" + id + ".mp3"}
	if mut != nil {
		mut(&tr)
	}
	if err := e.st.PutTrack(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestListTracks_Paging(t *testing.T) {
	e := newEnv()
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		seedTrack(t, e, id, nil)
	}
	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		path := "/tracks?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		rec := e.do("GET", path, "")
		wantStatus(t, rec, 200)
		m := decode(t, rec)
		items := m["items"].([]any)
		if len(items) > 2 {
			t.Fatalf("page has %d items", len(items))
		}
		for _, it := range items {
			seen[it.(map[string]any)["id"].(string)] = true
		}
		cursor, _ = m["next_cursor"].(string)
		if cursor == "" {
			break
		}
	}
	if cursor != "" || len(seen) != 5 {
		t.Errorf("cursor=%q seen=%v", cursor, seen)
	}
	wantErrCode(t, e.do("GET", "/tracks?limit=0", ""), 400, "bad_request")
	wantErrCode(t, e.do("GET", "/tracks?limit=101", ""), 400, "bad_request")
	wantErrCode(t, e.do("GET", "/tracks?limit=x", ""), 400, "bad_request")
	wantErrCode(t, e.do("GET", "/tracks?cursor=!!!bad!!!", ""), 400, "bad_request")
	wantStatus(t, e.do("GET", "/tracks", ""), 200)
}

func TestGetTrack_TagsNeverNull(t *testing.T) {
	e := newEnv()
	seedTrack(t, e, "a", nil)
	rec := e.do("GET", "/tracks/a", "")
	wantStatus(t, rec, 200)
	if !strings.Contains(rec.Body.String(), `"tags":[]`) {
		t.Errorf("body = %s", rec.Body.String())
	}
	rec = e.do("GET", "/tracks", "")
	if !strings.Contains(rec.Body.String(), `"tags":[]`) {
		t.Errorf("list body = %s", rec.Body.String())
	}
	wantErrCode(t, e.do("GET", "/tracks/nope", ""), 404, "not_found")
}

func TestPatchTrack(t *testing.T) {
	e := newEnv()
	seedTrack(t, e, "a", func(tr *model.Track) {
		tr.Artist = "Old"
		tr.ArtistID = "old"
		tr.BPM = 100
		tr.BPMSource = model.BPMTag
	})
	ctx := context.Background()

	wantStatus(t, e.do("PATCH", "/tracks/a", `{"bpm":128}`), 200)
	tr, _ := e.st.GetTrack(ctx, "a")
	if tr.BPM != 128 || tr.BPMSource != model.BPMManual || tr.UpdatedAt != "2026-10-20T12:00:00Z" {
		t.Errorf("track = %+v", tr)
	}

	rec := e.do("PATCH", "/tracks/a", `{"artist":"New Artist","album":"LP","year":1999,"title":"Hi","genre":"House","lyrics":"la"}`)
	wantStatus(t, rec, 200)
	if decode(t, rec)["artist_id"] != "new-artist" {
		t.Errorf("body = %s", rec.Body.String())
	}
	tr, _ = e.st.GetTrack(ctx, "a")
	if tr.ArtistID != "new-artist" || tr.AlbumID != "new-artist--lp" || tr.Title != "Hi" || tr.Year != 1999 || tr.Genre != "House" {
		t.Errorf("track = %+v", tr)
	}
	if a, err := e.st.GetArtist(ctx, "new-artist"); err != nil || a.Name != "New Artist" {
		t.Errorf("artist = %+v, %v", a, err)
	}
	if al, err := e.st.GetAlbum(ctx, "new-artist--lp"); err != nil || al.Year != 1999 {
		t.Errorf("album = %+v, %v", al, err)
	}

	wantStatus(t, e.do("PATCH", "/tracks/a", `{"album":""}`), 200)
	tr, _ = e.st.GetTrack(ctx, "a")
	if tr.Album != "" || tr.AlbumID != "" {
		t.Errorf("album not cleared: %+v", tr)
	}

	wantErrCode(t, e.do("PATCH", "/tracks/a", `{"nope":1}`), 400, "bad_request")
	wantErrCode(t, e.do("PATCH", "/tracks/a", `{"bpm":19}`), 400, "bad_request")
	wantErrCode(t, e.do("PATCH", "/tracks/a", `{"bpm":301}`), 400, "bad_request")
	wantStatus(t, e.do("PATCH", "/tracks/a", `{"bpm":20}`), 200)
	wantStatus(t, e.do("PATCH", "/tracks/a", `{"bpm":300}`), 200)
	wantErrCode(t, e.do("PATCH", "/tracks/a", `{"title":""}`), 400, "bad_request")
	wantErrCode(t, e.do("PATCH", "/tracks/a", `{"year":-1}`), 400, "bad_request")
	wantErrCode(t, e.do("PATCH", "/tracks/zzz", `{"bpm":128}`), 404, "not_found")
}

func TestDeleteTrack_RemovesFromPlaylists(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		seedTrack(t, e, id, nil)
		_ = e.bl.Put(ctx, "tracks/"+id+".mp3", "audio/mpeg", []byte("x"))
	}
	_ = e.st.PutPlaylist(ctx, model.Playlist{ID: "p1", Name: "P", TrackIDs: []string{"a", "b", "c"}})
	_ = e.st.PutPlaylist(ctx, model.Playlist{ID: "p2", Name: "Q", TrackIDs: []string{"a"}})

	wantStatus(t, e.do("DELETE", "/tracks/b", ""), 204)
	p, _ := e.st.GetPlaylist(ctx, "p1")
	if strings.Join(p.TrackIDs, ",") != "a,c" || p.UpdatedAt != "2026-10-20T12:00:00Z" {
		t.Errorf("p1 = %+v", p)
	}
	if p2, _ := e.st.GetPlaylist(ctx, "p2"); p2.UpdatedAt != "" {
		t.Errorf("p2 was rewritten: %+v", p2)
	}
	if _, ok := e.bl.Objects["tracks/b.mp3"]; ok {
		t.Error("blob not deleted")
	}
	if _, ok := e.bl.Objects["tracks/a.mp3"]; !ok {
		t.Error("other blob deleted")
	}
	if _, err := e.st.GetTrack(ctx, "b"); err == nil {
		t.Error("track not deleted")
	}
	wantErrCode(t, e.do("DELETE", "/tracks/b", ""), 404, "not_found")
}

func TestStream(t *testing.T) {
	e := newEnv()
	seedTrack(t, e, "a", nil)
	rec := e.do("GET", "/tracks/a/stream", "")
	wantStatus(t, rec, 200)
	m := decode(t, rec)
	if u, _ := m["url"].(string); !strings.Contains(u, "tracks/a.mp3") || !strings.Contains(u, "ttl=900") {
		t.Errorf("url = %v", m["url"])
	}
	if m["expires_at"] != "2026-10-20T12:15:00Z" {
		t.Errorf("expires_at = %v", m["expires_at"])
	}
	wantErrCode(t, e.do("GET", "/tracks/nope/stream", ""), 404, "not_found")
}

func TestStatus(t *testing.T) {
	e := newEnv()
	seedTrack(t, e, "f", func(tr *model.Track) { tr.Status = model.StatusFailed; tr.Error = "bad mp3" })
	seedTrack(t, e, "r", nil)
	m := decode(t, e.do("GET", "/tracks/f/status", ""))
	if m["id"] != "f" || m["status"] != "failed" || m["error"] != "bad mp3" {
		t.Errorf("body = %v", m)
	}
	m = decode(t, e.do("GET", "/tracks/r/status", ""))
	if _, has := m["error"]; has || m["status"] != "ready" {
		t.Errorf("body = %v", m)
	}
	wantErrCode(t, e.do("GET", "/tracks/nope/status", ""), 404, "not_found")
}

type failingStore struct {
	*store.Memory
}

func (failingStore) GetTrack(context.Context, string) (model.Track, error) {
	return model.Track{}, errors.New("secret dynamo detail")
}

func (failingStore) ListTracks(context.Context, int, string) ([]model.Track, string, error) {
	return nil, "", errors.New("secret dynamo detail")
}

func TestStoreFailureIs500(t *testing.T) {
	h := New(Deps{Store: failingStore{store.NewMemory()}, Blob: blob.NewFake(), Now: func() time.Time { return fixedNow }, NewID: func() string { return "x" }})
	for _, path := range []string{"/tracks/a", "/tracks?cursor=abc", "/tracks"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		wantErrCode(t, rec, 500, "internal")
		if strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("%s leaks error text: %s", path, rec.Body.String())
		}
	}
}

package api

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/marianina8/audiofile-api/internal/model"
)

func seedReady(t *testing.T, e *env, ids ...string) {
	t.Helper()
	for _, id := range ids {
		seedTrack(t, e, id, nil)
	}
}

func idsOf(t *testing.T, m map[string]any) []string {
	t.Helper()
	raw, ok := m["track_ids"].([]any)
	if !ok {
		t.Fatalf("track_ids = %v, want array", m["track_ids"])
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.(string))
	}
	return out
}

func newPlaylist(t *testing.T, e *env, ids ...string) string {
	t.Helper()
	b := `{"name":"Mix","track_ids":["` + strings.Join(ids, `","`) + `"]}`
	if len(ids) == 0 {
		b = `{"name":"Mix"}`
	}
	rec := e.do("POST", "/playlists", b)
	wantStatus(t, rec, 201)
	return decode(t, rec)["id"].(string)
}

func TestCreatePlaylist(t *testing.T) {
	e := newEnv()
	seedReady(t, e, "a", "b")
	seedTrack(t, e, "p", func(tr *model.Track) { tr.Status = model.StatusProcessing })

	for _, body := range []string{`{}`, `{"name":""}`, `{"name":"   "}`, `{"name":"x","bogus":1}`,
		`{"name":"` + strings.Repeat("n", 201) + `"}`, `{"name":"x","description":"` + strings.Repeat("d", 2001) + `"}`} {
		wantErrCode(t, e.do("POST", "/playlists", body), 400, "bad_request")
	}
	rec := e.do("POST", "/playlists", `{"name":"  Run  "}`)
	wantStatus(t, rec, 201)
	m := decode(t, rec)
	if m["name"] != "Run" || m["track_count"] != float64(0) || m["created_at"] != "2026-10-20T12:00:00Z" || m["updated_at"] != m["created_at"] {
		t.Errorf("created = %v", m)
	}
	if got := idsOf(t, m); len(got) != 0 {
		t.Errorf("track_ids = %v", got)
	}
	wantStatus(t, e.do("POST", "/playlists", `{"name":"`+strings.Repeat("n", 200)+`"}`), 201)

	// with tracks: validated like add, duplicates collapse
	rec = e.do("POST", "/playlists", `{"name":"M","description":"d","track_ids":["b","a","b"]}`)
	wantStatus(t, rec, 201)
	m = decode(t, rec)
	if got := idsOf(t, m); !reflect.DeepEqual(got, []string{"b", "a"}) || m["track_count"] != float64(2) {
		t.Errorf("created = %v", m)
	}
	before, _ := e.st.ListPlaylists(context.Background())
	rec = e.do("POST", "/playlists", `{"name":"M","track_ids":["a","nope","p"]}`)
	wantErrCode(t, rec, 400, "bad_request")
	after, _ := e.st.ListPlaylists(context.Background())
	if len(after) != len(before) {
		t.Error("playlist created despite bad ids")
	}
}

func TestAddPlaylistTracks(t *testing.T) {
	e := newEnv()
	seedReady(t, e, "a", "b", "c", "d", "x", "y", "z")
	id := newPlaylist(t, e, "a", "b")
	path := "/playlists/" + id + "/tracks"

	rec := e.do("POST", path, `{"track_ids":["c","a"]}`)
	wantStatus(t, rec, 200)
	if got := idsOf(t, decode(t, rec)); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("after add = %v", got)
	}

	rec = e.do("POST", path, `{"track_ids":["d"],"position":0}`)
	wantStatus(t, rec, 200)
	m := decode(t, rec)
	if got := idsOf(t, m); !reflect.DeepEqual(got, []string{"d", "a", "b", "c"}) || m["track_count"] != float64(4) {
		t.Fatalf("after position 0 = %v", m)
	}

	// middle insert keeps request order; duplicates in request collapse
	rec = e.do("POST", path, `{"track_ids":["x","y","x","a"],"position":2}`)
	wantStatus(t, rec, 200)
	if got := idsOf(t, decode(t, rec)); !reflect.DeepEqual(got, []string{"d", "a", "x", "y", "b", "c"}) {
		t.Fatalf("after middle = %v", got)
	}

	// beyond length appends
	rec = e.do("POST", path, `{"track_ids":["z"],"position":99}`)
	wantStatus(t, rec, 200)
	if got := idsOf(t, decode(t, rec)); !reflect.DeepEqual(got, []string{"d", "a", "x", "y", "b", "c", "z"}) {
		t.Fatalf("after beyond = %v", got)
	}

	// stored and updated_at set
	p, _ := e.st.GetPlaylist(context.Background(), id)
	if len(p.TrackIDs) != 7 || p.UpdatedAt != "2026-10-20T12:00:00Z" {
		t.Errorf("stored = %+v", p)
	}

	for _, body := range []string{`{}`, `{"track_ids":[]}`, `{"track_ids":["a"],"position":-1}`, `{"track_ids":["a"],"extra":1}`} {
		wantErrCode(t, e.do("POST", path, body), 400, "bad_request")
	}
	wantErrCode(t, e.do("POST", "/playlists/nope/tracks", `{"track_ids":["a"]}`), 404, "not_found")
}

func TestAddPlaylistTracks_NoOpKeepsUpdatedAt(t *testing.T) {
	e := newEnv()
	seedReady(t, e, "a")
	_ = e.st.PutPlaylist(context.Background(), model.Playlist{ID: "p", Name: "P", TrackIDs: []string{"a"}, UpdatedAt: "old"})
	rec := e.do("POST", "/playlists/p/tracks", `{"track_ids":["a"]}`)
	wantStatus(t, rec, 200)
	if got := decode(t, rec)["updated_at"]; got != "old" {
		t.Errorf("updated_at = %v", got)
	}
}

func TestAddPlaylistTracks_RejectsNotReady(t *testing.T) {
	e := newEnv()
	seedReady(t, e, "a", "b")
	seedTrack(t, e, "proc", func(tr *model.Track) { tr.Status = model.StatusProcessing })
	seedTrack(t, e, "bad", func(tr *model.Track) { tr.Status = model.StatusFailed })
	id := newPlaylist(t, e, "a")

	rec := e.do("POST", "/playlists/"+id+"/tracks", `{"track_ids":["b","bad","ghost","proc"]}`)
	wantErrCode(t, rec, 400, "bad_request")
	msg := decode(t, rec)["error"].(map[string]any)["message"].(string)
	if i, j, k := strings.Index(msg, "bad"), strings.Index(msg, "ghost"), strings.Index(msg, "proc"); i < 0 || j < i || k < j {
		t.Errorf("message %q should list bad, ghost, proc in order", msg)
	}
	p, _ := e.st.GetPlaylist(context.Background(), id)
	if !reflect.DeepEqual(p.TrackIDs, []string{"a"}) {
		t.Errorf("playlist changed on failure: %v", p.TrackIDs)
	}
}

func TestReorder(t *testing.T) {
	e := newEnv()
	seedReady(t, e, "a", "b", "c")
	id := newPlaylist(t, e, "a", "b", "c")
	path := "/playlists/" + id + "/tracks/order"

	rec := e.do("PUT", path, `{"track_ids":["c","a","b"]}`)
	wantStatus(t, rec, 200)
	if got := idsOf(t, decode(t, rec)); !reflect.DeepEqual(got, []string{"c", "a", "b"}) {
		t.Errorf("order = %v", got)
	}
	p, _ := e.st.GetPlaylist(context.Background(), id)
	if !reflect.DeepEqual(p.TrackIDs, []string{"c", "a", "b"}) {
		t.Errorf("stored = %v", p.TrackIDs)
	}
	for _, body := range []string{
		`{"track_ids":["c","a"]}`,         // missing
		`{"track_ids":["c","a","b","d"]}`, // extra
		`{"track_ids":["c","a","a"]}`,     // duplicate replacing a member
		`{"track_ids":["c","a","b","b"]}`, // duplicate extra
		`{"track_ids":["c","a","z"]}`,     // wrong member
		`{}`, `{"track_ids":[]}`, `{"x":1}`,
	} {
		wantErrCode(t, e.do("PUT", path, body), 400, "bad_request")
	}
	p, _ = e.st.GetPlaylist(context.Background(), id)
	if !reflect.DeepEqual(p.TrackIDs, []string{"c", "a", "b"}) {
		t.Errorf("changed by bad reorder: %v", p.TrackIDs)
	}
	wantErrCode(t, e.do("PUT", "/playlists/nope/tracks/order", `{"track_ids":[]}`), 404, "not_found")

	// empty playlist accepts an empty permutation
	empty := newPlaylist(t, e)
	wantStatus(t, e.do("PUT", "/playlists/"+empty+"/tracks/order", `{"track_ids":[]}`), 200)
}

func TestRemovePlaylistTrack(t *testing.T) {
	e := newEnv()
	seedReady(t, e, "a", "b", "c")
	id := newPlaylist(t, e, "a", "b", "c")
	wantStatus(t, e.do("DELETE", "/playlists/"+id+"/tracks/b", ""), 204)
	p, _ := e.st.GetPlaylist(context.Background(), id)
	if !reflect.DeepEqual(p.TrackIDs, []string{"a", "c"}) || p.UpdatedAt != "2026-10-20T12:00:00Z" {
		t.Errorf("stored = %+v", p)
	}
	wantErrCode(t, e.do("DELETE", "/playlists/"+id+"/tracks/b", ""), 404, "not_found")
	wantErrCode(t, e.do("DELETE", "/playlists/nope/tracks/a", ""), 404, "not_found")
}

func TestListPlaylistTracks_Order(t *testing.T) {
	e := newEnv()
	seedReady(t, e, "a", "b", "c")
	_ = e.st.PutPlaylist(context.Background(), model.Playlist{ID: "p", Name: "P", TrackIDs: []string{"c", "gone", "a", "b"}})
	rec := e.do("GET", "/playlists/p/tracks", "")
	wantStatus(t, rec, 200)
	var got []string
	for _, it := range decode(t, rec)["items"].([]any) {
		m := it.(map[string]any)
		got = append(got, m["id"].(string))
		if m["title"] == nil || m["tags"] == nil {
			t.Errorf("not a full track view: %v", m)
		}
	}
	if !reflect.DeepEqual(got, []string{"c", "a", "b"}) {
		t.Errorf("order = %v", got)
	}
	_ = e.st.PutPlaylist(context.Background(), model.Playlist{ID: "q", Name: "Q"})
	if items := decode(t, e.do("GET", "/playlists/q/tracks", ""))["items"]; !reflect.DeepEqual(items, []any{}) {
		t.Errorf("empty items = %#v", items)
	}
	wantErrCode(t, e.do("GET", "/playlists/nope/tracks", ""), 404, "not_found")
}

func TestListGetPlaylists(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	_ = e.st.PutPlaylist(ctx, model.Playlist{ID: "z", Name: "Z", CreatedAt: "2026-01-01T00:00:00Z"})
	_ = e.st.PutPlaylist(ctx, model.Playlist{ID: "b", Name: "B", CreatedAt: "2026-02-01T00:00:00Z", TrackIDs: []string{"t"}})
	_ = e.st.PutPlaylist(ctx, model.Playlist{ID: "a", Name: "A", CreatedAt: "2026-02-01T00:00:00Z"})
	var got []string
	for _, it := range decode(t, e.do("GET", "/playlists", ""))["items"].([]any) {
		m := it.(map[string]any)
		got = append(got, m["id"].(string))
		if _, has := m["track_ids"]; has {
			t.Error("summary has track_ids")
		}
	}
	if !reflect.DeepEqual(got, []string{"z", "a", "b"}) {
		t.Errorf("order = %v", got)
	}
	m := decode(t, e.do("GET", "/playlists/b", ""))
	if m["track_count"] != float64(1) || !reflect.DeepEqual(idsOf(t, m), []string{"t"}) {
		t.Errorf("detail = %v", m)
	}
	if got := idsOf(t, decode(t, e.do("GET", "/playlists/z", ""))); len(got) != 0 {
		t.Errorf("nil track_ids = %v", got)
	}
	wantErrCode(t, e.do("GET", "/playlists/nope", ""), 404, "not_found")
}

func TestPatchDeletePlaylist(t *testing.T) {
	e := newEnv()
	id := newPlaylist(t, e)
	_ = e.st.PutPlaylist(context.Background(), model.Playlist{ID: id, Name: "Mix", Description: "keep", CreatedAt: "c", UpdatedAt: "c"})
	path := "/playlists/" + id

	rec := e.do("PATCH", path, `{"name":" New "}`)
	wantStatus(t, rec, 200)
	m := decode(t, rec)
	if m["name"] != "New" || m["description"] != "keep" || m["updated_at"] != "2026-10-20T12:00:00Z" || m["created_at"] != "c" {
		t.Errorf("patched = %v", m)
	}
	m = decode(t, e.do("PATCH", path, `{"description":""}`))
	if m["description"] != "" || m["name"] != "New" {
		t.Errorf("cleared description = %v", m)
	}
	for _, body := range []string{`{}`, `{"name":"  "}`, `{"name":null}`, `{"bogus":1}`, `{"name":"` + strings.Repeat("n", 201) + `"}`} {
		wantErrCode(t, e.do("PATCH", path, body), 400, "bad_request")
	}
	wantErrCode(t, e.do("PATCH", "/playlists/nope", `{"name":"x"}`), 404, "not_found")

	wantStatus(t, e.do("DELETE", path, ""), 204)
	wantErrCode(t, e.do("GET", path, ""), 404, "not_found")
	wantErrCode(t, e.do("DELETE", path, ""), 404, "not_found")
}

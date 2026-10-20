package api

import (
	"reflect"
	"testing"

	"github.com/marianina8/audiofile-api/internal/slug"
)

func TestListArtists(t *testing.T) {
	e := newEnv()
	seedLibrary(t, e)
	m := decode(t, e.do("GET", "/artists", ""))
	want := []any{
		map[string]any{"id": "beat-doctor", "name": "Beat Doctor", "track_count": 3.0},
		map[string]any{"id": "ghost", "name": "Ghost", "track_count": 0.0},
		map[string]any{"id": "nova", "name": "Nova", "track_count": 2.0},
	}
	if !reflect.DeepEqual(m["items"], want) {
		t.Errorf("items = %v, want %v", m["items"], want)
	}
}

func TestArtistEndpoints(t *testing.T) {
	e := newEnv()
	seedLibrary(t, e)
	a := decode(t, e.do("GET", "/artists/beat-doctor", ""))
	want := map[string]any{"id": "beat-doctor", "name": "Beat Doctor", "track_count": 3.0, "album_count": 2.0}
	if !reflect.DeepEqual(a, want) {
		t.Errorf("artist = %v, want %v", a, want)
	}
	if got := itemIDs(t, decode(t, e.do("GET", "/artists/beat-doctor/tracks", ""))); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("tracks = %v", got)
	}
	alb := decode(t, e.do("GET", "/artists/beat-doctor/albums", ""))["items"].([]any)
	if len(alb) != 2 || alb[0].(map[string]any)["title"] != "Echo Chamber" || alb[1].(map[string]any)["title"] != "Pulse" {
		t.Errorf("albums (by year) = %v", alb)
	}
	if got := decode(t, e.do("GET", "/artists/ghost", ""))["album_count"]; got != 0.0 {
		t.Errorf("ghost album_count = %v", got)
	}
	for _, p := range []string{"/artists/nope", "/artists/nope/tracks", "/artists/nope/albums"} {
		wantErrCode(t, e.do("GET", p, ""), 404, "not_found")
	}
}

func TestAlbums(t *testing.T) {
	e := newEnv()
	seedLibrary(t, e)
	pulse := slug.AlbumID("beat-doctor", "Pulse")
	items := decode(t, e.do("GET", "/albums", ""))["items"].([]any)
	var titles []string
	for _, it := range items {
		titles = append(titles, it.(map[string]any)["title"].(string))
	}
	if want := []string{"Echo Chamber", "Pulse", "Skies"}; !reflect.DeepEqual(titles, want) {
		t.Errorf("titles = %v, want %v", titles, want)
	}
	got := decode(t, e.do("GET", "/albums/"+pulse, ""))
	want := map[string]any{"id": pulse, "title": "Pulse", "artist_id": "beat-doctor", "artist": "Beat Doctor", "year": 2020.0, "track_count": 2.0}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("album = %v, want %v", got, want)
	}
	wantErrCode(t, e.do("GET", "/albums/nope", ""), 404, "not_found")
	wantErrCode(t, e.do("GET", "/albums/nope/tracks", ""), 404, "not_found")
}

func TestAlbumTracksOrdered(t *testing.T) {
	e := newEnv()
	seedLibrary(t, e)
	rec := e.do("GET", "/albums/"+slug.AlbumID("beat-doctor", "Pulse")+"/tracks", "")
	wantStatus(t, rec, 200)
	// b is track 1, a is track 2; f (track 3) is still processing.
	if got := itemIDs(t, decode(t, rec)); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Errorf("ids = %v", got)
	}
}

func TestGenres(t *testing.T) {
	e := newEnv()
	seedLibrary(t, e)
	seedTrack(t, e, "g", nil) // empty genre
	m := decode(t, e.do("GET", "/genres", ""))
	want := []any{
		map[string]any{"name": "Electronic", "track_count": 1.0},
		map[string]any{"name": "Pop", "track_count": 2.0},
		map[string]any{"name": "Rock", "track_count": 2.0},
	}
	if !reflect.DeepEqual(m["items"], want) {
		t.Errorf("items = %v, want %v", m["items"], want)
	}
}

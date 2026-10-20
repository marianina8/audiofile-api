package api

import (
	"context"
	"errors"
	"github.com/marianina8/audiofile-api/internal/blob"
	"github.com/marianina8/audiofile-api/internal/store"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/marianina8/audiofile-api/internal/model"
	"github.com/marianina8/audiofile-api/internal/slug"
)

// seedLibrary stores 6 tracks (one still processing) plus their artists and albums.
func seedLibrary(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	for _, a := range []model.Artist{{ID: "beat-doctor", Name: "Beat Doctor"}, {ID: "nova", Name: "Nova"}, {ID: "ghost", Name: "Ghost"}} {
		if err := e.st.EnsureArtist(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	pulse, echo, skies := slug.AlbumID("beat-doctor", "Pulse"), slug.AlbumID("beat-doctor", "Echo Chamber"), slug.AlbumID("nova", "Skies")
	for _, a := range []model.Album{
		{ID: pulse, Title: "Pulse", ArtistID: "beat-doctor", Artist: "Beat Doctor", Year: 2020},
		{ID: echo, Title: "Echo Chamber", ArtistID: "beat-doctor", Artist: "Beat Doctor", Year: 2018},
		{ID: skies, Title: "Skies", ArtistID: "nova", Artist: "Nova", Year: 2019},
	} {
		if err := e.st.EnsureAlbum(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	type row struct {
		id, title, artistID, artist, album, albumID, genre string
		year, tn                                           int
		bpm, energy                                        float64
		status                                             model.TrackStatus
		tags                                               []string
	}
	for _, r := range []row{
		{"a", "alpha", "beat-doctor", "Beat Doctor", "Pulse", pulse, "Rock", 2020, 2, 128, 0.8, model.StatusReady, []string{"gym", "loud"}},
		{"b", "Bravo", "beat-doctor", "Beat Doctor", "Pulse", pulse, "Rock", 2020, 1, 100, 0.9, model.StatusReady, []string{"gym"}},
		{"c", "Charlie", "beat-doctor", "Beat Doctor", "Echo Chamber", echo, "Electronic", 2018, 1, 140, 0.5, model.StatusReady, nil},
		{"d", "Delta", "nova", "Nova", "Skies", skies, "Pop", 2019, 1, 124, 0.7, model.StatusReady, nil},
		{"e", "Echo", "nova", "Nova", "Skies", skies, "Pop", 2019, 2, 90, 0.2, model.StatusReady, []string{"loud"}},
		{"f", "Foxtrot", "beat-doctor", "Beat Doctor", "Pulse", pulse, "Rock", 2020, 3, 130, 0.9, model.StatusProcessing, []string{"gym"}},
	} {
		seedTrack(t, e, r.id, func(tr *model.Track) {
			tr.Title, tr.ArtistID, tr.Artist, tr.Album, tr.AlbumID, tr.Genre = r.title, r.artistID, r.artist, r.album, r.albumID, r.genre
			tr.Year, tr.TrackNumber, tr.BPM, tr.Energy, tr.Status, tr.Tags = r.year, r.tn, r.bpm, r.energy, r.status, r.tags
		})
	}
}

// itemIDs returns the "id" of each element of body["items"], in order.
func itemIDs(t *testing.T, m map[string]any) []string {
	t.Helper()
	items, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("items missing or not an array: %v", m)
	}
	ids := []string{}
	for _, it := range items {
		ids = append(ids, it.(map[string]any)["id"].(string))
	}
	return ids
}

func TestSearch_Filters(t *testing.T) {
	e := newEnv()
	seedLibrary(t, e)
	cases := []struct {
		name, query string
		want        []string
	}{
		{"all ready, sorted by title ignoring case", "", []string{"a", "b", "c", "d", "e"}},
		{"bpm and energy", "bpm_min=120&energy_min=0.6", []string{"a", "d"}},
		{"bpm_max inclusive", "bpm_max=100", []string{"b", "e"}},
		{"energy_max", "energy_max=0.5", []string{"c", "e"}},
		{"genre case-insensitive", "genre=ROCK", []string{"a", "b"}},
		{"q matches artist", "q=doc", []string{"a", "b", "c"}},
		{"q matches title", "q=ALP", []string{"a"}},
		{"q matches album", "q=skies", []string{"d", "e"}},
		{"artist by id", "artist=beat-doctor", []string{"a", "b", "c"}},
		{"artist by name", "artist=Beat%20Doctor", []string{"a", "b", "c"}},
		{"artist by name lowercase", "artist=beat%20doctor", []string{"a", "b", "c"}},
		{"year", "year=2019", []string{"d", "e"}},
		{"combined", "q=o&genre=pop&year=2019", []string{"d", "e"}},
		{"min above max is empty", "bpm_min=200&bpm_max=100", []string{}},
		{"no match", "q=zzz", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := e.do("GET", "/search?"+c.query, "")
			wantStatus(t, rec, 200)
			if got := itemIDs(t, decode(t, rec)); !reflect.DeepEqual(got, c.want) {
				t.Errorf("ids = %v, want %v", got, c.want)
			}
		})
	}
}

func TestSearch_ReturnsTrackView(t *testing.T) {
	e := newEnv()
	seedLibrary(t, e)
	m := decode(t, e.do("GET", "/search?q=charlie", ""))
	it := m["items"].([]any)[0].(map[string]any)
	if it["title"] != "Charlie" || it["tags"] == nil {
		t.Errorf("item = %v", it)
	}
}

func TestSearch_ExcludesNotReady(t *testing.T) {
	e := newEnv()
	seedLibrary(t, e)
	for _, id := range itemIDs(t, decode(t, e.do("GET", "/search?genre=rock&bpm_min=129", ""))) {
		t.Errorf("unexpected %s", id)
	}
	for _, id := range itemIDs(t, decode(t, e.do("GET", "/search", ""))) {
		if id == "f" {
			t.Error("processing track returned")
		}
	}
}

func TestSearch_BadNumber(t *testing.T) {
	e := newEnv()
	for _, q := range []string{"bpm_min=abc", "bpm_max=x", "energy_min=%20", "energy_max=1,5", "year=2020.5", "year=abc", "limit=abc", "limit=0", "bpm_min=NaN"} {
		wantErrCode(t, e.do("GET", "/search?"+q, ""), 400, "bad_request")
	}
}

func TestSearch_Limit(t *testing.T) {
	e := newEnv()
	seedLibrary(t, e)
	if got := itemIDs(t, decode(t, e.do("GET", "/search?limit=2", ""))); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("limit=2 ids = %v", got)
	}
	wantStatus(t, e.do("GET", "/search?limit=100", ""), 200)
	wantErrCode(t, e.do("GET", "/search?limit=101", ""), 400, "bad_request")
}

type failingAll struct{ failingStore }

func (failingAll) AllTracks(context.Context) ([]model.Track, error) {
	return nil, errors.New("secret dynamo detail")
}

func TestAggregateStoreFailureIs500(t *testing.T) {
	h := New(Deps{Store: failingAll{failingStore{store.NewMemory()}}, Blob: blob.NewFake(), Now: func() time.Time { return fixedNow }, NewID: func() string { return "x" }})
	for _, path := range []string{"/search", "/artists", "/albums", "/genres", "/tags"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		wantErrCode(t, rec, 500, "internal")
		if strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("%s leaks error text", path)
		}
	}
}

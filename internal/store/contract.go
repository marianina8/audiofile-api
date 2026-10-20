package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/marianina8/audiofile-api/internal/model"
)

// RunContract runs the behaviours every Store implementation must satisfy.
// It lives in a non-test file so other packages' tests can call it.
func RunContract(t *testing.T, newStore func(t *testing.T) Store) {
	ctx := context.Background()

	t.Run("missing ids return ErrNotFound", func(t *testing.T) {
		s := newStore(t)
		checks := map[string]error{
			"GetTrack":       func() error { _, err := s.GetTrack(ctx, "x"); return err }(),
			"DeleteTrack":    s.DeleteTrack(ctx, "x"),
			"GetArtist":      func() error { _, err := s.GetArtist(ctx, "x"); return err }(),
			"GetAlbum":       func() error { _, err := s.GetAlbum(ctx, "x"); return err }(),
			"GetPlaylist":    func() error { _, err := s.GetPlaylist(ctx, "x"); return err }(),
			"DeletePlaylist": s.DeletePlaylist(ctx, "x"),
		}
		for name, err := range checks {
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("%s: err = %v, want ErrNotFound", name, err)
			}
		}
	})

	t.Run("track put get delete", func(t *testing.T) {
		s := newStore(t)
		if err := s.PutTrack(ctx, model.Track{ID: "a", Title: "One"}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutTrack(ctx, model.Track{ID: "a", Title: "Uno"}); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetTrack(ctx, "a")
		if err != nil || got.Title != "Uno" {
			t.Fatalf("got %+v, %v", got, err)
		}
		if err := s.DeleteTrack(ctx, "a"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetTrack(ctx, "a"); !errors.Is(err, ErrNotFound) {
			t.Errorf("after delete err = %v", err)
		}
	})

	t.Run("paging", func(t *testing.T) {
		s := newStore(t)
		for i := 1; i <= 5; i++ {
			if err := s.PutTrack(ctx, model.Track{ID: fmt.Sprintf("t%d", i)}); err != nil {
				t.Fatal(err)
			}
		}
		var sizes []int
		seen := map[string]bool{}
		cursor := ""
		for range 10 {
			page, next, err := s.ListTracks(ctx, 2, cursor)
			if err != nil {
				t.Fatal(err)
			}
			sizes = append(sizes, len(page))
			for _, tr := range page {
				if seen[tr.ID] {
					t.Errorf("duplicate %s", tr.ID)
				}
				seen[tr.ID] = true
			}
			if next == "" {
				break
			}
			cursor = next
		}
		if !slices.Equal(sizes, []int{2, 2, 1}) {
			t.Errorf("page sizes = %v, want [2 2 1]", sizes)
		}
		if len(seen) != 5 {
			t.Errorf("saw %d distinct tracks, want 5", len(seen))
		}
		all, err := s.AllTracks(ctx)
		if err != nil || len(all) != 5 {
			t.Errorf("AllTracks = %d, %v", len(all), err)
		}
	})

	t.Run("tracks by artist ordered by title", func(t *testing.T) {
		s := newStore(t)
		for _, tr := range []model.Track{
			{ID: "1", ArtistID: "ar", Title: "Charlie"},
			{ID: "2", ArtistID: "ar", Title: "Alpha"},
			{ID: "3", ArtistID: "ar", Title: "Bravo"},
			{ID: "4", ArtistID: "other", Title: "Aaa"},
		} {
			if err := s.PutTrack(ctx, tr); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.TracksByArtist(ctx, "ar")
		if err != nil {
			t.Fatal(err)
		}
		var titles []string
		for _, tr := range got {
			titles = append(titles, tr.Title)
		}
		if !slices.Equal(titles, []string{"Alpha", "Bravo", "Charlie"}) {
			t.Errorf("titles = %v", titles)
		}
	})

	t.Run("tracks by album ordered by track number", func(t *testing.T) {
		s := newStore(t)
		for _, tr := range []model.Track{
			{ID: "1", AlbumID: "al", TrackNumber: 3},
			{ID: "2", AlbumID: "al", TrackNumber: 1},
			{ID: "3", AlbumID: "al", TrackNumber: 2},
			{ID: "4", TrackNumber: 1}, // no album
			{ID: "5", AlbumID: "other", TrackNumber: 1},
		} {
			if err := s.PutTrack(ctx, tr); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.TracksByAlbum(ctx, "al")
		if err != nil {
			t.Fatal(err)
		}
		var nums []int
		for _, tr := range got {
			nums = append(nums, tr.TrackNumber)
		}
		if !slices.Equal(nums, []int{1, 2, 3}) {
			t.Errorf("track numbers = %v", nums)
		}
		none, err := s.TracksByAlbum(ctx, "")
		if err != nil || len(none) != 0 {
			t.Errorf("empty album id = %v, %v", none, err)
		}
	})

	t.Run("EnsureArtist never overwrites", func(t *testing.T) {
		s := newStore(t)
		if err := s.EnsureArtist(ctx, model.Artist{ID: "a", Name: "First"}); err != nil {
			t.Fatal(err)
		}
		if err := s.EnsureArtist(ctx, model.Artist{ID: "a", Name: "Second"}); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetArtist(ctx, "a")
		if err != nil || got.Name != "First" {
			t.Fatalf("got %+v, %v", got, err)
		}
		list, err := s.ListArtists(ctx)
		if err != nil || len(list) != 1 {
			t.Errorf("ListArtists = %v, %v", list, err)
		}
	})

	t.Run("albums", func(t *testing.T) {
		s := newStore(t)
		for _, a := range []model.Album{
			{ID: "1", ArtistID: "ar", Title: "New", Year: 2020},
			{ID: "2", ArtistID: "ar", Title: "Old", Year: 1990},
			{ID: "3", ArtistID: "ar", Title: "Mid", Year: 2005},
			{ID: "4", ArtistID: "other", Year: 1},
		} {
			if err := s.EnsureAlbum(ctx, a); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.EnsureAlbum(ctx, model.Album{ID: "1", Title: "Changed"}); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetAlbum(ctx, "1")
		if err != nil || got.Title != "New" {
			t.Fatalf("got %+v, %v", got, err)
		}
		by, err := s.AlbumsByArtist(ctx, "ar")
		if err != nil {
			t.Fatal(err)
		}
		var years []int
		for _, a := range by {
			years = append(years, a.Year)
		}
		if !slices.Equal(years, []int{1990, 2005, 2020}) {
			t.Errorf("years = %v", years)
		}
		all, err := s.ListAlbums(ctx)
		if err != nil || len(all) != 4 {
			t.Errorf("ListAlbums = %d, %v", len(all), err)
		}
	})

	t.Run("playlist keeps track order", func(t *testing.T) {
		s := newStore(t)
		want := []string{"c", "a", "b"}
		if err := s.PutPlaylist(ctx, model.Playlist{ID: "p", Name: "P", TrackIDs: want}); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetPlaylist(ctx, "p")
		if err != nil || !slices.Equal(got.TrackIDs, want) {
			t.Fatalf("got %+v, %v", got, err)
		}
		list, err := s.ListPlaylists(ctx)
		if err != nil || len(list) != 1 {
			t.Errorf("ListPlaylists = %v, %v", list, err)
		}
		if err := s.DeletePlaylist(ctx, "p"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetPlaylist(ctx, "p"); !errors.Is(err, ErrNotFound) {
			t.Errorf("after delete err = %v", err)
		}
	})

	t.Run("tags round trip", func(t *testing.T) {
		s := newStore(t)
		if err := s.PutTrack(ctx, model.Track{ID: "a", Tags: []string{"rock", "live"}}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutTrack(ctx, model.Track{ID: "b"}); err != nil {
			t.Fatal(err)
		}
		a, err := s.GetTrack(ctx, "a")
		if err != nil {
			t.Fatal(err)
		}
		got := slices.Clone(a.Tags)
		slices.Sort(got) // do not depend on set ordering
		if !slices.Equal(got, []string{"live", "rock"}) {
			t.Errorf("tags = %v", a.Tags)
		}
		b, err := s.GetTrack(ctx, "b")
		if err != nil {
			t.Fatal(err)
		}
		if len(b.Tags) != 0 {
			t.Errorf("nil tags came back as %v", b.Tags)
		}
	})

	t.Run("returned values are copies", func(t *testing.T) {
		s := newStore(t)
		if err := s.PutTrack(ctx, model.Track{ID: "a", Tags: []string{"x"}}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutPlaylist(ctx, model.Playlist{ID: "p", TrackIDs: []string{"a", "b"}}); err != nil {
			t.Fatal(err)
		}
		tr, err := s.GetTrack(ctx, "a")
		if err != nil || len(tr.Tags) != 1 {
			t.Fatalf("GetTrack = %+v, %v", tr, err)
		}
		tr.Tags[0] = "mutated"
		again, err := s.GetTrack(ctx, "a")
		if err != nil || len(again.Tags) != 1 {
			t.Fatalf("GetTrack again = %+v, %v", again, err)
		}
		if again.Tags[0] != "x" {
			t.Errorf("track tags mutated in store: %v", again.Tags)
		}
		pl, err := s.GetPlaylist(ctx, "p")
		if err != nil || len(pl.TrackIDs) != 2 {
			t.Fatalf("GetPlaylist = %+v, %v", pl, err)
		}
		pl.TrackIDs[0] = "mutated"
		pAgain, err := s.GetPlaylist(ctx, "p")
		if err != nil || len(pAgain.TrackIDs) != 2 {
			t.Fatalf("GetPlaylist again = %+v, %v", pAgain, err)
		}
		if pAgain.TrackIDs[0] != "a" {
			t.Errorf("playlist ids mutated in store: %v", pAgain.TrackIDs)
		}
		list, err := s.ListPlaylists(ctx)
		if err != nil || len(list) != 1 || len(list[0].TrackIDs) != 2 {
			t.Fatalf("ListPlaylists = %+v, %v", list, err)
		}
		list[0].TrackIDs[1] = "mutated"
		pAgain, err = s.GetPlaylist(ctx, "p")
		if err != nil || len(pAgain.TrackIDs) != 2 {
			t.Fatalf("GetPlaylist after list = %+v, %v", pAgain, err)
		}
		if pAgain.TrackIDs[1] != "b" {
			t.Errorf("playlist ids mutated via list: %v", pAgain.TrackIDs)
		}
		all, err := s.AllTracks(ctx)
		if err != nil || len(all) != 1 || len(all[0].Tags) != 1 {
			t.Fatalf("AllTracks = %+v, %v", all, err)
		}
		all[0].Tags[0] = "mutated"
		tAgain, err := s.GetTrack(ctx, "a")
		if err != nil || len(tAgain.Tags) != 1 {
			t.Fatalf("GetTrack after all = %+v, %v", tAgain, err)
		}
		if tAgain.Tags[0] != "x" {
			t.Errorf("track tags mutated via AllTracks: %v", tAgain.Tags)
		}
	})

	t.Run("list without limit returns everything", func(t *testing.T) {
		s := newStore(t)
		for i := 1; i <= 3; i++ {
			if err := s.PutTrack(ctx, model.Track{ID: fmt.Sprintf("t%d", i)}); err != nil {
				t.Fatal(err)
			}
		}
		got, next, err := s.ListTracks(ctx, 0, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 || next != "" {
			t.Errorf("got %d tracks, next %q; want 3 and empty", len(got), next)
		}
	})

	t.Run("garbage cursor is an error", func(t *testing.T) {
		s := newStore(t)
		if err := s.PutTrack(ctx, model.Track{ID: "a"}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.ListTracks(ctx, 2, "!!!not a cursor!!!"); err == nil {
			t.Error("expected an error for a garbage cursor")
		}
	})
}

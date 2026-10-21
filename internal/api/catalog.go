package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/marianina8/audiofile-api/internal/model"
)

type artistView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	TrackCount int    `json:"track_count"`
}

type artistDetail struct {
	artistView
	AlbumCount int `json:"album_count"`
}

type albumView struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	ArtistID   string `json:"artist_id"`
	Artist     string `json:"artist"`
	Year       int    `json:"year"`
	TrackCount int    `json:"track_count"`
}

type countView struct {
	Name       string `json:"name"`
	TrackCount int    `json:"track_count"`
}

func albumViews(albums []model.Album, ready []model.Track) []albumView {
	counts := map[string]int{}
	for _, t := range ready {
		if t.AlbumID != "" {
			counts[t.AlbumID]++
		}
	}
	out := make([]albumView, 0, len(albums))
	for _, a := range albums {
		out = append(out, albumView{a.ID, a.Title, a.ArtistID, a.Artist, a.Year, counts[a.ID]})
	}
	return out
}

func sortAlbumsByYear(as []model.Album) {
	sort.SliceStable(as, func(i, j int) bool {
		if as[i].Year != as[j].Year {
			return as[i].Year < as[j].Year
		}
		if a, b := strings.ToLower(as[i].Title), strings.ToLower(as[j].Title); a != b {
			return a < b
		}
		return as[i].ID < as[j].ID
	})
}

// readyTracks returns every ready track.
func (s *server) readyTracks(r *http.Request) ([]model.Track, error) {
	all, err := s.Store.AllTracks(r.Context())
	return readyOnly(all), err
}

func (s *server) listArtists(w http.ResponseWriter, r *http.Request) {
	artists, err := s.Store.ListArtists(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	ready, err := s.readyTracks(r)
	if err != nil {
		internalError(w)
		return
	}
	counts := map[string]int{}
	for _, t := range ready {
		counts[t.ArtistID]++
	}
	sort.SliceStable(artists, func(i, j int) bool {
		if a, b := strings.ToLower(artists[i].Name), strings.ToLower(artists[j].Name); a != b {
			return a < b
		}
		return artists[i].ID < artists[j].ID
	})
	items := make([]artistView, 0, len(artists))
	for _, a := range artists {
		if counts[a.ID] == 0 {
			continue // lists hide artists with no ready tracks; GET /artists/{id} still returns them
		}
		items = append(items, artistView{a.ID, a.Name, counts[a.ID]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) getArtist(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.GetArtist(r.Context(), r.PathValue("id"))
	if err != nil {
		storeError(w, err, "artist")
		return
	}
	tracks, err := s.Store.TracksByArtist(r.Context(), a.ID)
	if err != nil {
		internalError(w)
		return
	}
	albums, err := s.Store.AlbumsByArtist(r.Context(), a.ID)
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, artistDetail{artistView{a.ID, a.Name, len(readyOnly(tracks))}, len(albums)})
}

func (s *server) listArtistTracks(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.GetArtist(r.Context(), r.PathValue("id"))
	if err != nil {
		storeError(w, err, "artist")
		return
	}
	tracks, err := s.Store.TracksByArtist(r.Context(), a.ID)
	if err != nil {
		internalError(w)
		return
	}
	tracks = readyOnly(tracks)
	byTitle(tracks)
	writeJSON(w, http.StatusOK, s.trackItems(tracks))
}

func (s *server) listArtistAlbums(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.GetArtist(r.Context(), r.PathValue("id"))
	if err != nil {
		storeError(w, err, "artist")
		return
	}
	albums, err := s.Store.AlbumsByArtist(r.Context(), a.ID)
	if err != nil {
		internalError(w)
		return
	}
	ready, err := s.readyTracks(r)
	if err != nil {
		internalError(w)
		return
	}
	sortAlbumsByYear(albums)
	writeJSON(w, http.StatusOK, map[string]any{"items": albumViews(albums, ready)})
}

func (s *server) listAlbums(w http.ResponseWriter, r *http.Request) {
	albums, err := s.Store.ListAlbums(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	ready, err := s.readyTracks(r)
	if err != nil {
		internalError(w)
		return
	}
	sort.SliceStable(albums, func(i, j int) bool {
		x, y := albums[i], albums[j]
		if a, b := strings.ToLower(x.Artist), strings.ToLower(y.Artist); a != b {
			return a < b
		}
		if x.Year != y.Year {
			return x.Year < y.Year
		}
		if a, b := strings.ToLower(x.Title), strings.ToLower(y.Title); a != b {
			return a < b
		}
		return x.ID < y.ID
	})
	items := albumViews(albums, ready)
	nonEmpty := items[:0]
	for _, a := range items {
		if a.TrackCount > 0 { // lists hide albums with no ready tracks
			nonEmpty = append(nonEmpty, a)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonEmpty})
}

func (s *server) getAlbum(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.GetAlbum(r.Context(), r.PathValue("id"))
	if err != nil {
		storeError(w, err, "album")
		return
	}
	tracks, err := s.Store.TracksByAlbum(r.Context(), a.ID)
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, albumViews([]model.Album{a}, readyOnly(tracks))[0])
}

func (s *server) listAlbumTracks(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.GetAlbum(r.Context(), r.PathValue("id"))
	if err != nil {
		storeError(w, err, "album")
		return
	}
	tracks, err := s.Store.TracksByAlbum(r.Context(), a.ID)
	if err != nil {
		internalError(w)
		return
	}
	tracks = readyOnly(tracks)
	sort.SliceStable(tracks, func(i, j int) bool {
		x, y := tracks[i], tracks[j]
		if x.TrackNumber != y.TrackNumber {
			return x.TrackNumber < y.TrackNumber
		}
		if a, b := strings.ToLower(x.Title), strings.ToLower(y.Title); a != b {
			return a < b
		}
		return x.ID < y.ID
	})
	writeJSON(w, http.StatusOK, s.trackItems(tracks))
}

// countItems renders name -> count as a list sorted by name.
func countItems(counts map[string]int) []countView {
	items := make([]countView, 0, len(counts))
	for n, c := range counts {
		items = append(items, countView{n, c})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (s *server) listGenres(w http.ResponseWriter, r *http.Request) {
	ready, err := s.readyTracks(r)
	if err != nil {
		internalError(w)
		return
	}
	counts := map[string]int{}
	for _, t := range ready {
		if t.Genre != "" {
			counts[t.Genre]++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": countItems(counts)})
}

func (s *server) listTags(w http.ResponseWriter, r *http.Request) {
	ready, err := s.readyTracks(r)
	if err != nil {
		internalError(w)
		return
	}
	counts := map[string]int{}
	for _, t := range ready {
		for _, tag := range t.Tags {
			counts[tag]++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": countItems(counts)})
}

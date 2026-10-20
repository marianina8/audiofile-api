package api

import (
	"net/http"
	"time"

	"github.com/marianina8/audiofile-api/internal/blob"
	"github.com/marianina8/audiofile-api/internal/store"
)

// Deps are the collaborators handlers need.
type Deps struct {
	Store store.Store
	Blob  blob.Blob
	Now   func() time.Time
	NewID func() string
}

type server struct {
	Deps
}

// New returns the HTTP handler serving every route in Routes().
func New(d Deps) http.Handler {
	s := &server{Deps: d}
	handlers := map[string]http.HandlerFunc{
		"GET /health":                             s.health,
		"POST /tracks/uploads":                    s.createTrackUpload,
		"GET /tracks":                             s.listTracks,
		"GET /tracks/{id}":                        s.getTrack,
		"PATCH /tracks/{id}":                      s.updateTrack,
		"DELETE /tracks/{id}":                     s.deleteTrack,
		"GET /tracks/{id}/stream":                 s.getTrackStream,
		"GET /tracks/{id}/status":                 s.getTrackStatus,
		"GET /search":                             s.searchTracks,
		"GET /artists":                            s.listArtists,
		"GET /artists/{id}":                       s.getArtist,
		"GET /artists/{id}/tracks":                s.listArtistTracks,
		"GET /artists/{id}/albums":                s.listArtistAlbums,
		"GET /albums":                             s.listAlbums,
		"GET /albums/{id}":                        s.getAlbum,
		"GET /albums/{id}/tracks":                 s.listAlbumTracks,
		"GET /genres":                             s.listGenres,
		"GET /tags":                               s.listTags,
		"POST /tracks/{id}/tags":                  s.addTrackTags,
		"DELETE /tracks/{id}/tags/{tag}":          s.removeTrackTag,
		"POST /playlists":                         s.createPlaylist,
		"GET /playlists":                          s.listPlaylists,
		"GET /playlists/{id}":                     s.getPlaylist,
		"PATCH /playlists/{id}":                   s.updatePlaylist,
		"DELETE /playlists/{id}":                  s.deletePlaylist,
		"GET /playlists/{id}/tracks":              s.listPlaylistTracks,
		"POST /playlists/{id}/tracks":             s.addPlaylistTracks,
		"DELETE /playlists/{id}/tracks/{trackId}": s.removePlaylistTrack,
		"PUT /playlists/{id}/tracks/order":        s.reorderPlaylistTracks,
	}

	mux := http.NewServeMux()
	for _, r := range Routes() {
		key := r.Method + " " + r.Path
		h, ok := handlers[key]
		if !ok {
			panic("api: no handler for route " + key)
		}
		mux.HandleFunc(key, h)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such route")
	})
	return mux
}

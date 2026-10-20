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
		"GET /health":             s.health,
		"POST /tracks/uploads":    s.createTrackUpload,
		"GET /tracks":             s.listTracks,
		"GET /tracks/{id}":        s.getTrack,
		"PATCH /tracks/{id}":      s.updateTrack,
		"DELETE /tracks/{id}":     s.deleteTrack,
		"GET /tracks/{id}/stream": s.getTrackStream,
		"GET /tracks/{id}/status": s.getTrackStatus,
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

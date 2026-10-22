package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/marianina8/audiofile-api/internal/model"
	"github.com/marianina8/audiofile-api/internal/store"
)

// maxEditAttempts bounds the read-modify-write retries on a version conflict.
const maxEditAttempts = 5

// errUnchanged tells editPlaylist the edit is a no-op: nothing is written.
var errUnchanged = errors.New("unchanged")

// apiError is a client error an edit function rejects the request with.
type apiError struct {
	status    int
	code, msg string
}

func (e *apiError) Error() string { return e.msg }

// editPlaylist applies edit to the stored playlist and writes it back on the
// version it read, re-reading and retrying when another writer got there
// first. p is the playlist already read, if any (zero ID: read it here). It
// returns store.ErrConflict once maxEditAttempts writes have all lost.
func (s *server) editPlaylist(ctx context.Context, id string, p model.Playlist, edit func(*model.Playlist) error) (model.Playlist, error) {
	for attempt := range maxEditAttempts {
		if attempt > 0 || p.ID == "" {
			var err error
			if p, err = s.Store.GetPlaylist(ctx, id); err != nil {
				return p, err
			}
		}
		switch err := edit(&p); {
		case errors.Is(err, errUnchanged):
			return p, nil
		case err != nil:
			return p, err
		}
		prev := p.Version
		err := s.Store.UpdatePlaylist(ctx, p, prev)
		if errors.Is(err, store.ErrConflict) {
			continue
		}
		if err != nil {
			return p, err
		}
		p.Version = prev + 1
		return p, nil
	}
	return p, store.ErrConflict
}

// editError writes the response for an editPlaylist error.
func editError(w http.ResponseWriter, err error) {
	var ae *apiError
	switch {
	case errors.As(err, &ae):
		writeError(w, ae.status, ae.code, ae.msg)
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "playlist is being changed by another request; try again")
	default:
		storeError(w, err, "playlist")
	}
}

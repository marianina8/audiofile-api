package api

import (
	"bytes"
	_ "embed"
	"errors"
	"html/template"
	"math"
	"net/http"
	"time"

	"github.com/marianina8/audiofile-api/internal/model"
	"github.com/marianina8/audiofile-api/internal/store"
)

const (
	playerPageTTL  = time.Hour
	playerTrackTTL = 75 * time.Minute // slightly longer than the page
	htmlType       = "text/html; charset=utf-8"
)

//go:embed player.html.tmpl
var playerSource string

var playerTmpl = template.Must(template.New("player").
	Funcs(template.FuncMap{"inc": func(i int) int { return i + 1 }}).
	Parse(playerSource))

// playerTrack is one entry on the player page; its json tags name the fields
// the inline script reads.
type playerTrack struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
	BPM    int    `json:"bpm"`
	URL    string `json:"url"`
}

type playerPage struct {
	Name   string
	Tracks []playerTrack
}

func (s *server) createPlaylistPlayer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := s.Store.GetPlaylist(ctx, r.PathValue("id"))
	if err != nil {
		storeError(w, err, "playlist")
		return
	}
	page := playerPage{Name: p.Name}
	for _, id := range p.TrackIDs {
		t, err := s.Store.GetTrack(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			internalError(w)
			return
		}
		if t.Status != model.StatusReady {
			continue
		}
		u, err := s.Blob.PresignGet(ctx, t.S3Key, playerTrackTTL)
		if err != nil {
			internalError(w)
			return
		}
		page.Tracks = append(page.Tracks, playerTrack{
			Title: t.Title, Artist: t.Artist, BPM: int(math.Round(t.BPM)), URL: u,
		})
	}
	if len(page.Tracks) == 0 {
		badRequest(w, "playlist has no playable tracks")
		return
	}

	var buf bytes.Buffer
	if err := playerTmpl.Execute(&buf, page); err != nil {
		internalError(w)
		return
	}
	key := "players/" + p.ID + "/" + s.NewID() + ".html"
	if err := s.Blob.Put(ctx, key, htmlType, buf.Bytes()); err != nil {
		internalError(w)
		return
	}
	u, err := s.Blob.PresignGet(ctx, key, playerPageTTL)
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"url":        u,
		"expires_at": s.Now().UTC().Add(playerPageTTL).Format(time.RFC3339),
	})
}

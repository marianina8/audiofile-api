package api

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"errors"
	"html/template"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/marianina8/audiofile-api/internal/blob"
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
	pid := s.NewSecret()
	expires := s.Now().UTC().Add(playerPageTTL).Format(time.RFC3339)
	err = s.Blob.PutWithMetadata(ctx, playerKey(pid), htmlType, buf.Bytes(),
		map[string]string{"expires-at": expires})
	if err != nil {
		internalError(w)
		return
	}
	base := s.PublicBaseURL
	if base == "" {
		base = "https://" + r.Host
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"url":        strings.TrimRight(base, "/") + "/play/" + pid,
		"expires_at": expires,
	})
}

func playerKey(pid string) string { return "players/" + pid + ".html" }

var pidPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// newSecret returns 16 crypto/rand bytes as 22 base64url characters.
func newSecret() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("api: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

const playerCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
	"media-src https://*.amazonaws.com; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// getPlayerPage serves a stored player page. Unknown, malformed and expired
// links all answer the same 404.
func (s *server) getPlayerPage(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	if !pidPattern.MatchString(pid) {
		notFound(w)
		return
	}
	body, meta, err := s.Blob.Get(r.Context(), playerKey(pid))
	if errors.Is(err, blob.ErrNotFound) {
		notFound(w)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	exp, err := time.Parse(time.RFC3339, meta["expires-at"])
	if err != nil || !s.Now().Before(exp) {
		notFound(w)
		return
	}
	h := w.Header()
	h.Set("Content-Type", htmlType)
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", playerCSP)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func notFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "not_found", "player not found")
}

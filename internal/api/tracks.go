package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/marianina8/audiofile-api/internal/blob"
	"github.com/marianina8/audiofile-api/internal/catalog"
	"github.com/marianina8/audiofile-api/internal/model"
	"github.com/marianina8/audiofile-api/internal/store"
)

const (
	maxUploadBytes  = 52428800
	presignTTL      = 15 * time.Minute
	defaultPageSize = 50
	maxPageSize     = 100
	mp3Type         = "audio/mpeg"
	minBPM, maxBPM  = 20, 300
)

// trackView is the JSON shape of a track: model.Track with tags never null.
func (s *server) trackView(t model.Track) map[string]any {
	b, _ := json.Marshal(t) // Track contains only marshalable fields
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["tags"] = t.JSONTags()
	return m
}

// ensureCatalog creates the artist and album rows for a track's tags.
func (s *server) ensureCatalog(ctx context.Context, artist, album string, year int) (string, string, error) {
	return catalog.Ensure(ctx, s.Store, s.Now(), artist, album, year)
}

func internalError(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, "internal", "internal error")
}

// storeError maps a store failure to a response: not found stays 404, anything
// else is a generic 500.
func storeError(w http.ResponseWriter, err error, what string) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", what+" not found")
		return
	}
	internalError(w)
}

func badRequest(w http.ResponseWriter, msg string) {
	writeError(w, http.StatusBadRequest, "bad_request", msg)
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		badRequest(w, "invalid JSON body")
		return false
	}
	return true
}

func (s *server) createTrackUpload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filename      string `json:"filename"`
		SizeBytes     int64  `json:"size_bytes"`
		ContentSHA256 string `json:"content_sha256"`
		ContentType   string `json:"content_type"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	switch {
	case !strings.HasSuffix(strings.ToLower(req.Filename), ".mp3"):
		badRequest(w, "filename must end in .mp3")
		return
	case req.SizeBytes <= 0 || req.SizeBytes > maxUploadBytes:
		badRequest(w, "size_bytes must be between 1 and 52428800")
		return
	case req.ContentType != "" && req.ContentType != mp3Type:
		badRequest(w, "content_type must be audio/mpeg")
		return
	}

	id := s.NewID()
	key := "tracks/" + id + ".mp3"
	url, headers, err := s.Blob.PresignPut(r.Context(), key, mp3Type, req.SizeBytes, presignTTL)
	if err != nil {
		internalError(w)
		return
	}
	now := s.Now().UTC()
	ts := now.Format(time.RFC3339)
	t := model.Track{
		ID: id, Status: model.StatusProcessing, S3Key: key,
		OriginalFilename: req.Filename, SizeBytes: req.SizeBytes, ContentSHA256: req.ContentSHA256,
		CreatedAt: ts, UpdatedAt: ts,
	}
	if err := s.Store.PutTrack(r.Context(), t); err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"track_id":       id,
		"upload_url":     url,
		"upload_method":  "PUT",
		"upload_headers": headers,
		"expires_at":     now.Add(presignTTL).Format(time.RFC3339),
	})
}

func (s *server) listTracks(w http.ResponseWriter, r *http.Request) {
	limit := defaultPageSize
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxPageSize {
			badRequest(w, "limit must be between 1 and 100")
			return
		}
		limit = n
	}
	cursor := r.URL.Query().Get("cursor")
	tracks, next, err := s.Store.ListTracks(r.Context(), limit, cursor)
	if err != nil {
		if errors.Is(err, store.ErrInvalidCursor) {
			badRequest(w, "invalid cursor")
			return
		}
		internalError(w)
		return
	}
	items := make([]map[string]any, 0, len(tracks))
	for _, t := range tracks {
		items = append(items, s.trackView(t))
	}
	body := map[string]any{"items": items}
	if next != "" {
		body["next_cursor"] = next
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *server) getTrack(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.GetTrack(r.Context(), r.PathValue("id"))
	if err != nil {
		storeError(w, err, "track")
		return
	}
	writeJSON(w, http.StatusOK, s.trackView(t))
}

func (s *server) updateTrack(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title  *string  `json:"title"`
		Artist *string  `json:"artist"`
		Album  *string  `json:"album"`
		Genre  *string  `json:"genre"`
		Year   *int     `json:"year"`
		Lyrics *string  `json:"lyrics"`
		BPM    *float64 `json:"bpm"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	switch {
	case req.Title != nil && strings.TrimSpace(*req.Title) == "":
		badRequest(w, "title must not be empty")
		return
	case req.Artist != nil && strings.TrimSpace(*req.Artist) == "":
		badRequest(w, "artist must not be empty")
		return
	case req.Year != nil && *req.Year < 0:
		badRequest(w, "year must not be negative")
		return
	case req.BPM != nil && (*req.BPM < minBPM || *req.BPM > maxBPM):
		badRequest(w, "bpm must be between 20 and 300")
		return
	}

	ctx := r.Context()
	t, err := s.Store.GetTrack(ctx, r.PathValue("id"))
	if err != nil {
		storeError(w, err, "track")
		return
	}
	if req.Title != nil {
		t.Title = *req.Title
	}
	if req.Genre != nil {
		t.Genre = *req.Genre
	}
	if req.Lyrics != nil {
		t.Lyrics = *req.Lyrics
	}
	if req.Year != nil {
		t.Year = *req.Year
	}
	if req.BPM != nil {
		t.BPM = *req.BPM
		t.BPMSource = model.BPMManual
	}
	if req.Artist != nil || req.Album != nil {
		if req.Artist != nil {
			t.Artist = *req.Artist
		}
		if req.Album != nil {
			t.Album = *req.Album
		}
		t.ArtistID, t.AlbumID, err = s.ensureCatalog(ctx, t.Artist, t.Album, t.Year)
		if err != nil {
			internalError(w)
			return
		}
	}
	t.UpdatedAt = s.Now().UTC().Format(time.RFC3339)
	if err := s.Store.PutTrack(ctx, t); err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, s.trackView(t))
}

func (s *server) deleteTrack(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	t, err := s.Store.GetTrack(ctx, id)
	if err != nil {
		storeError(w, err, "track")
		return
	}
	playlists, err := s.Store.ListPlaylists(ctx)
	if err != nil {
		internalError(w)
		return
	}
	ts := s.Now().UTC().Format(time.RFC3339)
	for _, p := range playlists {
		if !slices.Contains(p.TrackIDs, id) {
			continue
		}
		_, err := s.editPlaylist(ctx, p.ID, p, func(p *model.Playlist) error {
			if !slices.Contains(p.TrackIDs, id) {
				return errUnchanged
			}
			p.TrackIDs = slices.DeleteFunc(slices.Clone(p.TrackIDs), func(x string) bool { return x == id })
			p.UpdatedAt = ts
			return nil
		})
		switch {
		case errors.Is(err, store.ErrNotFound): // deleted meanwhile; nothing to clean up
		case err != nil:
			editError(w, err)
			return
		}
	}
	if t.S3Key != "" {
		if err := s.Blob.Delete(ctx, t.S3Key); err != nil && !errors.Is(err, blob.ErrNotFound) {
			internalError(w)
			return
		}
	}
	if err := s.Store.DeleteTrack(ctx, id); err != nil {
		storeError(w, err, "track")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getTrackStream answers with a short single-track player link rather than a
// raw presigned URL, which breaks when a client appends query parameters.
func (s *server) getTrackStream(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.GetTrack(r.Context(), r.PathValue("id"))
	if err != nil {
		storeError(w, err, "track")
		return
	}
	if t.Status != model.StatusReady {
		writeError(w, http.StatusConflict, "conflict", "track is not ready")
		return
	}
	url, expires, err := s.publishPlayer(r, t.Title, []model.Track{t})
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url, "expires_at": expires})
}

func (s *server) getTrackStatus(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.GetTrack(r.Context(), r.PathValue("id"))
	if err != nil {
		storeError(w, err, "track")
		return
	}
	body := map[string]any{"id": t.ID, "status": t.Status}
	if t.Error != "" {
		body["error"] = t.Error
	}
	writeJSON(w, http.StatusOK, body)
}

package api

import (
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/marianina8/audiofile-api/internal/model"
	"github.com/marianina8/audiofile-api/internal/store"
)

const (
	maxPlaylistName = 200
	maxPlaylistDesc = 2000
)

func playlistSummary(p model.Playlist) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "description": p.Description,
		"track_count": len(p.TrackIDs), "created_at": p.CreatedAt, "updated_at": p.UpdatedAt,
	}
}

func playlistDetail(p model.Playlist) map[string]any {
	m := playlistSummary(p)
	ids := p.TrackIDs
	if ids == nil {
		ids = []string{}
	}
	m["track_ids"] = ids
	return m
}

// validName trims and checks a playlist name; msg is non-empty on failure.
func validName(raw string) (name, msg string) {
	name = strings.TrimSpace(raw)
	switch {
	case name == "":
		return "", "name must not be empty"
	case utf8.RuneCountInString(name) > maxPlaylistName:
		return "", "name must be at most 200 characters"
	}
	return name, ""
}

func validDescription(d string) string {
	if utf8.RuneCountInString(d) > maxPlaylistDesc {
		return "description must be at most 2000 characters"
	}
	return ""
}

// checkTrackIDs de-duplicates ids (first occurrence wins) and verifies each is
// an existing, ready track. It writes the error response and returns ok=false
// on failure. Bad ids are listed in request order.
func (s *server) checkTrackIDs(w http.ResponseWriter, r *http.Request, ids []string) ([]string, bool) {
	var uniq, bad []string
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
		t, err := s.Store.GetTrack(r.Context(), id)
		switch {
		case errors.Is(err, store.ErrNotFound):
			bad = append(bad, id)
		case err != nil:
			internalError(w)
			return nil, false
		case t.Status != model.StatusReady:
			bad = append(bad, id)
		}
	}
	if len(bad) > 0 {
		badRequest(w, "unknown or not ready track ids: "+strings.Join(bad, ", "))
		return nil, false
	}
	return uniq, true
}

func (s *server) now() string { return s.Now().UTC().Format(time.RFC3339) }

func (s *server) createPlaylist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		TrackIDs    []string `json:"track_ids"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	name, msg := validName(req.Name)
	if msg == "" {
		msg = validDescription(req.Description)
	}
	if msg != "" {
		badRequest(w, msg)
		return
	}
	ids, ok := s.checkTrackIDs(w, r, req.TrackIDs)
	if !ok {
		return
	}
	ts := s.now()
	p := model.Playlist{
		ID: s.NewID(), Name: name, Description: req.Description,
		TrackIDs: ids, CreatedAt: ts, UpdatedAt: ts, Version: 1,
	}
	if err := s.Store.PutPlaylist(r.Context(), p); err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusCreated, playlistDetail(p))
}

func (s *server) listPlaylists(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Store.ListPlaylists(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].CreatedAt != ps[j].CreatedAt {
			return ps[i].CreatedAt < ps[j].CreatedAt
		}
		return ps[i].ID < ps[j].ID
	})
	items := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		items = append(items, playlistSummary(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) getPlaylist(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.GetPlaylist(r.Context(), r.PathValue("id"))
	if err != nil {
		storeError(w, err, "playlist")
		return
	}
	writeJSON(w, http.StatusOK, playlistDetail(p))
}

func (s *server) updatePlaylist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Name == nil && req.Description == nil {
		badRequest(w, "provide name and/or description")
		return
	}
	var name string
	if req.Name != nil {
		var msg string
		if name, msg = validName(*req.Name); msg != "" {
			badRequest(w, msg)
			return
		}
	}
	if req.Description != nil {
		if msg := validDescription(*req.Description); msg != "" {
			badRequest(w, msg)
			return
		}
	}
	p, err := s.editPlaylist(r.Context(), r.PathValue("id"), model.Playlist{}, func(p *model.Playlist) error {
		if req.Name != nil {
			p.Name = name
		}
		if req.Description != nil {
			p.Description = *req.Description
		}
		p.UpdatedAt = s.now()
		return nil
	})
	if err != nil {
		editError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, playlistDetail(p))
}

func (s *server) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeletePlaylist(r.Context(), r.PathValue("id")); err != nil {
		storeError(w, err, "playlist")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listPlaylistTracks(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.GetPlaylist(r.Context(), r.PathValue("id"))
	if err != nil {
		storeError(w, err, "playlist")
		return
	}
	items := make([]map[string]any, 0, len(p.TrackIDs))
	for _, id := range p.TrackIDs {
		t, err := s.Store.GetTrack(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			internalError(w)
			return
		}
		items = append(items, s.trackView(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) addPlaylistTracks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TrackIDs []string `json:"track_ids"`
		Position *int     `json:"position"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if len(req.TrackIDs) == 0 {
		badRequest(w, "track_ids must be a non-empty array")
		return
	}
	if req.Position != nil && *req.Position < 0 {
		badRequest(w, "position must not be negative")
		return
	}
	p, err := s.Store.GetPlaylist(r.Context(), r.PathValue("id"))
	if err != nil {
		storeError(w, err, "playlist")
		return
	}
	ids, ok := s.checkTrackIDs(w, r, req.TrackIDs)
	if !ok {
		return
	}
	p, err = s.editPlaylist(r.Context(), p.ID, p, func(p *model.Playlist) error {
		fresh := slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return slices.Contains(p.TrackIDs, id) })
		if len(fresh) == 0 {
			return errUnchanged
		}
		pos := len(p.TrackIDs)
		if req.Position != nil && *req.Position < pos {
			pos = *req.Position
		}
		p.TrackIDs = slices.Insert(slices.Clone(p.TrackIDs), pos, fresh...)
		p.UpdatedAt = s.now()
		return nil
	})
	if err != nil {
		editError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, playlistDetail(p))
}

func (s *server) removePlaylistTrack(w http.ResponseWriter, r *http.Request) {
	trackID := r.PathValue("trackId")
	_, err := s.editPlaylist(r.Context(), r.PathValue("id"), model.Playlist{}, func(p *model.Playlist) error {
		if !slices.Contains(p.TrackIDs, trackID) {
			return &apiError{http.StatusNotFound, "not_found", "track not in playlist"}
		}
		p.TrackIDs = slices.DeleteFunc(slices.Clone(p.TrackIDs), func(x string) bool { return x == trackID })
		p.UpdatedAt = s.now()
		return nil
	})
	if err != nil {
		editError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) reorderPlaylistTracks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TrackIDs []string `json:"track_ids"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.TrackIDs == nil {
		badRequest(w, "track_ids is required")
		return
	}
	got := slices.Clone(req.TrackIDs)
	slices.Sort(got)
	p, err := s.editPlaylist(r.Context(), r.PathValue("id"), model.Playlist{}, func(p *model.Playlist) error {
		want := slices.Clone(p.TrackIDs)
		slices.Sort(want)
		if !slices.Equal(want, got) {
			return &apiError{http.StatusBadRequest, "bad_request", "track_ids must contain each current playlist track exactly once"}
		}
		p.TrackIDs = slices.Clone(req.TrackIDs)
		p.UpdatedAt = s.now()
		return nil
	})
	if err != nil {
		editError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, playlistDetail(p))
}

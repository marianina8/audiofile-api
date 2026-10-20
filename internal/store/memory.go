package store

import (
	"cmp"
	"context"
	"encoding/base64"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"

	"github.com/marianina8/audiofile-api/internal/model"
)

// Memory is a mutex-guarded in-memory Store. Values are copied on the way in
// and out so callers cannot mutate stored state.
type Memory struct {
	mu        sync.RWMutex
	tracks    map[string]model.Track
	artists   map[string]model.Artist
	albums    map[string]model.Album
	playlists map[string]model.Playlist
}

var _ Store = (*Memory)(nil)

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		tracks:    map[string]model.Track{},
		artists:   map[string]model.Artist{},
		albums:    map[string]model.Album{},
		playlists: map[string]model.Playlist{},
	}
}

func cloneTrack(t model.Track) model.Track {
	t.Tags = slices.Clone(t.Tags)
	return t
}

func clonePlaylist(p model.Playlist) model.Playlist {
	p.TrackIDs = slices.Clone(p.TrackIDs)
	return p
}

func (m *Memory) PutTrack(_ context.Context, t model.Track) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tracks[t.ID] = cloneTrack(t)
	return nil
}

func (m *Memory) GetTrack(_ context.Context, id string) (model.Track, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tracks[id]
	if !ok {
		return model.Track{}, fmt.Errorf("track %q: %w", id, ErrNotFound)
	}
	return cloneTrack(t), nil
}

func (m *Memory) DeleteTrack(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tracks[id]; !ok {
		return fmt.Errorf("track %q: %w", id, ErrNotFound)
	}
	delete(m.tracks, id)
	return nil
}

// tracksWhere returns copies of matching tracks sorted by cmpFn, ties by id.
func (m *Memory) tracksWhere(keep func(model.Track) bool, cmpFn func(a, b model.Track) int) []model.Track {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []model.Track{}
	for _, t := range m.tracks {
		if keep(t) {
			out = append(out, cloneTrack(t))
		}
	}
	slices.SortFunc(out, func(a, b model.Track) int {
		if c := cmpFn(a, b); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

func byID(a, b model.Track) int { return cmp.Compare(a.ID, b.ID) }

// ListTracks returns tracks in id order. limit<=0 means no limit. The cursor
// is an opaque base64 of the last id returned; next=="" at the end.
func (m *Memory) ListTracks(_ context.Context, limit int, cursor string) ([]model.Track, string, error) {
	after := ""
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
		after = string(b)
	}
	all := m.tracksWhere(func(t model.Track) bool { return cursor == "" || t.ID > after }, byID)
	if limit <= 0 || len(all) <= limit {
		return all, "", nil
	}
	page := all[:limit]
	return page, base64.RawURLEncoding.EncodeToString([]byte(page[limit-1].ID)), nil
}

func (m *Memory) AllTracks(_ context.Context) ([]model.Track, error) {
	return m.tracksWhere(func(model.Track) bool { return true }, byID), nil
}

func (m *Memory) TracksByArtist(_ context.Context, artistID string) ([]model.Track, error) {
	return m.tracksWhere(
		func(t model.Track) bool { return t.ArtistID == artistID },
		func(a, b model.Track) int { return cmp.Compare(a.Title, b.Title) },
	), nil
}

func (m *Memory) TracksByAlbum(_ context.Context, albumID string) ([]model.Track, error) {
	return m.tracksWhere(
		func(t model.Track) bool { return albumID != "" && t.AlbumID == albumID },
		func(a, b model.Track) int { return cmp.Compare(a.TrackNumber, b.TrackNumber) },
	), nil
}

func (m *Memory) EnsureArtist(_ context.Context, a model.Artist) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.artists[a.ID]; !ok {
		m.artists[a.ID] = a
	}
	return nil
}

func (m *Memory) GetArtist(_ context.Context, id string) (model.Artist, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.artists[id]
	if !ok {
		return model.Artist{}, fmt.Errorf("artist %q: %w", id, ErrNotFound)
	}
	return a, nil
}

func (m *Memory) ListArtists(_ context.Context) ([]model.Artist, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := slices.Collect(maps.Values(m.artists))
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Memory) EnsureAlbum(_ context.Context, a model.Album) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.albums[a.ID]; !ok {
		m.albums[a.ID] = a
	}
	return nil
}

func (m *Memory) GetAlbum(_ context.Context, id string) (model.Album, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.albums[id]
	if !ok {
		return model.Album{}, fmt.Errorf("album %q: %w", id, ErrNotFound)
	}
	return a, nil
}

func (m *Memory) ListAlbums(_ context.Context) ([]model.Album, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := slices.Collect(maps.Values(m.albums))
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Memory) AlbumsByArtist(_ context.Context, artistID string) ([]model.Album, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []model.Album{}
	for _, a := range m.albums {
		if a.ArtistID == artistID {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b model.Album) int {
		if c := cmp.Compare(a.Year, b.Year); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (m *Memory) PutPlaylist(_ context.Context, p model.Playlist) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.playlists[p.ID] = clonePlaylist(p)
	return nil
}

func (m *Memory) GetPlaylist(_ context.Context, id string) (model.Playlist, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.playlists[id]
	if !ok {
		return model.Playlist{}, fmt.Errorf("playlist %q: %w", id, ErrNotFound)
	}
	return clonePlaylist(p), nil
}

func (m *Memory) ListPlaylists(_ context.Context) ([]model.Playlist, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.Playlist, 0, len(m.playlists))
	for _, p := range m.playlists {
		out = append(out, clonePlaylist(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Memory) DeletePlaylist(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.playlists[id]; !ok {
		return fmt.Errorf("playlist %q: %w", id, ErrNotFound)
	}
	delete(m.playlists, id)
	return nil
}

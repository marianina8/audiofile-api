// Package store defines the persistence interface and an in-memory implementation.
package store

import (
	"context"
	"errors"

	"github.com/marianina8/audiofile-api/internal/model"
)

// ErrNotFound is returned (wrapped or bare) when an entity does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned by UpdatePlaylist when the stored version is not the
// expected one: another writer got there first. Re-read and retry.
var ErrConflict = errors.New("conflict")

// ErrInvalidCursor is returned by ListTracks for a cursor it cannot decode.
var ErrInvalidCursor = errors.New("invalid cursor")

// Store is the persistence boundary for tracks, artists, albums and playlists.
type Store interface {
	PutTrack(ctx context.Context, t model.Track) error
	GetTrack(ctx context.Context, id string) (model.Track, error)
	DeleteTrack(ctx context.Context, id string) error
	// ListTracks pages tracks in a stable but unspecified order. limit<=0 means no limit (callers
	// that want to reject it must validate first). next=="" at the end.
	ListTracks(ctx context.Context, limit int, cursor string) ([]model.Track, string, error)
	AllTracks(ctx context.Context) ([]model.Track, error)
	TracksByArtist(ctx context.Context, artistID string) ([]model.Track, error) // by title
	TracksByAlbum(ctx context.Context, albumID string) ([]model.Track, error)   // by track_number
	EnsureArtist(ctx context.Context, a model.Artist) error                     // create if absent; never overwrites
	GetArtist(ctx context.Context, id string) (model.Artist, error)
	ListArtists(ctx context.Context) ([]model.Artist, error)
	EnsureAlbum(ctx context.Context, a model.Album) error
	GetAlbum(ctx context.Context, id string) (model.Album, error)
	ListAlbums(ctx context.Context) ([]model.Album, error)
	AlbumsByArtist(ctx context.Context, artistID string) ([]model.Album, error) // by year
	PutPlaylist(ctx context.Context, p model.Playlist) error                    // unconditional; for new playlists
	// UpdatePlaylist replaces an existing playlist only if its stored version is
	// expectedVersion (a missing version counts as 0), writing p with version
	// expectedVersion+1. ErrConflict on a version mismatch, ErrNotFound if absent.
	UpdatePlaylist(ctx context.Context, p model.Playlist, expectedVersion int) error
	GetPlaylist(ctx context.Context, id string) (model.Playlist, error)
	ListPlaylists(ctx context.Context) ([]model.Playlist, error)
	DeletePlaylist(ctx context.Context, id string) error
}

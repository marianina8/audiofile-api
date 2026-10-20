// Package catalog keeps the artists and albums tables in step with track tags.
package catalog

import (
	"context"
	"time"

	"github.com/marianina8/audiofile-api/internal/model"
	"github.com/marianina8/audiofile-api/internal/slug"
	"github.com/marianina8/audiofile-api/internal/store"
)

// Ensure creates the artist and, when album is non-empty, the album if they do
// not exist yet, and returns their ids. An empty album yields albumID "".
func Ensure(ctx context.Context, st store.Store, now time.Time, artist, album string, year int) (artistID, albumID string, err error) {
	ts := now.UTC().Format(time.RFC3339)
	artistID = slug.Make(artist)
	if err := st.EnsureArtist(ctx, model.Artist{ID: artistID, Name: artist, CreatedAt: ts}); err != nil {
		return "", "", err
	}
	if album == "" {
		return artistID, "", nil
	}
	albumID = slug.AlbumID(artistID, album)
	err = st.EnsureAlbum(ctx, model.Album{
		ID: albumID, Title: album, ArtistID: artistID, Artist: artist, Year: year, CreatedAt: ts,
	})
	if err != nil {
		return "", "", err
	}
	return artistID, albumID, nil
}

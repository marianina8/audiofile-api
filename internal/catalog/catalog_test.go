package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/marianina8/audiofile-api/internal/store"
)

var now = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func TestEnsure(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	aid, bid, err := Ensure(ctx, st, now, "Beat Doctor", "Best Bytes", 2024)
	if err != nil {
		t.Fatal(err)
	}
	if aid != "beat-doctor" || bid != "beat-doctor--best-bytes" {
		t.Fatalf("ids = %q, %q", aid, bid)
	}
	a, err := st.GetArtist(ctx, aid)
	if err != nil || a.Name != "Beat Doctor" || a.CreatedAt != "2026-10-20T12:00:00Z" {
		t.Errorf("artist = %+v, %v", a, err)
	}
	al, err := st.GetAlbum(ctx, bid)
	if err != nil || al.Title != "Best Bytes" || al.ArtistID != aid || al.Year != 2024 {
		t.Errorf("album = %+v, %v", al, err)
	}
}

func TestEnsure_NoAlbum(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	aid, bid, err := Ensure(ctx, st, now, "Solo", "", 0)
	if err != nil || aid != "solo" || bid != "" {
		t.Fatalf("got %q, %q, %v", aid, bid, err)
	}
	albums, _ := st.ListAlbums(ctx)
	if len(albums) != 0 {
		t.Errorf("albums = %v, want none", albums)
	}
}

func TestEnsure_Idempotent(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	if _, _, err := Ensure(ctx, st, now, "A", "B", 2000); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Ensure(ctx, st, now.Add(time.Hour), "A", "B", 2001); err != nil {
		t.Fatal(err)
	}
	al, _ := st.GetAlbum(ctx, "a--b")
	if al.Year != 2000 || al.CreatedAt != "2026-10-20T12:00:00Z" {
		t.Errorf("album overwritten: %+v", al)
	}
}

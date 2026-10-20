// Package processor analyses an uploaded track: tags, energy, BPM, catalog links.
package processor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/marianina8/audiofile-api/internal/audio"
	"github.com/marianina8/audiofile-api/internal/blob"
	"github.com/marianina8/audiofile-api/internal/catalog"
	"github.com/marianina8/audiofile-api/internal/model"
	"github.com/marianina8/audiofile-api/internal/store"
)

// Analyzer is the audio tooling the processor needs; audio.Tools satisfies it.
type Analyzer interface {
	Probe(ctx context.Context, path string) (audio.Probe, error)
	IntegratedLUFS(ctx context.Context, path string) (float64, error)
	DecodeMono(ctx context.Context, path string, start, dur float64, rate int) ([]float32, error)
}

var _ Analyzer = audio.Tools{}

// Processor turns an uploaded object into a ready (or failed) track.
type Processor struct {
	Store    store.Store
	Blob     blob.Blob
	Analyzer Analyzer
	Now      func() time.Time
	TmpDir   string
}

// keyRe matches the object key. The key must already be URL-decoded: the Lambda
// entrypoint owns url.QueryUnescape of S3 event keys.
var keyRe = regexp.MustCompile(`^tracks/([^/]+)\.mp3$`)

const (
	decodeRate = 22050
	decodeSecs = 60.0
)

// Process handles one uploaded key. It returns nil after recording a failed
// track and an error only when the store cannot be written. key must be
// URL-decoded; the Lambda entrypoint owns url.QueryUnescape of S3 event keys.
func (p *Processor) Process(ctx context.Context, key string) error {
	m := keyRe.FindStringSubmatch(key)
	if m == nil {
		slog.Info("processor: ignoring key", "key", key)
		return nil
	}
	id := m[1]
	now := p.now().UTC().Format(time.RFC3339)

	t, err := p.Store.GetTrack(ctx, id)
	existed := err == nil
	if errors.Is(err, store.ErrNotFound) {
		t = model.Track{ID: id, S3Key: key, OriginalFilename: id + ".mp3", CreatedAt: now}
	} else if err != nil {
		return fmt.Errorf("get track: %w", err)
	}
	t.Status = model.StatusProcessing
	t.Error = ""

	if step, err := p.analyse(ctx, &t, id, key); err != nil {
		t.Status = model.StatusFailed
		t.Error = fmt.Sprintf("%s: %v", step, err)
	} else {
		t.Status = model.StatusReady
	}
	t.UpdatedAt = now

	// Processing takes seconds; a user may have tagged or PATCHed meanwhile.
	// The re-read and PutTrack are not atomic; a small race window remains.
	fresh, err := p.Store.GetTrack(ctx, id)
	switch {
	case err == nil:
		t.Tags = fresh.Tags
		if fresh.BPMSource == model.BPMManual {
			t.BPM, t.BPMSource = fresh.BPM, fresh.BPMSource
		}
	case errors.Is(err, store.ErrNotFound) && existed:
		slog.Info("track deleted during processing; not saving", "id", id)
		return nil
	case errors.Is(err, store.ErrNotFound):
		// created by this run; nothing to merge
	default:
		return fmt.Errorf("re-read track: %w", err)
	}
	if err := p.Store.PutTrack(ctx, t); err != nil {
		return fmt.Errorf("put track: %w", err)
	}
	slog.Info("processed track", "id", id, "status", t.Status, "bpm", t.BPM,
		"bpm_source", t.BPMSource, "energy", t.Energy, "error", t.Error)
	return nil
}

func (p *Processor) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// analyse fills t; on error it returns the failing step name.
func (p *Processor) analyse(ctx context.Context, t *model.Track, id, key string) (string, error) {
	dir := p.TmpDir
	if dir == "" {
		dir = "/tmp"
	}
	path := filepath.Join(dir, id+".mp3")
	defer os.Remove(path)
	if err := p.Blob.Download(ctx, key, path); err != nil {
		return "download", err
	}

	pr, err := p.Analyzer.Probe(ctx, path)
	if err != nil {
		return "probe", err
	}
	t.Title = pr.Title
	if t.Title == "" {
		t.Title = TitleFromFilename(t.OriginalFilename)
	}
	t.Artist = pr.Artist
	if t.Artist == "" {
		t.Artist = "Unknown Artist"
	}
	t.Album, t.AlbumArtist, t.Composer, t.Genre = pr.Album, pr.AlbumArtist, pr.Composer, pr.Genre
	t.Lyrics, t.Comment, t.Copyright = pr.Lyrics, pr.Comment, pr.Copyright
	t.Year, t.TrackNumber, t.DurationSeconds = pr.Year, pr.TrackNumber, pr.DurationSeconds

	lufs, err := p.Analyzer.IntegratedLUFS(ctx, path)
	if err != nil {
		return "loudness", err
	}
	t.Energy = audio.EnergyFromLUFS(lufs)

	if t.BPMSource != model.BPMManual {
		if bpm, ok := audio.ParseTBPM(pr.TBPM); ok {
			t.BPM, t.BPMSource = bpm, model.BPMTag
		} else {
			start := math.Max(0, pr.DurationSeconds/2-decodeSecs/2)
			samples, err := p.Analyzer.DecodeMono(ctx, path, start, decodeSecs, decodeRate)
			if err != nil {
				return "decode", err
			}
			bpm, err := audio.DetectBPM(samples, decodeRate)
			if err != nil {
				return "tempo", err
			}
			t.BPM, t.BPMSource = bpm, model.BPMMeasured
		}
	}

	artistID, albumID, err := catalog.Ensure(ctx, p.Store, p.now(), t.Artist, t.Album, t.Year)
	if err != nil {
		return "catalog", err
	}
	t.ArtistID, t.AlbumID = artistID, albumID
	return "", nil
}

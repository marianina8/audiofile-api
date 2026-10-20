package processor

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marianina8/audiofile-api/internal/audio"
	"github.com/marianina8/audiofile-api/internal/blob"
	"github.com/marianina8/audiofile-api/internal/model"
	"github.com/marianina8/audiofile-api/internal/store"
)

type fakeAnalyzer struct {
	probe    audio.Probe
	probeErr error
	lufs     float64
	samples  []float32
	onLUFS   func()
	dec      struct {
		start, dur float64
		rate       int
	}
	paths []string
}

func (f *fakeAnalyzer) Probe(_ context.Context, path string) (audio.Probe, error) {
	f.paths = append(f.paths, path)
	return f.probe, f.probeErr
}

func (f *fakeAnalyzer) IntegratedLUFS(context.Context, string) (float64, error) {
	if f.onLUFS != nil {
		f.onLUFS()
	}
	return f.lufs, nil
}

func (f *fakeAnalyzer) DecodeMono(_ context.Context, _ string, start, dur float64, rate int) ([]float32, error) {
	f.dec.start, f.dec.dur, f.dec.rate = start, dur, rate
	return f.samples, nil
}

// clicks makes a 30 s click track at bpm, 22050 Hz.
func clicks(bpm float64) []float32 {
	const rate = 22050
	s := make([]float32, 30*rate)
	rng := rand.New(rand.NewSource(1))
	period := 60 / bpm * rate
	for beat := 0.0; int(beat*period) < len(s); beat++ {
		at := int(beat * period)
		for i := 0; i < 400 && at+i < len(s); i++ {
			s[at+i] = float32((rng.Float64()*2 - 1) * math.Exp(-float64(i)/80))
		}
	}
	return s
}

type env struct {
	st  *store.Memory
	bl  *blob.Fake
	an  *fakeAnalyzer
	p   *Processor
	dir string
}

func setup(t *testing.T, filename string) *env {
	t.Helper()
	e := &env{st: store.NewMemory(), bl: blob.NewFake(), an: &fakeAnalyzer{lufs: -12, samples: clicks(120)}, dir: t.TempDir()}
	e.p = &Processor{Store: e.st, Blob: e.bl, Analyzer: e.an, TmpDir: e.dir,
		Now: func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }}
	_ = e.bl.Put(context.Background(), "tracks/t1.mp3", "audio/mpeg", []byte("x"))
	err := e.st.PutTrack(context.Background(), model.Track{ID: "t1", Status: model.StatusProcessing,
		OriginalFilename: filename, ContentSHA256: "sha", CreatedAt: "2026-01-01T00:00:00Z", S3Key: "tracks/t1.mp3"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) track(t *testing.T) model.Track {
	t.Helper()
	tr, err := e.st.GetTrack(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestProcess_TaggedTrack(t *testing.T) {
	e := setup(t, "a.mp3")
	e.an.probe = audio.Probe{Title: "Beat", Artist: "Marian", Album: "Book", Year: 2025, TBPM: "124", DurationSeconds: 100}
	if err := e.p.Process(context.Background(), "tracks/t1.mp3"); err != nil {
		t.Fatal(err)
	}
	tr := e.track(t)
	if tr.Status != model.StatusReady || tr.BPM != 124 || tr.BPMSource != model.BPMTag || tr.Energy != 0.67 {
		t.Fatalf("got %+v", tr)
	}
	if tr.ContentSHA256 != "sha" || tr.CreatedAt != "2026-01-01T00:00:00Z" || tr.UpdatedAt != "2026-10-20T12:00:00Z" {
		t.Fatalf("preserved fields wrong: %+v", tr)
	}
	if _, err := e.st.GetArtist(context.Background(), tr.ArtistID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.GetAlbum(context.Background(), tr.AlbumID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "t1.mp3")); !os.IsNotExist(err) {
		t.Fatalf("temp file not removed: %v", err)
	}
}

func TestProcess_NoTags(t *testing.T) {
	e := setup(t, "alex-morgan-no-copyright-music-578487.mp3")
	e.an.probe = audio.Probe{DurationSeconds: 100}
	if err := e.p.Process(context.Background(), "tracks/t1.mp3"); err != nil {
		t.Fatal(err)
	}
	tr := e.track(t)
	if tr.Status != model.StatusReady || tr.Title != "Alex Morgan" || tr.Artist != "Unknown Artist" ||
		tr.ArtistID != "unknown-artist" || tr.AlbumID != "" || tr.BPMSource != model.BPMMeasured {
		t.Fatalf("got %+v", tr)
	}
	if math.Abs(tr.BPM-120) > 3 {
		t.Fatalf("bpm = %v", tr.BPM)
	}
}

func TestProcess_TempoFailureMarksFailed(t *testing.T) {
	e := setup(t, "a.mp3")
	e.an.samples = make([]float32, 30*22050)
	if err := e.p.Process(context.Background(), "tracks/t1.mp3"); err != nil {
		t.Fatal(err)
	}
	tr := e.track(t)
	if tr.Status != model.StatusFailed || !strings.Contains(tr.Error, "no tempo") || !strings.HasPrefix(tr.Error, "tempo: ") {
		t.Fatalf("got %+v", tr)
	}
}

func TestProcess_ProbeAndDownloadFailures(t *testing.T) {
	e := setup(t, "a.mp3")
	e.an.probeErr = errors.New("bad file")
	if err := e.p.Process(context.Background(), "tracks/t1.mp3"); err != nil {
		t.Fatal(err)
	}
	if tr := e.track(t); tr.Status != model.StatusFailed || tr.Error != "probe: bad file" {
		t.Fatalf("got %+v", tr)
	}
	e = setup(t, "a.mp3")
	_ = e.bl.Delete(context.Background(), "tracks/t1.mp3")
	if err := e.p.Process(context.Background(), "tracks/t1.mp3"); err != nil {
		t.Fatal(err)
	}
	if tr := e.track(t); tr.Status != model.StatusFailed || !strings.HasPrefix(tr.Error, "download: ") {
		t.Fatalf("got %+v", tr)
	}
}

func TestProcess_PreservesManualBPMAndTags(t *testing.T) {
	e := setup(t, "a.mp3")
	tr := e.track(t)
	tr.BPM, tr.BPMSource, tr.Tags = 99, model.BPMManual, []string{"workout"}
	_ = e.st.PutTrack(context.Background(), tr)
	e.an.probe = audio.Probe{TBPM: "124", DurationSeconds: 100}
	if err := e.p.Process(context.Background(), "tracks/t1.mp3"); err != nil {
		t.Fatal(err)
	}
	got := e.track(t)
	if got.BPM != 99 || got.BPMSource != model.BPMManual || len(got.Tags) != 1 || got.Tags[0] != "workout" || got.Status != model.StatusReady {
		t.Fatalf("got %+v", got)
	}
}

func TestProcess_LostUpdateGuard(t *testing.T) {
	e := setup(t, "a.mp3")
	e.an.probe = audio.Probe{DurationSeconds: 100}
	e.an.onLUFS = func() {
		cur := e.track(t)
		cur.Tags = []string{"chill"}
		cur.BPM, cur.BPMSource = 77, model.BPMManual
		if err := e.st.PutTrack(context.Background(), cur); err != nil {
			t.Error(err)
		}
	}
	if err := e.p.Process(context.Background(), "tracks/t1.mp3"); err != nil {
		t.Fatal(err)
	}
	got := e.track(t)
	if got.BPM != 77 || got.BPMSource != model.BPMManual || len(got.Tags) != 1 || got.Tags[0] != "chill" {
		t.Fatalf("mid-processing edits lost: %+v", got)
	}
}

func TestProcess_IgnoresOtherKeys(t *testing.T) {
	e := setup(t, "a.mp3")
	before := e.track(t)
	for _, k := range []string{"players/x.html", "tracks/a/b.mp3", "tracks/x.wav"} {
		if err := e.p.Process(context.Background(), k); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.track(t); got.Status != before.Status || got.UpdatedAt != before.UpdatedAt {
		t.Fatalf("store changed: %+v", got)
	}
	all, _ := e.st.AllTracks(context.Background())
	if len(all) != 1 {
		t.Fatalf("tracks = %d", len(all))
	}
}

func TestProcess_DeletedMidProcessingStaysDeleted(t *testing.T) {
	e := setup(t, "a.mp3")
	e.an.probe = audio.Probe{DurationSeconds: 100}
	e.an.onLUFS = func() { _ = e.st.DeleteTrack(context.Background(), "t1") }
	if err := e.p.Process(context.Background(), "tracks/t1.mp3"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.GetTrack(context.Background(), "t1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("track resurrected: %v", err)
	}
}

type flakyStore struct {
	*store.Memory
	gets, puts int
}

func (f *flakyStore) GetTrack(ctx context.Context, id string) (model.Track, error) {
	f.gets++
	if f.gets == 2 {
		return model.Track{}, errors.New("boom")
	}
	return f.Memory.GetTrack(ctx, id)
}

func (f *flakyStore) PutTrack(ctx context.Context, t model.Track) error {
	f.puts++
	return f.Memory.PutTrack(ctx, t)
}

func TestProcess_ReReadErrorReturnsError(t *testing.T) {
	e := setup(t, "a.mp3")
	fs := &flakyStore{Memory: e.st}
	e.p.Store = fs
	e.an.probe = audio.Probe{TBPM: "124", DurationSeconds: 100}
	if err := e.p.Process(context.Background(), "tracks/t1.mp3"); err == nil {
		t.Fatal("expected error")
	}
	if fs.puts != 0 {
		t.Fatalf("puts = %d", fs.puts)
	}
	if e.track(t).Status != model.StatusProcessing {
		t.Fatal("track was written")
	}
}

func TestProcess_MissingTrackCreated(t *testing.T) {
	e := setup(t, "a.mp3")
	_ = e.st.DeleteTrack(context.Background(), "t1")
	e.an.probe = audio.Probe{Title: "T", TBPM: "100", DurationSeconds: 10}
	if err := e.p.Process(context.Background(), "tracks/t1.mp3"); err != nil {
		t.Fatal(err)
	}
	tr := e.track(t)
	if tr.Status != model.StatusReady || tr.S3Key != "tracks/t1.mp3" || tr.CreatedAt != "2026-10-20T12:00:00Z" {
		t.Fatalf("got %+v", tr)
	}
}

func TestProcess_AlbumClearedOnReprocess(t *testing.T) {
	e := setup(t, "a.mp3")
	tr := e.track(t)
	tr.Album, tr.AlbumID = "Old", "old-album"
	_ = e.st.PutTrack(context.Background(), tr)
	e.an.probe = audio.Probe{Artist: "X", TBPM: "100", DurationSeconds: 10}
	if err := e.p.Process(context.Background(), "tracks/t1.mp3"); err != nil {
		t.Fatal(err)
	}
	if got := e.track(t); got.AlbumID != "" || got.Album != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestProcess_CarryOverOntoFailedTrack(t *testing.T) {
	e := setup(t, "a.mp3")
	e.an.samples = make([]float32, 30*22050)
	e.an.probe = audio.Probe{DurationSeconds: 100}
	e.an.onLUFS = func() {
		cur := e.track(t)
		cur.Tags = []string{"keep"}
		_ = e.st.PutTrack(context.Background(), cur)
	}
	if err := e.p.Process(context.Background(), "tracks/t1.mp3"); err != nil {
		t.Fatal(err)
	}
	got := e.track(t)
	if got.Status != model.StatusFailed || len(got.Tags) != 1 || got.Tags[0] != "keep" {
		t.Fatalf("got %+v", got)
	}
}

func TestProcess_DecodeWindow(t *testing.T) {
	for _, c := range []struct{ dur, start float64 }{{200, 70}, {40, 0}} {
		e := setup(t, "a.mp3")
		e.an.probe = audio.Probe{DurationSeconds: c.dur}
		if err := e.p.Process(context.Background(), "tracks/t1.mp3"); err != nil {
			t.Fatal(err)
		}
		if e.an.dec.start != c.start || e.an.dec.dur != 60 || e.an.dec.rate != 22050 {
			t.Fatalf("dur %v: got %+v", c.dur, e.an.dec)
		}
	}
}

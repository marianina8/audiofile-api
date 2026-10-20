package audio

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"os"
	"testing"

	"github.com/marianina8/audiofile-api/internal/testutil"
)

const clickRate = 22050

// clickTrack builds secs seconds of 5 ms 2 kHz bursts at bpm, plus a little noise.
func clickTrack(bpm, secs float64) []float32 {
	n := int(secs * clickRate)
	s := make([]float32, n)
	rng := rand.New(rand.NewSource(1))
	for i := range s {
		s[i] = float32(0.005 * rng.NormFloat64())
	}
	burst := clickRate / 200
	period := 60 / bpm * clickRate
	for start := 0.0; int(start) < n; start += period {
		for j := 0; j < burst && int(start)+j < n; j++ {
			s[int(start)+j] += float32(0.8 * math.Sin(2*math.Pi*2000*float64(j)/clickRate))
		}
	}
	return s
}

func TestDetectBPM_Clicks(t *testing.T) {
	for _, want := range []float64{90, 120, 128, 150} {
		got, err := DetectBPM(clickTrack(want, 30), clickRate)
		if err != nil {
			t.Fatalf("%v BPM: %v", want, err)
		}
		if math.Abs(got-want) > 1.5 {
			t.Errorf("%v BPM: got %v", want, got)
		}
	}
}

func TestDetectBPM_Fast(t *testing.T) {
	// The prior centred on 120 may halve fast tempos: 174 or 87 are both accepted.
	got, err := DetectBPM(clickTrack(174, 30), clickRate)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-174) > 1.5 && math.Abs(got-87) > 1.5 {
		t.Errorf("got %v, want 174 or 87", got)
	}
}

func TestDetectBPM_SilenceErrors(t *testing.T) {
	silence := make([]float32, 30*clickRate)
	if _, err := DetectBPM(silence, clickRate); !errors.Is(err, ErrNoTempo) {
		t.Errorf("silence: err = %v", err)
	}
	sine := make([]float32, 30*clickRate)
	for i := range sine {
		sine[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/clickRate))
	}
	if _, err := DetectBPM(sine, clickRate); !errors.Is(err, ErrNoTempo) {
		t.Errorf("sine: err = %v", err)
	}
}

func TestDetectBPM_NoiseErrors(t *testing.T) {
	for _, sigma := range []float64{0.1, 0.001} {
		rng := rand.New(rand.NewSource(7))
		s := make([]float32, 30*clickRate)
		for i := range s {
			s[i] = float32(sigma * rng.NormFloat64())
		}
		if bpm, err := DetectBPM(s, clickRate); !errors.Is(err, ErrNoTempo) {
			t.Errorf("noise sigma=%v: bpm=%v err=%v", sigma, bpm, err)
		}
	}
}

func TestDetectBPM_TooShort(t *testing.T) {
	if _, err := DetectBPM(clickTrack(120, 5), clickRate); !errors.Is(err, ErrNoTempo) {
		t.Errorf("err = %v", err)
	}
	// Shorter than one FFT frame, and a bad rate, must error rather than panic.
	if _, err := DetectBPM(make([]float32, 100), 1); !errors.Is(err, ErrNoTempo) {
		t.Errorf("sub-frame: err = %v", err)
	}
	if _, err := DetectBPM(clickTrack(120, 30), 0); !errors.Is(err, ErrNoTempo) {
		t.Errorf("rate 0: err = %v", err)
	}
}

func TestDetectBPM_RealFile(t *testing.T) {
	const realFileRate = 22050 // decode rate the pipeline uses (spec §6)
	path := os.Getenv("BEATDOCTOR_MP3")
	if path == "" {
		t.Skip("BEATDOCTOR_MP3 not set")
	}
	testutil.RequireFFmpeg(t)
	tools := ToolsFromEnv()
	info, err := tools.Probe(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	start := math.Max(0, info.DurationSeconds/2-30)
	s, err := tools.DecodeMono(context.Background(), path, start, 60, realFileRate)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DetectBPM(s, realFileRate)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("beatdoctor measured BPM = %v", got)
	if math.Abs(got-124) > 2 && math.Abs(got-62) > 2 {
		t.Errorf("got %v, want 124 or 62", got)
	}
}

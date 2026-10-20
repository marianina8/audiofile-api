package audio

import (
	"context"
	"math"
	"testing"

	"github.com/marianina8/audiofile-api/internal/testutil"
)

func TestIntegratedLUFS(t *testing.T) {
	testutil.RequireFFmpeg(t)
	tools := Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	loud := writeTestMP3(t, "-af", "volume=0.5")
	quiet := writeTestMP3(t, "-af", "volume=0.05")
	l1, err := tools.IntegratedLUFS(context.Background(), loud)
	if err != nil {
		t.Fatal(err)
	}
	l2, err := tools.IntegratedLUFS(context.Background(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("loud=%.2f quiet=%.2f", l1, l2)
	if math.Abs(l1-pinnedHalfAmpLUFS) > 1.5 {
		t.Errorf("loud LUFS = %v, want %v ± 1.5", l1, pinnedHalfAmpLUFS)
	}
	if math.Abs((l1-l2)-20) > 1.5 {
		t.Errorf("difference = %v, want ~20", l1-l2)
	}
}

func TestIntegratedLUFS_Silence(t *testing.T) {
	testutil.RequireFFmpeg(t)
	p := writeTestMP3From(t, "anullsrc=r=44100:cl=mono:d=3")
	l, err := Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}.IntegratedLUFS(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if l != -70 || EnergyFromLUFS(l) != 0 {
		t.Errorf("silence LUFS = %v", l)
	}
}

func TestParseLUFS(t *testing.T) {
	if v, err := parseLUFS("  I:         -inf LUFS\n"); err != nil || v != -70 {
		t.Errorf("inf: %v %v", v, err)
	}
	if _, err := parseLUFS("nothing here"); err == nil {
		t.Error("expected error")
	}
	if v, _ := parseLUFS("I: -20.0 LUFS\nSummary:\n  I:         -9.3 LUFS\n  LRA: 0 LU"); v != -9.3 {
		t.Errorf("last = %v", v)
	}
}

func TestEnergyFromLUFS(t *testing.T) {
	cases := map[float64]float64{-24: 0, -6: 1, -15: 0.5, -40: 0, 0: 1, -9.3: 0.82}
	for in, want := range cases {
		if got := EnergyFromLUFS(in); got != want {
			t.Errorf("EnergyFromLUFS(%v) = %v want %v", in, got, want)
		}
	}
}

// pinnedHalfAmpLUFS was measured once with ffmpeg 9.0.2 (ebur128) for a 10 s
// 1 kHz sine: lavfi sine defaults to amplitude 1/8, and volume=0.5 halves it.
const pinnedHalfAmpLUFS = -27.5

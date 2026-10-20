package audio

import (
	"context"
	"errors"
	"testing"

	"github.com/marianina8/audiofile-api/internal/testutil"
)

func TestDecodeMono(t *testing.T) {
	testutil.RequireFFmpeg(t)
	p := writeTestMP3From(t, "sine=frequency=440:duration=4")
	s, err := Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}.DecodeMono(context.Background(), p, 1, 2, 22050)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(s); n < 44100-1200 || n > 44100+1200 {
		t.Errorf("samples = %d", n)
	}
	var peak float32
	for _, v := range s {
		if v > peak {
			peak = v
		}
	}
	if peak < 0.1 {
		t.Errorf("peak = %v, expected audible signal", peak)
	}
}

func TestDecodeMono_Canceled(t *testing.T) {
	testutil.RequireFFmpeg(t)
	p := writeTestMP3(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}.DecodeMono(ctx, p, 0, 2, 22050)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("expected error")
	}
}

func TestToolsFromEnv(t *testing.T) {
	t.Setenv("FFMPEG_PATH", "/opt/bin/ffmpeg")
	t.Setenv("FFPROBE_PATH", "")
	got := ToolsFromEnv()
	if got.FFmpeg != "/opt/bin/ffmpeg" || got.FFprobe != "ffprobe" {
		t.Errorf("%+v", got)
	}
}

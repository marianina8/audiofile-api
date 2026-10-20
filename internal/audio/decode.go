package audio

import (
	"context"
	"encoding/binary"
	"math"
	"strconv"
)

// DecodeMono decodes dur seconds starting at start into mono float32 PCM at rate Hz.
func (t Tools) DecodeMono(ctx context.Context, path string, startSec, durSec float64, rate int) ([]float32, error) {
	out, _, err := run(ctx, "ffmpeg", t.FFmpeg, "-hide_banner", "-nostdin", "-loglevel", "error",
		"-ss", strconv.FormatFloat(startSec, 'f', -1, 64),
		"-t", strconv.FormatFloat(durSec, 'f', -1, 64),
		"-i", path, "-ac", "1", "-ar", strconv.Itoa(rate), "-f", "f32le", "pipe:1")
	if err != nil {
		return nil, err
	}
	n := len(out) / 4
	s := make([]float32, n)
	for i := range s {
		s[i] = math.Float32frombits(binary.LittleEndian.Uint32(out[i*4:]))
	}
	return s, nil
}

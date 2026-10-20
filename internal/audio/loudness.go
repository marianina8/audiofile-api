package audio

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

var lufsRe = regexp.MustCompile(`(?m)^\s*I:\s+(-?inf|-?[0-9]+(?:\.[0-9]+)?)\s+LUFS`)

// IntegratedLUFS measures integrated loudness with ffmpeg's ebur128 filter.
// Silence (-inf) is reported as -70.
func (t Tools) IntegratedLUFS(ctx context.Context, path string) (float64, error) {
	_, stderr, err := run(ctx, "ffmpeg", t.FFmpeg, "-hide_banner", "-nostdin", "-nostats",
		"-loglevel", "info", "-i", path, "-af", "ebur128=framelog=quiet", "-f", "null", "-")
	if err != nil {
		return 0, err
	}
	return parseLUFS(string(stderr))
}

// parseLUFS returns the last "I: x LUFS" value in the output.
func parseLUFS(s string) (float64, error) {
	m := lufsRe.FindAllStringSubmatch(s, -1)
	if len(m) == 0 {
		return 0, fmt.Errorf("ffmpeg: no integrated loudness in output")
	}
	v, err := strconv.ParseFloat(m[len(m)-1][1], 64)
	if err != nil || math.IsInf(v, 0) || v < -70 {
		return -70, nil
	}
	return v, nil
}

// EnergyFromLUFS maps loudness to 0..1: clamp((l+24)/18), 2 decimals.
func EnergyFromLUFS(l float64) float64 {
	e := math.Min(1, math.Max(0, (l+24)/18))
	return math.Round(e*100) / 100
}

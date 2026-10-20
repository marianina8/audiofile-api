// Package audio wraps ffprobe and ffmpeg for tag reading, loudness and PCM decoding.
package audio

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// Tools locates the ffmpeg and ffprobe binaries.
type Tools struct {
	FFmpeg  string
	FFprobe string
}

// ToolsFromEnv reads FFMPEG_PATH and FFPROBE_PATH, defaulting to PATH lookups.
func ToolsFromEnv() Tools {
	t := Tools{FFmpeg: os.Getenv("FFMPEG_PATH"), FFprobe: os.Getenv("FFPROBE_PATH")}
	if t.FFmpeg == "" {
		t.FFmpeg = "ffmpeg"
	}
	if t.FFprobe == "" {
		t.FFprobe = "ffprobe"
	}
	return t
}

// run executes bin with args (never via a shell) and returns stdout and stderr.
func run(ctx context.Context, name, bin string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, nil, fmt.Errorf("%s: %w", name, cerr)
		}
		return nil, nil, fmt.Errorf("%s: %w: %s", name, err, tail(errb.Bytes(), 300))
	}
	return out.Bytes(), errb.Bytes(), nil
}

func tail(b []byte, n int) string {
	b = bytes.TrimSpace(b)
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}

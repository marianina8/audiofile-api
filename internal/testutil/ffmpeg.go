// Package testutil holds shared test helpers.
package testutil

import (
	"os/exec"
	"testing"
)

// RequireFFmpeg skips the test unless ffmpeg and ffprobe are on PATH.
func RequireFFmpeg(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("ffmpeg/ffprobe not on PATH; install ffmpeg to run audio tests")
		}
	}
}

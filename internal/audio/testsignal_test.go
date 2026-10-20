package audio

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// writeTestMP3 renders a 10 s 1 kHz sine to an MP3 in t.TempDir(). Extra args
// (e.g. -af volume=0.1, -metadata title=T) are inserted before the output file.
func writeTestMP3(t *testing.T, args ...string) string {
	t.Helper()
	return writeTestMP3From(t, "sine=frequency=1000:duration=10", args...)
}

func writeTestMP3From(t *testing.T, lavfi string, args ...string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "test.mp3")
	full := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-f", "lavfi", "-i", lavfi}
	full = append(full, args...)
	full = append(full, "-c:a", "libmp3lame", out)
	if b, err := exec.Command("ffmpeg", full...).CombinedOutput(); err != nil {
		t.Skipf("cannot generate MP3 fixture (libmp3lame missing?): %v: %s", err, b)
	}
	return out
}

package slug

import "testing"

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Beat Doctor":     "beat-doctor",
		"Beyoncé & Jay-Z": "beyonce-jay-z",
		"  --  ":          "unknown",
	}
	for in, want := range cases {
		if got := Make(in); got != want {
			t.Errorf("Make(%q) = %q, want %q", in, got, want)
		}
	}
	if got := AlbumID("beat-doctor", "Best Bytes Volume 4"); got != "beat-doctor--best-bytes-volume-4" {
		t.Errorf("AlbumID = %q", got)
	}
}

// Package slug builds URL-safe identifiers for artists and albums.
package slug

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Make returns a lowercase ASCII slug of s, or "unknown" if nothing remains.
func Make(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFKD.String(s) {
		switch {
		case r >= 'A' && r <= 'Z':
			r += 'a' - 'A'
			fallthrough
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		case r < 128:
			dash = true
		}
		// non-ASCII (e.g. combining marks) is dropped without breaking a word
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// AlbumID returns the album slug: artistID + "--" + Make(albumTitle).
func AlbumID(artistID, albumTitle string) string {
	return artistID + "--" + Make(albumTitle)
}

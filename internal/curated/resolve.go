package curated

import (
	"fmt"
	"strings"
)

// ResolvePlaylist finds a playlist by exact id, else by case-insensitive exact
// name. An id match always beats a name match. It never guesses: a miss lists
// the playlist names, and an ambiguous name lists the matching ids.
func ResolvePlaylist(ps []Playlist, ref string) (Playlist, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Playlist{}, fmt.Errorf("playlist is required: give a playlist name or id")
	}
	for _, p := range ps {
		if p.ID == ref {
			return p, nil
		}
	}
	var hits []Playlist
	for _, p := range ps {
		if strings.EqualFold(p.Name, ref) {
			hits = append(hits, p)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		names := make([]string, len(ps))
		for i, p := range ps {
			names[i] = p.Name
		}
		list := "none"
		if len(names) > 0 {
			list = strings.Join(names, ", ")
		}
		return Playlist{}, fmt.Errorf("no playlist named '%s'; playlists: %s", ref, list)
	default:
		ids := make([]string, len(hits))
		for i, p := range hits {
			ids[i] = p.ID
		}
		return Playlist{}, fmt.Errorf("%d playlists are named '%s'; use one of these ids: %s", len(hits), ref, strings.Join(ids, ", "))
	}
}

// uniqueName returns name, or name with " 2", " 3" and so on, so it doesn't
// match any existing playlist name (case-insensitively).
func uniqueName(ps []Playlist, name string) string {
	taken := func(n string) bool {
		for _, p := range ps {
			if strings.EqualFold(p.Name, n) {
				return true
			}
		}
		return false
	}
	if !taken(name) {
		return name
	}
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s %d", name, i); !taken(n) {
			return n
		}
	}
}

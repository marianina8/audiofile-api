package api

import (
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/marianina8/audiofile-api/internal/model"
)

// byTitle sorts tracks by title (case-insensitive), then id.
func byTitle(ts []model.Track) {
	sort.SliceStable(ts, func(i, j int) bool {
		a, b := strings.ToLower(ts[i].Title), strings.ToLower(ts[j].Title)
		if a != b {
			return a < b
		}
		return ts[i].ID < ts[j].ID
	})
}

func readyOnly(ts []model.Track) []model.Track {
	out := make([]model.Track, 0, len(ts))
	for _, t := range ts {
		if t.Status == model.StatusReady {
			out = append(out, t)
		}
	}
	return out
}

func (s *server) trackItems(ts []model.Track) map[string]any {
	items := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		items = append(items, s.trackView(t))
	}
	return map[string]any{"items": items}
}

// floatParam parses an optional finite float query parameter.
func floatParam(q url.Values, name string) (*float64, bool) {
	v := q.Get(name)
	if v == "" {
		return nil, true
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, false
	}
	return &f, true
}

func (s *server) searchTracks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := defaultPageSize
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxPageSize {
			badRequest(w, "limit must be between 1 and 100")
			return
		}
		limit = n
	}
	year := 0
	hasYear := false
	if v := q.Get("year"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			badRequest(w, "year must be an integer")
			return
		}
		year, hasYear = n, true
	}
	var bounds [4]*float64
	for i, name := range []string{"bpm_min", "bpm_max", "energy_min", "energy_max"} {
		f, ok := floatParam(q, name)
		if !ok {
			badRequest(w, name+" must be a number")
			return
		}
		bounds[i] = f
	}
	bpmMin, bpmMax, enMin, enMax := bounds[0], bounds[1], bounds[2], bounds[3]
	text := strings.ToLower(q.Get("q"))
	artist := q.Get("artist")
	genre := q.Get("genre")

	all, err := s.Store.AllTracks(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	var out []model.Track
	for _, t := range readyOnly(all) {
		switch {
		case text != "" && !strings.Contains(strings.ToLower(t.Title), text) &&
			!strings.Contains(strings.ToLower(t.Artist), text) &&
			!strings.Contains(strings.ToLower(t.Album), text):
		case artist != "" && t.ArtistID != artist && !strings.EqualFold(t.Artist, artist):
		case genre != "" && !strings.EqualFold(t.Genre, genre):
		case hasYear && t.Year != year:
		case bpmMin != nil && t.BPM < *bpmMin:
		case bpmMax != nil && t.BPM > *bpmMax:
		case enMin != nil && t.Energy < *enMin:
		case enMax != nil && t.Energy > *enMax:
		default:
			out = append(out, t)
		}
	}
	byTitle(out)
	if len(out) > limit {
		out = out[:limit]
	}
	writeJSON(w, http.StatusOK, s.trackItems(out))
}

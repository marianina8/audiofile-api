package api

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

var tagPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// normalizeTag trims, lowercases and turns spaces into hyphens; the result must
// be 1-32 characters of a-z, 0-9 or "-".
func normalizeTag(s string) (string, error) {
	t := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "-")
	if !tagPattern.MatchString(t) {
		return "", fmt.Errorf("invalid tag %q: use 1-32 letters, digits or hyphens", s)
	}
	return t, nil
}

func (s *server) addTrackTags(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tags []string `json:"tags"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if len(req.Tags) == 0 {
		badRequest(w, "tags must be a non-empty array")
		return
	}
	norm := make([]string, 0, len(req.Tags))
	for _, raw := range req.Tags {
		tag, err := normalizeTag(raw)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		norm = append(norm, tag)
	}

	ctx := r.Context()
	t, err := s.Store.GetTrack(ctx, r.PathValue("id"))
	if err != nil {
		storeError(w, err, "track")
		return
	}
	merged := append(slices.Clone(t.Tags), norm...)
	slices.Sort(merged)
	t.Tags = slices.Compact(merged)
	t.UpdatedAt = s.Now().UTC().Format(time.RFC3339)
	if err := s.Store.PutTrack(ctx, t); err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, s.trackView(t))
}

func (s *server) removeTrackTag(w http.ResponseWriter, r *http.Request) {
	tag, err := normalizeTag(r.PathValue("tag"))
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	ctx := r.Context()
	t, err := s.Store.GetTrack(ctx, r.PathValue("id"))
	if err != nil {
		storeError(w, err, "track")
		return
	}
	if !slices.Contains(t.Tags, tag) {
		writeError(w, http.StatusNotFound, "not_found", "tag not found on track")
		return
	}
	t.Tags = slices.DeleteFunc(slices.Clone(t.Tags), func(x string) bool { return x == tag })
	t.UpdatedAt = s.Now().UTC().Format(time.RFC3339)
	if err := s.Store.PutTrack(ctx, t); err != nil {
		internalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

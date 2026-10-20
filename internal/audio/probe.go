package audio

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Probe holds the tags and duration read from an audio file.
type Probe struct {
	Title, Artist, Album, AlbumArtist, Composer, Genre, Lyrics, Comment, Copyright string
	TBPM                                                                           string
	Year, TrackNumber                                                              int
	DurationSeconds                                                                float64
}

// Probe reads tags and duration with ffprobe.
func (t Tools) Probe(ctx context.Context, path string) (Probe, error) {
	out, _, err := run(ctx, "ffprobe", t.FFprobe, "-hide_banner", "-v", "quiet",
		"-print_format", "json", "-show_format", "-show_streams", path)
	if err != nil {
		return Probe{}, err
	}
	return parseProbeJSON(out)
}

type probeJSON struct {
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		Tags map[string]string `json:"tags"`
	} `json:"streams"`
}

func parseProbeJSON(b []byte) (Probe, error) {
	var j probeJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return Probe{}, fmt.Errorf("ffprobe: parse json: %w", err)
	}
	tags := map[string]string{}
	// Stream tags first, then format tags overwrite (format wins).
	for _, s := range j.Streams {
		for k, v := range s.Tags {
			if _, ok := tags[strings.ToLower(k)]; !ok {
				tags[strings.ToLower(k)] = v
			}
		}
	}
	for k, v := range j.Format.Tags {
		tags[strings.ToLower(k)] = v
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(tags[k]); v != "" {
				return v
			}
		}
		return ""
	}
	p := Probe{
		Title:       get("title"),
		Artist:      get("artist"),
		Album:       get("album"),
		AlbumArtist: get("album_artist"),
		Composer:    get("composer"),
		Genre:       get("genre"),
		Comment:     get("comment"),
		Copyright:   get("copyright", "tcop"),
		TBPM:        get("tbpm", "bpm"),
		Year:        firstYear(get("date", "year", "tyer", "tdrc")),
		TrackNumber: leadingInt(get("track")),
	}
	var lk []string
	for k := range tags {
		if strings.HasPrefix(k, "lyrics") {
			lk = append(lk, k)
		}
	}
	sort.Strings(lk)
	for _, k := range lk {
		if v := strings.TrimSpace(tags[k]); v != "" {
			p.Lyrics = v
			break
		}
	}
	if d, err := strconv.ParseFloat(j.Format.Duration, 64); err == nil {
		p.DurationSeconds = d
	}
	return p, nil
}

// firstYear returns the first run of 4 consecutive digits, or 0.
func firstYear(s string) int {
	for i := 0; i+4 <= len(s); i++ {
		if allDigits(s[i : i+4]) {
			n, _ := strconv.Atoi(s[i : i+4])
			return n
		}
	}
	return 0
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func leadingInt(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, _ := strconv.Atoi(s[:i])
	return n
}

// ParseTBPM validates a raw TBPM string: 40 <= bpm <= 250, rounded to 1 decimal.
func ParseTBPM(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || v < 40 || v > 250 {
		return 0, false
	}
	return math.Round(v*10) / 10, true
}

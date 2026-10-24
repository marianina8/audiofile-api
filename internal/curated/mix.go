package curated

import (
	"fmt"
	"math"
	"sort"
	"strconv"
)

// Mood is a named rule over bpm and energy. It is defined once, here.
type Mood string

const (
	Upbeat Mood = "upbeat"
	Calm   Mood = "calm"
	Any    Mood = "any"
)

const (
	UpbeatMinBPM    = 120.0
	UpbeatMinEnergy = 0.6
	CalmMaxEnergy   = 0.5
)

// Match reports whether a ready track fits the mood. Tracks that aren't ready
// never match.
func (m Mood) Match(t Track) bool {
	if t.Status != "ready" {
		return false
	}
	switch m {
	case Upbeat:
		return t.BPM >= UpbeatMinBPM && t.Energy >= UpbeatMinEnergy
	case Calm:
		return t.Energy < CalmMaxEnergy
	default:
		return true
	}
}

// Filter narrows the library. The mood always applies; the ranges only narrow
// it further.
type Filter struct {
	Mood                                                         Mood
	BPMMin, BPMMax, EnergyMin, EnergyMax, MinSeconds, MaxSeconds *float64
}

func (f Filter) match(t Track) bool {
	if !f.Mood.Match(t) {
		return false
	}
	in := func(v float64, lo, hi *float64) bool {
		return (lo == nil || v >= *lo) && (hi == nil || v <= *hi)
	}
	return in(t.BPM, f.BPMMin, f.BPMMax) &&
		in(t.Energy, f.EnergyMin, f.EnergyMax) &&
		in(t.DurationSeconds, f.MinSeconds, f.MaxSeconds)
}

// FindTracks returns up to limit ready tracks that match, sorted by bpm
// ascending, then id.
func FindTracks(lib []Track, f Filter, limit int) []Track {
	out := []Track{}
	for _, t := range lib {
		if f.match(t) {
			out = append(out, t)
		}
	}
	sortByBPM(out, false)
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

type Section string

const (
	Warmup   Section = "warm-up"
	Peak     Section = "peak"
	Cooldown Section = "cooldown"
	// SteadySection is "steady"; the name Steady is taken by the mix builder.
	SteadySection Section = "steady"
)

type MixTrack struct {
	Track
	Section Section
}

type Mix struct {
	Tracks  []MixTrack
	Seconds float64
	Note    string
}

// Minutes rounds seconds to minutes with one decimal.
func Minutes(sec float64) float64 { return math.Round(sec/60*10) / 10 }

func fmtMinutes(sec float64) string {
	return strconv.FormatFloat(Minutes(sec), 'f', -1, 64)
}

// sortByBPM sorts by bpm (descending when desc), ties by id ascending.
func sortByBPM(ts []Track, desc bool) {
	sort.SliceStable(ts, func(i, j int) bool {
		if ts[i].BPM != ts[j].BPM {
			return (ts[i].BPM < ts[j].BPM) != desc
		}
		return ts[i].ID < ts[j].ID
	})
}

// takeUntil returns how many tracks from ts reach share seconds; the track
// that reaches or crosses it is the last one taken.
func takeUntil(ts []Track, share float64) int {
	var s float64
	for i, t := range ts {
		if s >= share {
			return i
		}
		s += t.DurationSeconds
	}
	return len(ts)
}

func tag(ts []Track, s Section) []MixTrack {
	out := make([]MixTrack, len(ts))
	for i, t := range ts {
		out[i] = MixTrack{Track: t, Section: s}
	}
	return out
}

// Steady plays upbeat tracks in bpm order. minutes == 0 means no target.
func Steady(lib []Track, minutes float64) Mix {
	up := FindTracks(lib, Filter{Mood: Upbeat}, len(lib))
	n := len(up)
	if minutes > 0 {
		n = takeUntil(up, minutes*60)
	}
	m := Mix{Tracks: tag(up[:n], SteadySection)}
	m.Seconds = sum(up[:n])
	if minutes > 0 && m.Seconds < minutes*60 {
		m.Note = fmt.Sprintf("Only %s minutes of upbeat music in the library; the mix is %s minutes, not %s.",
			fmtMinutes(m.Seconds), fmtMinutes(m.Seconds), strconv.FormatFloat(minutes, 'f', -1, 64))
	}
	return m
}

func sum(ts []Track) float64 {
	var s float64
	for _, t := range ts {
		s += t.DurationSeconds
	}
	return s
}

// Arc builds warm-up, peak and cooldown sections. Peak is the upbeat tracks;
// warm-up and cooldown come from the rest, highest energy first. minutes == 0
// means no target.
func Arc(lib []Track, minutes float64) Mix {
	peak := FindTracks(lib, Filter{Mood: Upbeat}, len(lib))
	rest := []Track{}
	for _, t := range lib {
		if t.Status == "ready" && !Upbeat.Match(t) {
			rest = append(rest, t)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool {
		a, b := rest[i], rest[j]
		if a.Energy != b.Energy {
			return a.Energy > b.Energy
		}
		if a.BPM != b.BPM {
			return a.BPM > b.BPM
		}
		return a.ID < b.ID
	})

	var warm, cool []Track
	if minutes <= 0 {
		w := min(2, len(rest))
		warm = append(warm, rest[:w]...)
		c := min(2, len(rest)-w)
		cool = append(cool, rest[len(rest)-c:]...)
	} else {
		target := minutes * 60
		peak = peak[:takeUntil(peak, 0.6*target)]
		share := (target - sum(peak)) / 2
		w := takeUntil(rest, share)
		warm = append(warm, rest[:w]...)
		left := rest[w:]
		// Cooldown takes from the back, so walk the remainder reversed.
		rev := make([]Track, len(left))
		for i, t := range left {
			rev[len(left)-1-i] = t
		}
		cool = rev[:takeUntil(rev, share)]
		cool = append([]Track(nil), cool...)
	}
	sortByBPM(warm, false)
	sortByBPM(cool, true)

	m := Mix{Tracks: append(append(tag(warm, Warmup), tag(peak, Peak)...), tag(cool, Cooldown)...)}
	m.Seconds = sum(warm) + sum(peak) + sum(cool)
	ready := 0
	for _, t := range lib {
		if t.Status == "ready" {
			ready++
		}
	}
	if minutes > 0 && len(m.Tracks) == ready && m.Seconds < minutes*60 {
		m.Note = fmt.Sprintf("The whole library is only %s minutes; the mix is %s minutes, not %s.",
			fmtMinutes(m.Seconds), fmtMinutes(m.Seconds), strconv.FormatFloat(minutes, 'f', -1, 64))
	}
	return m
}

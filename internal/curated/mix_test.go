package curated

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"testing"
)

// loadFixture reads the library snapshot from 2026-10-24.
func loadFixture(t *testing.T) []Track {
	t.Helper()
	b, err := os.ReadFile("testdata/library-2026-10-24.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Items []Track `json:"items"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f.Items
}

func titles(ts []Track) []string {
	out := []string{}
	for _, t := range ts {
		out = append(out, t.Title)
	}
	return out
}

func mixTitles(m Mix) []string {
	out := []string{}
	for _, t := range m.Tracks {
		out = append(out, t.Title)
	}
	return out
}

func sections(m Mix) []Section {
	out := []Section{}
	for _, t := range m.Tracks {
		out = append(out, t.Section)
	}
	return out
}

func fmtMin(m float64) string { return strconv.FormatFloat(m, 'f', -1, 64) }

func TestUpbeatOnTodaysLibrary(t *testing.T) {
	got := titles(FindTracks(loadFixture(t), Filter{Mood: Upbeat}, 100))
	want := []string{"Scary Intro", "Q", "Studiokolomna Track 1", "Arpmedia Track 1", "Prettyjohn1 Track 1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMoodBoundaries(t *testing.T) {
	tr := func(bpm, energy float64, status string) Track {
		return Track{ID: "x", Status: status, BPM: bpm, Energy: energy}
	}
	cases := []struct {
		name string
		m    Mood
		t    Track
		want bool
	}{
		{"upbeat edge", Upbeat, tr(120, 0.6, "ready"), true},
		{"upbeat bpm low", Upbeat, tr(119.9, 0.9, "ready"), false},
		{"upbeat energy low", Upbeat, tr(150, 0.59, "ready"), false},
		{"calm 0.49", Calm, tr(90, 0.49, "ready"), true},
		{"calm 0.5", Calm, tr(90, 0.5, "ready"), false},
		{"any", Any, tr(0, 0, "ready"), true},
		{"upbeat not ready", Upbeat, tr(150, 0.9, "processing"), false},
		{"calm not ready", Calm, tr(90, 0.1, "failed"), false},
		{"any not ready", Any, tr(90, 0.1, "processing"), false},
	}
	for _, c := range cases {
		if got := FindTracks([]Track{c.t}, Filter{Mood: c.m}, 10); (len(got) == 1) != c.want {
			t.Errorf("%s: matched=%v, want %v", c.name, len(got) == 1, c.want)
		}
	}
}

func TestFilterNarrowsNeverLoosens(t *testing.T) {
	lib := []Track{
		{ID: "a", Title: "slow", Status: "ready", BPM: 110, Energy: 0.9},
		{ID: "b", Title: "fast", Status: "ready", BPM: 130, Energy: 0.9},
	}
	got := titles(FindTracks(lib, Filter{Mood: Upbeat, BPMMin: ptr(100.0)}, 10))
	// 110 BPM fails the mood on its own; BPMMin must not rescue it.
	if !reflect.DeepEqual(got, []string{"fast"}) {
		t.Fatalf("got %v", got)
	}
}

func TestFindTracksRangesAndLimit(t *testing.T) {
	lib := loadFixture(t)
	got := FindTracks(lib, Filter{BPMMin: ptr(100.0), BPMMax: ptr(125.0), EnergyMin: ptr(0.7), MinSeconds: ptr(100.0), MaxSeconds: ptr(200.0)}, 100)
	for _, tr := range got {
		if tr.BPM < 100 || tr.BPM > 125 || tr.Energy < 0.7 || tr.DurationSeconds < 100 || tr.DurationSeconds > 200 {
			t.Errorf("%s outside filter", tr.Title)
		}
	}
	if len(got) == 0 {
		t.Fatal("expected matches")
	}
	if n := len(FindTracks(lib, Filter{}, 3)); n != 3 {
		t.Fatalf("limit: got %d", n)
	}
}

func TestSteadyNoTarget(t *testing.T) {
	m := Steady(loadFixture(t), 0)
	if len(m.Tracks) != 5 || m.Note != "" {
		t.Fatalf("tracks=%d note=%q", len(m.Tracks), m.Note)
	}
	for _, tr := range m.Tracks {
		if tr.Section != SteadySection {
			t.Errorf("%s section %q", tr.Title, tr.Section)
		}
	}
}

func TestSteadyShortfallNote(t *testing.T) {
	lib := loadFixture(t)
	up := FindTracks(lib, Filter{Mood: Upbeat}, 100)
	m := Steady(lib, 30)
	if len(m.Tracks) != 5 {
		t.Fatalf("tracks=%d", len(m.Tracks))
	}
	have := fmtMin(Minutes(sum(up)))
	want := fmt.Sprintf("Only %s minutes of upbeat music in the library; the mix is %s minutes, not 30.", have, have)
	if m.Note != want {
		t.Fatalf("note %q, want %q", m.Note, want)
	}
}

func TestSteadyStopsAtTarget(t *testing.T) {
	lib := loadFixture(t)
	m := Steady(lib, 4)
	if m.Seconds < 240 || m.Note != "" {
		t.Fatalf("seconds=%v note=%q", m.Seconds, m.Note)
	}
	last := m.Tracks[len(m.Tracks)-1]
	if m.Seconds-last.DurationSeconds >= 240 {
		t.Fatalf("added a track past the target")
	}
	up := FindTracks(lib, Filter{Mood: Upbeat}, 100)
	for i, tr := range m.Tracks {
		if tr.ID != up[i].ID {
			t.Fatalf("track %d is %s, not bpm order", i, tr.Title)
		}
	}
}

func TestArcTodaysLibraryNoTarget(t *testing.T) {
	m := Arc(loadFixture(t), 0)
	want := []string{"Audiocoffee Track 1", "Guitar Chrome Etiquette", "Scary Intro", "Q", "Studiokolomna Track 1", "Arpmedia Track 1", "Prettyjohn1 Track 1", "Morning Fog", "Leberch Track 1"}
	if got := mixTitles(m); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	ws := []Section{Warmup, Warmup, Peak, Peak, Peak, Peak, Peak, Cooldown, Cooldown}
	if got := sections(m); !reflect.DeepEqual(got, ws) {
		t.Fatalf("sections %v", got)
	}
}

func TestArcDeterministic(t *testing.T) {
	lib := loadFixture(t)
	want := Arc(lib, 30)
	for seed := int64(1); seed <= 5; seed++ {
		sh := append([]Track(nil), lib...)
		rand.New(rand.NewSource(seed)).Shuffle(len(sh), func(i, j int) { sh[i], sh[j] = sh[j], sh[i] })
		if got := Arc(sh, 30); !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d differs", seed)
		}
	}
}

func TestArcWithTarget(t *testing.T) {
	lib := loadFixture(t)
	const T = 30 * 60.0
	m := Arc(lib, 30)

	var warm, peak, cool []Track
	seen := map[string]bool{}
	for _, mt := range m.Tracks {
		if seen[mt.ID] {
			t.Errorf("%s appears twice", mt.Title)
		}
		seen[mt.ID] = true
		switch mt.Section {
		case Warmup:
			warm = append(warm, mt.Track)
		case Peak:
			peak = append(peak, mt.Track)
		case Cooldown:
			cool = append(cool, mt.Track)
		default:
			t.Errorf("unexpected section %q", mt.Section)
		}
	}
	if len(peak) != 5 {
		t.Fatalf("peak has %d tracks", len(peak))
	}
	left := T - sum(peak)
	if len(warm) == 0 || len(cool) == 0 {
		t.Fatalf("warm=%d cool=%d", len(warm), len(cool))
	}
	// Sections are ordered warm, peak, cool in the output.
	for i := 1; i < len(warm); i++ {
		if warm[i].BPM < warm[i-1].BPM {
			t.Errorf("warm-up bpm decreases")
		}
	}
	for i := 1; i < len(cool); i++ {
		if cool[i].BPM > cool[i-1].BPM {
			t.Errorf("cooldown bpm increases")
		}
	}
	over := func(name string, ts []Track, share float64) {
		if s := sum(ts); s < share && len(seen) < 18 {
			t.Errorf("%s short of share without exhausting library", name)
		}
		if s := sum(ts[:len(ts)-1]); s >= share {
			t.Errorf("%s goes over by more than one track", name)
		}
	}
	over("warm", warm, left/2)
	over("cool", cool, left/2)
	if m.Seconds != sum(warm)+sum(peak)+sum(cool) {
		t.Errorf("seconds %v mismatch", m.Seconds)
	}
}

func TestArcLibraryTooShort(t *testing.T) {
	lib := loadFixture(t)
	m := Arc(lib, 120)
	if len(m.Tracks) != len(lib) {
		t.Fatalf("used %d of %d", len(m.Tracks), len(lib))
	}
	const p = "The whole library is only"
	if len(m.Note) < len(p) || m.Note[:len(p)] != p {
		t.Fatalf("note %q", m.Note)
	}
	x := fmtMin(Minutes(sum(lib)))
	if want := fmt.Sprintf("The whole library is only %s minutes; the mix is %s minutes, not 120.", x, x); m.Note != want {
		t.Fatalf("note %q, want %q", m.Note, want)
	}
}

func TestNoNilSlices(t *testing.T) {
	if Steady(nil, 0).Tracks == nil || Arc(nil, 0).Tracks == nil || FindTracks(nil, Filter{}, 5) == nil {
		t.Fatal("nil slice returned")
	}
}

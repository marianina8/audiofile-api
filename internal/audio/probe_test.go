package audio

import (
	"context"
	"math"
	"testing"

	"github.com/marianina8/audiofile-api/internal/testutil"
)

func TestProbe_Tags(t *testing.T) {
	testutil.RequireFFmpeg(t)
	p := writeTestMP3(t, "-metadata", "title=T", "-metadata", "artist=A", "-metadata", "genre=Rock",
		"-metadata", "TBPM=124", "-metadata", "date=2017", "-metadata", "track=9/12")
	got, err := Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}.Probe(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "T" || got.Artist != "A" || got.Genre != "Rock" {
		t.Errorf("tags = %+v", got)
	}
	if got.TBPM != "124" {
		t.Errorf("TBPM = %q", got.TBPM)
	}
	if got.Year != 2017 || got.TrackNumber != 9 {
		t.Errorf("year=%d track=%d", got.Year, got.TrackNumber)
	}
	if math.Abs(got.DurationSeconds-10) > 0.1 {
		t.Errorf("duration = %v", got.DurationSeconds)
	}
}

func TestProbe_NoTags(t *testing.T) {
	testutil.RequireFFmpeg(t)
	p := writeTestMP3(t, "-write_id3v2", "0", "-id3v2_version", "0")
	got, err := Tools{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}.Probe(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "" || got.Artist != "" || got.TBPM != "" || got.Year != 0 || got.TrackNumber != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
}

func TestProbe_Error(t *testing.T) {
	testutil.RequireFFmpeg(t)
	_, err := Tools{FFprobe: "ffprobe"}.Probe(context.Background(), "/nonexistent/x.mp3")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseProbeJSON_Merge(t *testing.T) {
	js := `{"format":{"duration":"3.5","tags":{"TITLE":"Fmt","Copyright":"(c) X"}},
	 "streams":[{"tags":{"title":"Stream","artist":"S","LYRICS-eng":"la la","bpm":" 99 ","TDRC":"2020-05-01"}}]}`
	p, err := parseProbeJSON([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Fmt" || p.Artist != "S" || p.Lyrics != "la la" || p.TBPM != "99" ||
		p.Copyright != "(c) X" || p.Year != 2020 || p.DurationSeconds != 3.5 {
		t.Errorf("got %+v", p)
	}
}

func TestParseTBPM(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"124", 124, true}, {"124.5", 124.5, true}, {" 90 ", 90, true}, {"124.54", 124.5, true},
		{"40", 40, true}, {"250", 250, true},
		{"300", 0, false}, {"39", 0, false}, {"", 0, false}, {"abc", 0, false},
	}
	for _, c := range cases {
		got, ok := ParseTBPM(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ParseTBPM(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

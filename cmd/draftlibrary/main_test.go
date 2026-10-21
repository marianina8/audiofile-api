package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDraft(t *testing.T) {
	got := draft([]string{
		"alex-morgan-no-copyright-music-578487.mp3",
		"audiocoffee-rock-no-copyright-music-537648.mp3",
		"cinematic-soul-guitar-background-music-chrome-etiquette-587088.mp3",
		"freesoundserver-dancing-in-the-stardust-free-music-no-copyright-203603 (1).mp3",
		"leberch-no-copyright-music-594951.mp3",
		"leberch-no-copyright-music-594952.mp3",
		"poorartistt-iron-ashes-rising-electro-industrial-musicno-copyright-390866.mp3",
		"tazdev_music-q-265016.mp3",
		"dailyvibe-upbeat-upbeat-music-614445.mp3",
		"cinematic-soul-scary-scary-intro-background-music-617920.mp3",
		"xhowiexo-madewithlove-the-zodiac-149319.mp3",
		"silentvoice-abstract-piano-392339.mp3",
		"poorartistt-machine-blood-ritual-electro-industrial-musicno-copyright-390863.mp3",
	})
	want := [][]string{
		{"alex-morgan-no-copyright-music-578487.mp3", "Alex Morgan Track 1", "Alex Morgan", "", "", "", ""},
		{"audiocoffee-rock-no-copyright-music-537648.mp3", "Audiocoffee Track 1", "Audiocoffee", "", "Rock", "filename", ""},
		{"cinematic-soul-guitar-background-music-chrome-etiquette-587088.mp3", "Guitar Chrome Etiquette", "Cinematic Soul", "", "", "", ""},
		{"freesoundserver-dancing-in-the-stardust-free-music-no-copyright-203603 (1).mp3", "Dancing In The Stardust", "FreeSoundServer", "", "", "", ""},
		{"leberch-no-copyright-music-594951.mp3", "Leberch Track 1", "Leberch", "", "", "", ""},
		{"leberch-no-copyright-music-594952.mp3", "Leberch Track 2", "Leberch", "", "", "", ""},
		{"poorartistt-iron-ashes-rising-electro-industrial-musicno-copyright-390866.mp3", "Iron Ashes Rising", "Poorartistt", "", "Industrial", "filename", ""},
		{"tazdev_music-q-265016.mp3", "Q", "Tazdev Music", "", "", "", ""},
		{"dailyvibe-upbeat-upbeat-music-614445.mp3", "Dailyvibe Track 1", "Dailyvibe", "", "Pop", "filename", ""},
		{"cinematic-soul-scary-scary-intro-background-music-617920.mp3", "Scary Intro", "Cinematic Soul", "", "", "", ""},
		{"xhowiexo-madewithlove-the-zodiac-149319.mp3", "The Zodiac", "Xhowiexo", "", "", "", ""},
		{"silentvoice-abstract-piano-392339.mp3", "Abstract Piano", "Silentvoice", "", "Classical", "filename", ""},
		{"poorartistt-machine-blood-ritual-electro-industrial-musicno-copyright-390863.mp3", "Machine Blood Ritual", "Poorartistt", "", "Industrial", "filename", ""},
	}
	if !reflect.DeepEqual(got, want) {
		for i := range got {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Errorf("row %d:\n got %q\nwant %q", i, got[i], want[i])
			}
		}
	}
}

func TestCSVQuotingRoundTripAndToJSON(t *testing.T) {
	title := `Hello, "World"`
	rows := [][]string{{"a.mp3", title, "Art, Ist", "", "", "", ""}}
	var buf bytes.Buffer
	if err := writeCSV(&buf, rows); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"Hello, ""World"""`)) {
		t.Errorf("title not RFC 4180 quoted: %s", buf.String())
	}
	path := filepath.Join(t.TempDir(), "library.csv")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := csvToJSON(path, &out); err != nil {
		t.Fatal(err)
	}
	var got []map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0]["title"] != title || got[0]["artist"] != "Art, Ist" || got[0]["filename"] != "a.mp3" {
		t.Errorf("to-json = %v", got)
	}
}

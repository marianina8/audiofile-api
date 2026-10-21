package main

import (
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
	})
	want := [][]string{
		{"alex-morgan-no-copyright-music-578487.mp3", "Alex Morgan Track 1", "Alex Morgan", "", "", "", ""},
		{"audiocoffee-rock-no-copyright-music-537648.mp3", "Rock", "Audiocoffee", "", "Rock", "filename", ""},
		{"cinematic-soul-guitar-background-music-chrome-etiquette-587088.mp3", "Guitar Chrome Etiquette", "Cinematic Soul", "", "", "", ""},
		{"freesoundserver-dancing-in-the-stardust-free-music-no-copyright-203603 (1).mp3", "Dancing In The Stardust", "FreeSoundServer", "", "", "", ""},
		{"leberch-no-copyright-music-594951.mp3", "Leberch Track 1", "Leberch", "", "", "", ""},
		{"leberch-no-copyright-music-594952.mp3", "Leberch Track 2", "Leberch", "", "", "", ""},
		{"poorartistt-iron-ashes-rising-electro-industrial-musicno-copyright-390866.mp3", "Iron Ashes Rising Electro Industrial", "Poorartistt", "", "Industrial", "filename", ""},
		{"tazdev_music-q-265016.mp3", "Q", "Tazdev Music", "", "", "", ""},
	}
	if !reflect.DeepEqual(got, want) {
		for i := range got {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Errorf("row %d:\n got %q\nwant %q", i, got[i], want[i])
			}
		}
	}
}

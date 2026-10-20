package processor

import "testing"

func TestTitleFromFilename(t *testing.T) {
	cases := []struct{ in, want string }{
		{"alex-morgan-no-copyright-music-578487.mp3", "Alex Morgan"},
		{"apalonbeats-no-copyright-music-597079.mp3", "Apalonbeats"},
		{"arpmedia-no-copyright-music-569463.mp3", "Arpmedia"},
		{"audiocoffee-rock-no-copyright-music-537648.mp3", "Audiocoffee Rock"},
		{"cinematic-soul-guitar-background-music-chrome-etiquette-587088.mp3", "Cinematic Soul Guitar Chrome Etiquette"},
		{"cinematic-soul-scary-scary-intro-background-music-617920.mp3", "Cinematic Soul Scary Scary Intro"},
		{"dailyvibe-upbeat-upbeat-music-614445.mp3", "Dailyvibe Upbeat Upbeat"},
		{"freesoundserver-dancing-in-the-stardust-free-music-no-copyright-203603 (1).mp3", "Freesoundserver Dancing In The Stardust"},
		{"freesoundserver-dancing-in-the-stardust-free-music-no-copyright-203603.mp3", "Freesoundserver Dancing In The Stardust"},
		{"leberch-no-copyright-music-594951.mp3", "Leberch"},
		{"nastelbom-no-copyright-music-602456.mp3", "Nastelbom"},
		{"poorartistt-iron-ashes-rising-electro-industrial-musicno-copyright-390866.mp3", "Poorartistt Iron Ashes Rising Electro Industrial"},
		{"poorartistt-machine-blood-ritual-electro-industrial-musicno-copyright-390863.mp3", "Poorartistt Machine Blood Ritual Electro Industrial"},
		{"prettyjohn1-no-copyright-music-530633.mp3", "Prettyjohn1"},
		{"silentvoice-abstract-piano-392339.mp3", "Silentvoice Abstract Piano"},
		{"silentvoice-morning-fog-263352.mp3", "Silentvoice Morning Fog"},
		{"studiokolomna-no-copyright-music-483817.mp3", "Studiokolomna"},
		{"tazdev_music-q-265016.mp3", "Tazdev Q"},
		{"xhowiexo-madewithlove-the-zodiac-149319.mp3", "Xhowiexo Madewithlove The Zodiac"},
		{"", "Untitled"},
		{"123456.mp3", "Untitled"},
		{"no-copyright-music.mp3", "Untitled"},
	}
	for _, c := range cases {
		if got := TitleFromFilename(c.in); got != c.want {
			t.Errorf("TitleFromFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

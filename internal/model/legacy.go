package model

import "encoding/json"

// Legacy shapes from the book's audiofile API. Field names and JSON tags are
// kept exactly as the book defines them.

type Audio struct {
	Id       string   `json:"Id"`
	Path     string   `json:"Path"`
	Metadata Metadata `json:"Metadata"`
	Status   string   `json:"Status"`
	Error    []string `json:"Error"`
}

type Metadata struct {
	Tags       Tags   `json:"tags"`
	Transcript string `json:"transcript"`
}

type Tags struct {
	Title       string `json:"title"`
	Album       string `json:"album"`
	Artist      string `json:"artist"`
	AlbumArtist string `json:"album_artist"`
	Composer    string `json:"composer"`
	Genre       string `json:"genre"`
	Year        int    `json:"year"`
	Lyrics      string `json:"lyrics"`
	Comment     string `json:"comment"`
}

// JSON marshals the audio indented with 4 spaces, as the book does.
func (a Audio) JSON() (string, error) {
	b, err := json.MarshalIndent(a, "", "    ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// AudioFromTrack converts a Track to the legacy Audio shape.
func AudioFromTrack(t Track) Audio {
	status := "Complete"
	if t.Status == StatusProcessing {
		status = "Initiating"
	}
	errs := []string{}
	if t.Status == StatusFailed {
		errs = []string{t.Error}
	}
	return Audio{
		Id:   t.ID,
		Path: t.S3Key,
		Metadata: Metadata{
			Tags: Tags{
				Title:       t.Title,
				Album:       t.Album,
				Artist:      t.Artist,
				AlbumArtist: t.AlbumArtist,
				Composer:    t.Composer,
				Genre:       t.Genre,
				Year:        t.Year,
				Lyrics:      t.Lyrics,
				Comment:     t.Comment,
			},
			Transcript: "",
		},
		Status: status,
		Error:  errs,
	}
}

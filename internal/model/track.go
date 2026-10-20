package model

// TrackStatus is the processing state of a track.
type TrackStatus string

const (
	StatusProcessing TrackStatus = "processing"
	StatusReady      TrackStatus = "ready"
	StatusFailed     TrackStatus = "failed"
)

// Valid reports whether s is a known status.
func (s TrackStatus) Valid() bool {
	switch s {
	case StatusProcessing, StatusReady, StatusFailed:
		return true
	}
	return false
}

// BPMSource says where a BPM value came from.
type BPMSource string

const (
	BPMTag      BPMSource = "tag"
	BPMMeasured BPMSource = "measured"
	BPMManual   BPMSource = "manual"
)

// Valid reports whether s is a known BPM source.
func (s BPMSource) Valid() bool {
	switch s {
	case BPMTag, BPMMeasured, BPMManual:
		return true
	}
	return false
}

// Track is a row in the tracks table and the API's track resource.
type Track struct {
	ID               string      `json:"id" dynamodbav:"id"`
	Status           TrackStatus `json:"status" dynamodbav:"status"`
	Error            string      `json:"error,omitempty" dynamodbav:"error,omitempty"`
	Title            string      `json:"title" dynamodbav:"title"`
	Artist           string      `json:"artist" dynamodbav:"artist"`
	ArtistID         string      `json:"artist_id" dynamodbav:"artist_id"`
	Album            string      `json:"album" dynamodbav:"album"`
	AlbumID          string      `json:"album_id,omitempty" dynamodbav:"album_id,omitempty"`
	TrackNumber      int         `json:"track_number" dynamodbav:"track_number"`
	AlbumArtist      string      `json:"album_artist" dynamodbav:"album_artist"`
	Composer         string      `json:"composer" dynamodbav:"composer"`
	Comment          string      `json:"comment" dynamodbav:"comment"`
	Genre            string      `json:"genre" dynamodbav:"genre"`
	Year             int         `json:"year" dynamodbav:"year"`
	Lyrics           string      `json:"lyrics" dynamodbav:"lyrics"`
	Copyright        string      `json:"copyright" dynamodbav:"copyright"`
	BPM              float64     `json:"bpm" dynamodbav:"bpm"`
	BPMSource        BPMSource   `json:"bpm_source" dynamodbav:"bpm_source"`
	Energy           float64     `json:"energy" dynamodbav:"energy"`
	DurationSeconds  float64     `json:"duration_seconds" dynamodbav:"duration_seconds"`
	S3Key            string      `json:"s3_key" dynamodbav:"s3_key"`
	OriginalFilename string      `json:"original_filename" dynamodbav:"original_filename"`
	SizeBytes        int64       `json:"size_bytes" dynamodbav:"size_bytes"`
	ContentSHA256    string      `json:"content_sha256" dynamodbav:"content_sha256"`
	Tags             []string    `json:"tags" dynamodbav:"tags,stringset,omitempty"`
	CreatedAt        string      `json:"created_at" dynamodbav:"created_at"`
	UpdatedAt        string      `json:"updated_at" dynamodbav:"updated_at"`
}

// JSONTags returns Tags, never nil, so the API renders [] rather than null.
func (t Track) JSONTags() []string {
	if t.Tags == nil {
		return []string{}
	}
	return t.Tags
}

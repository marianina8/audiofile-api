package model

// Artist is a row in the artists table. Counts are computed, never stored.
type Artist struct {
	ID        string `json:"id" dynamodbav:"id"`
	Name      string `json:"name" dynamodbav:"name"`
	CreatedAt string `json:"created_at" dynamodbav:"created_at"`
}

// Album is a row in the albums table.
type Album struct {
	ID        string `json:"id" dynamodbav:"id"`
	Title     string `json:"title" dynamodbav:"title"`
	ArtistID  string `json:"artist_id" dynamodbav:"artist_id"`
	Artist    string `json:"artist" dynamodbav:"artist"`
	Year      int    `json:"year" dynamodbav:"year"`
	CreatedAt string `json:"created_at" dynamodbav:"created_at"`
}

package model

// Playlist is a row in the playlists table. TrackIDs is ordered and stored as
// a DynamoDB list. Version counts conditional updates (store.UpdatePlaylist);
// rows written before versioning have no version attribute and read as 0.
type Playlist struct {
	ID          string   `json:"id" dynamodbav:"id"`
	Name        string   `json:"name" dynamodbav:"name"`
	Description string   `json:"description" dynamodbav:"description"`
	TrackIDs    []string `json:"track_ids" dynamodbav:"track_ids"`
	CreatedAt   string   `json:"created_at" dynamodbav:"created_at"`
	UpdatedAt   string   `json:"updated_at" dynamodbav:"updated_at"`
	Version     int      `json:"-" dynamodbav:"version,omitempty"`
}

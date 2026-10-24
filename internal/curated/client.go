// Package curated is Server B: a small, designed MCP server over the audiofile API.
package curated

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// ErrNotFound wraps API 404s.
var ErrNotFound = errors.New("not found")

type Track struct {
	ID              string  `json:"id"`
	Title           string  `json:"title"`
	Artist          string  `json:"artist"`
	Status          string  `json:"status"`
	BPM             float64 `json:"bpm"`
	Energy          float64 `json:"energy"`
	DurationSeconds float64 `json:"duration_seconds"`
}

type Playlist struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	TrackIDs    []string `json:"track_ids"`
}

// API is everything the curated tools need from the audiofile API.
type API interface {
	Tracks(ctx context.Context) ([]Track, error)
	Playlists(ctx context.Context) ([]Playlist, error)
	Playlist(ctx context.Context, id string) (Playlist, error)
	CreatePlaylist(ctx context.Context, name, description string, trackIDs []string) (Playlist, error)
	AddTracks(ctx context.Context, id string, trackIDs []string) error
	RemoveTrack(ctx context.Context, id, trackID string) error
	Reorder(ctx context.Context, id string, trackIDs []string) error
	DeletePlaylist(ctx context.Context, id string) error
	PlayerLink(ctx context.Context, id string) (string, error)
}

// Client calls the audiofile API with the machine token. The caller's own
// token is never forwarded.
type Client struct {
	base string
	tok  TokenSource
	hc   *http.Client
}

var _ API = (*Client)(nil)

func NewClient(baseURL string, tok TokenSource, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{base: baseURL, tok: tok, hc: hc}
}

func (c *Client) Tracks(ctx context.Context) ([]Track, error) {
	all := []Track{}
	cursor := ""
	for {
		q := url.Values{"limit": {"100"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page struct {
			Items      []Track `json:"items"`
			NextCursor string  `json:"next_cursor"`
		}
		if err := c.do(ctx, "list tracks", http.MethodGet, "/tracks?"+q.Encode(), nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if page.NextCursor == "" {
			return all, nil
		}
		if page.NextCursor == cursor {
			// Following it again would loop forever.
			return nil, errors.New("list tracks: API returned the same page cursor twice")
		}
		cursor = page.NextCursor
	}
}

func (c *Client) Playlists(ctx context.Context) ([]Playlist, error) {
	var out struct {
		Items []Playlist `json:"items"`
	}
	if err := c.do(ctx, "list playlists", http.MethodGet, "/playlists", nil, &out); err != nil {
		return nil, err
	}
	if out.Items == nil {
		out.Items = []Playlist{}
	}
	return out.Items, nil
}

func (c *Client) Playlist(ctx context.Context, id string) (Playlist, error) {
	var p Playlist
	err := c.do(ctx, "get playlist", http.MethodGet, "/playlists/"+url.PathEscape(id), nil, &p)
	return p, err
}

func (c *Client) CreatePlaylist(ctx context.Context, name, description string, trackIDs []string) (Playlist, error) {
	var p Playlist
	body := map[string]any{"name": name, "description": description, "track_ids": nonNil(trackIDs)}
	err := c.do(ctx, "create playlist", http.MethodPost, "/playlists", body, &p)
	return p, err
}

func (c *Client) AddTracks(ctx context.Context, id string, trackIDs []string) error {
	return c.do(ctx, "add tracks", http.MethodPost, "/playlists/"+url.PathEscape(id)+"/tracks", map[string]any{"track_ids": nonNil(trackIDs)}, nil)
}

func (c *Client) RemoveTrack(ctx context.Context, id, trackID string) error {
	return c.do(ctx, "remove track", http.MethodDelete, "/playlists/"+url.PathEscape(id)+"/tracks/"+url.PathEscape(trackID), nil, nil)
}

func (c *Client) Reorder(ctx context.Context, id string, trackIDs []string) error {
	return c.do(ctx, "reorder playlist", http.MethodPut, "/playlists/"+url.PathEscape(id)+"/tracks/order", map[string]any{"track_ids": nonNil(trackIDs)}, nil)
}

func (c *Client) DeletePlaylist(ctx context.Context, id string) error {
	return c.do(ctx, "delete playlist", http.MethodDelete, "/playlists/"+url.PathEscape(id), nil, nil)
}

func (c *Client) PlayerLink(ctx context.Context, id string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	if err := c.do(ctx, "create player link", http.MethodPost, "/playlists/"+url.PathEscape(id)+"/player", nil, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// do sends one request. A 401 invalidates the token and retries exactly once.
// Errors never carry the request URL.
func (c *Client) do(ctx context.Context, op, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("%s: encode request: %w", op, err)
		}
	}
	for attempt := 0; ; attempt++ {
		tok, err := c.tok.Token(ctx)
		if err != nil {
			return fmt.Errorf("%s: %w", op, err)
		}
		status, data, err := c.send(ctx, method, path, tok, payload)
		if err != nil {
			return fmt.Errorf("%s: request failed: %w", op, transportError(err, "the API"))
		}
		if status == http.StatusUnauthorized {
			c.tok.Invalidate()
			if attempt == 0 {
				continue
			}
			return fmt.Errorf("%s: API rejected the server's credentials", op)
		}
		if status >= 300 {
			return apiError(op, status, data)
		}
		if out != nil && len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("%s: unreadable API response", op)
			}
		}
		return nil
	}
}

func (c *Client) send(ctx context.Context, method, path, tok string, payload []byte) (int, []byte, error) {
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}

func apiError(op string, status int, data []byte) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := fmt.Sprintf("%s: API returned %d", op, status)
	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		msg += ": " + e.Error.Message
	}
	if status == http.StatusNotFound {
		return fmt.Errorf("%s: %w", msg, ErrNotFound)
	}
	return errors.New(msg)
}

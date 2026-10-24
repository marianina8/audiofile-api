package curated

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const moodRules = "Moods: upbeat = 120+ BPM and energy 0.6+; calm = energy below 0.5; any = no rule. Only ready tracks are used."

var findTracksTool = &mcp.Tool{
	Name: "find_tracks",
	Description: "Search the music library and list matching tracks, sorted by BPM, with a count and total minutes. " + moodRules +
		" Explicit bpm, energy and length limits narrow the mood further; they never loosen it. " +
		"It only reads: it won't change playlists or tracks.",
	Annotations: &mcp.ToolAnnotations{Title: "Find tracks", ReadOnlyHint: true, OpenWorldHint: ptr(false)},
}

var buildMixTool = &mcp.Tool{
	Name: "build_workout_mix",
	Description: "Build a workout playlist in one call: pick the tracks, create the playlist, and return a player link. " + moodRules +
		" shape=steady (default) uses upbeat tracks only, in BPM order; shape=arc goes warm-up, then upbeat peak, then cooldown, with each track labelled. " +
		"With minutes it stops once the target is reached (it may run over by one track). " +
		"It never pads with tracks that don't qualify; if there isn't enough music it says so in `note`. " +
		"If the name is taken it adds 2, 3 and so on. It won't delete or change existing playlists or tracks.",
	Annotations: &mcp.ToolAnnotations{Title: "Build workout mix", DestructiveHint: ptr(false), OpenWorldHint: ptr(false)},
}

var listPlaylistsTool = &mcp.Tool{
	Name:        "list_playlists",
	Description: "List every playlist with its id, name, track count and total minutes. It only reads: it won't show the tracks (use show_playlist) or change anything.",
	Annotations: &mcp.ToolAnnotations{Title: "List playlists", ReadOnlyHint: true, OpenWorldHint: ptr(false)},
}

var showPlaylistTool = &mcp.Tool{
	Name: "show_playlist",
	Description: "Show one playlist: its tracks in order (BPM, energy, seconds), total minutes, and a player link. " +
		"Give the playlist's name or id (an id wins over a name). Opening a player link changes nothing in the library. It won't guess: an unknown or ambiguous name is an error.",
	Annotations: &mcp.ToolAnnotations{Title: "Show playlist", ReadOnlyHint: true, OpenWorldHint: ptr(false)},
}

var editPlaylistTool = &mcp.Tool{
	Name: "edit_playlist",
	Description: "Change one named playlist. Applies, in this order: remove_track_ids and remove_shorter_than_seconds, then add_track_ids, then order. " +
		"order: keep (default), bpm_asc, bpm_desc, or arc (upbeat tracks form the peak; the rest are split by energy into warm-up and cooldown). " + moodRules +
		" The playlist is required, as a name or id; it never edits 'the latest' playlist, and it won't delete the playlist or any track from the library.",
	Annotations: &mcp.ToolAnnotations{Title: "Edit playlist", DestructiveHint: ptr(false), OpenWorldHint: ptr(false)},
}

var deletePlaylistTool = &mcp.Tool{
	Name:        "delete_playlist",
	Description: "Deletes one playlist, named by name or id. Tracks are never deleted; they stay in the library. It won't guess: an unknown or ambiguous name is an error.",
	Annotations: &mcp.ToolAnnotations{Title: "Delete playlist", DestructiveHint: ptr(true), OpenWorldHint: ptr(false)},
}

// TrackOut is how a track is shown to the model.
type TrackOut struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Artist  string  `json:"artist"`
	BPM     float64 `json:"bpm"`
	Energy  float64 `json:"energy"`
	Seconds float64 `json:"seconds"`
	Section string  `json:"section,omitempty" jsonschema:"warm-up, peak, cooldown or steady (build_workout_mix only)"`
}

func trackOut(t Track, s Section) TrackOut {
	return TrackOut{ID: t.ID, Title: t.Title, Artist: t.Artist, BPM: t.BPM, Energy: t.Energy, Seconds: t.DurationSeconds, Section: string(s)}
}

func tracksOut(ts []Track) []TrackOut {
	out := make([]TrackOut, len(ts))
	for i, t := range ts {
		out[i] = trackOut(t, "")
	}
	return out
}

func mixOut(ms []MixTrack) []TrackOut {
	out := make([]TrackOut, len(ms))
	for i, m := range ms {
		out[i] = trackOut(m.Track, m.Section)
	}
	return out
}

type findInput struct {
	Mood       string   `json:"mood,omitempty" jsonschema:"upbeat, calm or any (default any)"`
	BPMMin     *float64 `json:"bpm_min,omitempty" jsonschema:"minimum BPM"`
	BPMMax     *float64 `json:"bpm_max,omitempty" jsonschema:"maximum BPM"`
	EnergyMin  *float64 `json:"energy_min,omitempty" jsonschema:"minimum energy, 0 to 1"`
	EnergyMax  *float64 `json:"energy_max,omitempty" jsonschema:"maximum energy, 0 to 1"`
	MinSeconds *float64 `json:"min_seconds,omitempty" jsonschema:"minimum track length in seconds"`
	MaxSeconds *float64 `json:"max_seconds,omitempty" jsonschema:"maximum track length in seconds"`
	Limit      *int     `json:"limit,omitempty" jsonschema:"how many tracks to return, 1 to 100 (default 25)"`
}

type findOutput struct {
	Tracks  []TrackOut `json:"tracks"`
	Count   int        `json:"count"`
	Minutes float64    `json:"minutes"`
}

type buildInput struct {
	Name    string   `json:"name,omitempty" jsonschema:"playlist name (default Upbeat Workout Mix)"`
	Minutes *float64 `json:"minutes,omitempty" jsonschema:"target length in minutes, 1 to 600 (default: no target)"`
	Shape   string   `json:"shape,omitempty" jsonschema:"steady or arc (default steady)"`
}

type buildOutput struct {
	PlaylistID string     `json:"playlist_id"`
	Name       string     `json:"name"`
	Tracks     []TrackOut `json:"tracks"`
	Minutes    float64    `json:"minutes"`
	PlayerLink string     `json:"player_link"`
	Note       string     `json:"note" jsonschema:"set when there wasn't enough music to meet the target; empty otherwise"`
}

type listInput struct{}

type playlistSummary struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	TrackCount int     `json:"track_count"`
	Minutes    float64 `json:"minutes"`
}

type listOutput struct {
	Playlists []playlistSummary `json:"playlists"`
}

type playlistInput struct {
	Playlist string `json:"playlist" jsonschema:"playlist name (case-insensitive) or id"`
}

type showOutput struct {
	PlaylistID string     `json:"playlist_id"`
	Name       string     `json:"name"`
	Tracks     []TrackOut `json:"tracks"`
	Minutes    float64    `json:"minutes"`
	PlayerLink string     `json:"player_link"`
}

type editInput struct {
	Playlist                 string   `json:"playlist" jsonschema:"playlist name (case-insensitive) or id; required"`
	AddTrackIDs              []string `json:"add_track_ids,omitempty" jsonschema:"ids of library tracks to add"`
	RemoveTrackIDs           []string `json:"remove_track_ids,omitempty" jsonschema:"ids of tracks to remove from this playlist"`
	RemoveShorterThanSeconds *float64 `json:"remove_shorter_than_seconds,omitempty" jsonschema:"remove tracks shorter than this many seconds"`
	Order                    string   `json:"order,omitempty" jsonschema:"keep, bpm_asc, bpm_desc or arc (default keep)"`
}

type editOutput struct {
	PlaylistID string     `json:"playlist_id"`
	Name       string     `json:"name"`
	Removed    []string   `json:"removed" jsonschema:"titles of removed tracks"`
	Added      []string   `json:"added" jsonschema:"titles of added tracks"`
	Tracks     []TrackOut `json:"tracks"`
	Minutes    float64    `json:"minutes"`
	PlayerLink string     `json:"player_link"`
}

type deleteOutput struct {
	Deleted string `json:"deleted" jsonschema:"name of the deleted playlist"`
}

type tools struct {
	api API
}

func parseMood(s string) (Mood, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "any":
		return Any, nil
	case "upbeat":
		return Upbeat, nil
	case "calm":
		return Calm, nil
	}
	return "", fmt.Errorf("unknown mood %q: use upbeat|calm|any", s)
}

func (t *tools) findTracks(ctx context.Context, in findInput) (*mcp.CallToolResult, findOutput, error) {
	mood, err := parseMood(in.Mood)
	if err != nil {
		return toolError(err.Error()), findOutput{}, nil
	}
	limit := 25
	if in.Limit != nil {
		limit = *in.Limit
	}
	if limit < 1 || limit > 100 {
		return toolError("limit must be between 1 and 100"), findOutput{}, nil
	}
	lib, err := t.api.Tracks(ctx)
	if err != nil {
		return toolError(err.Error()), findOutput{}, nil
	}
	found := FindTracks(lib, Filter{Mood: mood, BPMMin: in.BPMMin, BPMMax: in.BPMMax,
		EnergyMin: in.EnergyMin, EnergyMax: in.EnergyMax, MinSeconds: in.MinSeconds, MaxSeconds: in.MaxSeconds}, limit)
	out := findOutput{Tracks: tracksOut(found), Count: len(found), Minutes: Minutes(sum(found))}
	kind := "tracks"
	if mood != Any {
		kind = string(mood) + " tracks"
	}
	return textResult(fmt.Sprintf("%d %s, %s minutes: %s", out.Count, kind, fmtMinutes(sum(found)), joinTitles(found))), out, nil
}

func joinTitles(ts []Track) string {
	names := make([]string, len(ts))
	for i, t := range ts {
		names[i] = t.Title
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func (t *tools) buildWorkoutMix(ctx context.Context, in buildInput) (*mcp.CallToolResult, buildOutput, error) {
	minutes := 0.0
	if in.Minutes != nil {
		minutes = *in.Minutes
		if minutes < 1 || minutes > 600 {
			return toolError("minutes must be between 1 and 600"), buildOutput{}, nil
		}
	}
	shape := in.Shape
	if shape == "" {
		shape = "steady"
	}
	if shape != "steady" && shape != "arc" {
		return toolError(fmt.Sprintf("unknown shape %q: use steady|arc", in.Shape)), buildOutput{}, nil
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Upbeat Workout Mix"
	}
	lib, err := t.api.Tracks(ctx)
	if err != nil {
		return toolError(err.Error()), buildOutput{}, nil
	}
	var mix Mix
	if shape == "arc" {
		mix = Arc(lib, minutes)
	} else {
		mix = Steady(lib, minutes)
	}
	if len(mix.Tracks) == 0 {
		return toolError("no tracks qualify, so no playlist was created. " + moodRules), buildOutput{}, nil
	}
	existing, err := t.api.Playlists(ctx)
	if err != nil {
		return toolError(err.Error()), buildOutput{}, nil
	}
	name = uniqueName(existing, name)
	ids := make([]string, len(mix.Tracks))
	for i, m := range mix.Tracks {
		ids[i] = m.ID
	}
	pl, err := t.api.CreatePlaylist(ctx, name, "", ids)
	if err != nil {
		return toolError(fmt.Sprintf("creating playlist '%s' failed: %v. It may have been created anyway; check list_playlists before retrying.", name, err)), buildOutput{}, nil
	}
	link, err := t.api.PlayerLink(ctx, pl.ID)
	if err != nil {
		return toolError(fmt.Sprintf("playlist '%s' (id %s) was created with %d tracks, but the player link failed: %v. Show it by name or delete it.",
			pl.Name, pl.ID, len(ids), err)), buildOutput{}, nil
	}
	out := buildOutput{PlaylistID: pl.ID, Name: pl.Name, Tracks: mixOut(mix.Tracks), Minutes: Minutes(mix.Seconds), PlayerLink: link, Note: mix.Note}
	msg := fmt.Sprintf("Created '%s': %d tracks, %s minutes. Player: %s", pl.Name, len(mix.Tracks), fmtMinutes(mix.Seconds), link)
	if mix.Note != "" {
		msg += "\n" + mix.Note
	}
	return textResult(msg), out, nil
}

func (t *tools) listPlaylists(ctx context.Context, _ listInput) (*mcp.CallToolResult, listOutput, error) {
	ps, err := t.api.Playlists(ctx)
	if err != nil {
		return toolError(err.Error()), listOutput{}, nil
	}
	lib, err := t.api.Tracks(ctx)
	if err != nil {
		return toolError(err.Error()), listOutput{}, nil
	}
	byID := trackIndex(lib)
	out := listOutput{Playlists: []playlistSummary{}}
	lines := []string{}
	for _, p := range ps {
		// The list endpoint has no durations, so fetch each playlist's own track list.
		full, err := t.api.Playlist(ctx, p.ID)
		if err != nil {
			return toolError(err.Error()), listOutput{}, nil
		}
		secs := sum(lookup(byID, full.TrackIDs))
		out.Playlists = append(out.Playlists, playlistSummary{ID: p.ID, Name: p.Name, TrackCount: len(full.TrackIDs), Minutes: Minutes(secs)})
		lines = append(lines, fmt.Sprintf("%s (%d tracks, %s min)", p.Name, len(full.TrackIDs), fmtMinutes(secs)))
	}
	if len(lines) == 0 {
		return textResult("No playlists."), out, nil
	}
	return textResult(strings.Join(lines, "\n")), out, nil
}

func trackIndex(lib []Track) map[string]Track {
	m := make(map[string]Track, len(lib))
	for _, t := range lib {
		m[t.ID] = t
	}
	return m
}

// lookup maps ids to tracks, in order. An id the library doesn't know is
// shown with its id as the title, so nothing silently disappears.
func lookup(byID map[string]Track, ids []string) []Track {
	out := make([]Track, len(ids))
	for i, id := range ids {
		t, ok := byID[id]
		if !ok {
			t = Track{ID: id, Title: id}
		}
		out[i] = t
	}
	return out
}

// resolve fetches the playlists and picks one by name or id.
func (t *tools) resolve(ctx context.Context, ref string) (Playlist, error) {
	ps, err := t.api.Playlists(ctx)
	if err != nil {
		return Playlist{}, err
	}
	p, err := ResolvePlaylist(ps, ref)
	if err != nil {
		return Playlist{}, err
	}
	// The list may omit track ids; the single-playlist read is authoritative.
	return t.api.Playlist(ctx, p.ID)
}

func (t *tools) showPlaylist(ctx context.Context, in playlistInput) (*mcp.CallToolResult, showOutput, error) {
	p, err := t.resolve(ctx, in.Playlist)
	if err != nil {
		return toolError(err.Error()), showOutput{}, nil
	}
	lib, err := t.api.Tracks(ctx)
	if err != nil {
		return toolError(err.Error()), showOutput{}, nil
	}
	ts := lookup(trackIndex(lib), p.TrackIDs)
	link, err := t.api.PlayerLink(ctx, p.ID)
	if err != nil {
		return toolError(fmt.Sprintf("playlist '%s' (id %s) has %d tracks, but the player link failed: %v", p.Name, p.ID, len(ts), err)), showOutput{}, nil
	}
	out := showOutput{PlaylistID: p.ID, Name: p.Name, Tracks: tracksOut(ts), Minutes: Minutes(sum(ts)), PlayerLink: link}
	return textResult(fmt.Sprintf("'%s': %d tracks, %s minutes: %s. Player: %s", p.Name, len(ts), fmtMinutes(sum(ts)), joinTitles(ts), link)), out, nil
}

func (t *tools) editPlaylist(ctx context.Context, in editInput) (*mcp.CallToolResult, editOutput, error) {
	order := in.Order
	if order == "" {
		order = "keep"
	}
	if !slices.Contains([]string{"keep", "bpm_asc", "bpm_desc", "arc"}, order) {
		return toolError(fmt.Sprintf("unknown order %q: use keep|bpm_asc|bpm_desc|arc", in.Order)), editOutput{}, nil
	}
	p, err := t.resolve(ctx, in.Playlist)
	if err != nil {
		return toolError(err.Error()), editOutput{}, nil
	}
	lib, err := t.api.Tracks(ctx)
	if err != nil {
		return toolError(err.Error()), editOutput{}, nil
	}
	byID := trackIndex(lib)

	// Check every id before changing anything.
	var unknown []string
	for _, id := range in.AddTrackIDs {
		if _, ok := byID[id]; !ok {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		return toolError("no track with id: " + strings.Join(unknown, ", ") + ". Nothing was changed; use find_tracks to get ids."), editOutput{}, nil
	}

	// Removals first.
	current := slices.Clone(p.TrackIDs)
	var removeIDs, removed []string
	for _, id := range current {
		short := in.RemoveShorterThanSeconds != nil && byID[id].DurationSeconds > 0 && byID[id].DurationSeconds < *in.RemoveShorterThanSeconds
		if short || slices.Contains(in.RemoveTrackIDs, id) {
			removeIDs = append(removeIDs, id)
			removed = append(removed, lookup(byID, []string{id})[0].Title)
		}
	}
	stopped := func(step string, err error) (*mcp.CallToolResult, editOutput, error) {
		return toolError(fmt.Sprintf("editing playlist '%s' (id %s) stopped at %s: %v. Earlier steps were already applied; use show_playlist to see its state.", p.Name, p.ID, step, err)), editOutput{}, nil
	}
	for _, id := range removeIDs {
		if err := t.api.RemoveTrack(ctx, p.ID, id); err != nil {
			return stopped("removing tracks", err)
		}
	}
	current = slices.DeleteFunc(current, func(id string) bool { return slices.Contains(removeIDs, id) })

	// Then additions; a track already in the playlist is skipped.
	var addIDs, added []string
	for _, id := range in.AddTrackIDs {
		if !slices.Contains(current, id) && !slices.Contains(addIDs, id) {
			addIDs = append(addIDs, id)
			added = append(added, byID[id].Title)
		}
	}
	if len(addIDs) > 0 {
		if err := t.api.AddTracks(ctx, p.ID, addIDs); err != nil {
			return stopped("adding tracks", err)
		}
		current = append(current, addIDs...)
	}

	// Then ordering.
	ts := lookup(byID, current)
	switch order {
	case "bpm_asc":
		sortByBPM(ts, false)
	case "bpm_desc":
		sortByBPM(ts, true)
	case "arc":
		ms := arcOrder(ts)
		for i, m := range ms {
			ts[i] = m.Track
		}
	}
	if order != "keep" {
		ids := make([]string, len(ts))
		for i, tr := range ts {
			ids[i] = tr.ID
		}
		if !slices.Equal(ids, current) {
			if err := t.api.Reorder(ctx, p.ID, ids); err != nil {
				return stopped("reordering", err)
			}
		}
	}

	link, err := t.api.PlayerLink(ctx, p.ID)
	if err != nil {
		return toolError(fmt.Sprintf("playlist '%s' (id %s) was edited (removed %d, added %d), but the player link failed: %v", p.Name, p.ID, len(removed), len(added), err)), editOutput{}, nil
	}
	if removed == nil {
		removed = []string{}
	}
	if added == nil {
		added = []string{}
	}
	out := editOutput{PlaylistID: p.ID, Name: p.Name, Removed: removed, Added: added, Tracks: tracksOut(ts), Minutes: Minutes(sum(ts)), PlayerLink: link}
	return textResult(fmt.Sprintf("Edited '%s': removed %d, added %d, now %d tracks, %s minutes. Player: %s",
		p.Name, len(removed), len(added), len(ts), fmtMinutes(sum(ts)), link)), out, nil
}

func (t *tools) deletePlaylist(ctx context.Context, in playlistInput) (*mcp.CallToolResult, deleteOutput, error) {
	p, err := t.resolve(ctx, in.Playlist)
	if err != nil {
		return toolError(err.Error()), deleteOutput{}, nil
	}
	if err := t.api.DeletePlaylist(ctx, p.ID); err != nil {
		return toolError(fmt.Sprintf("could not delete playlist '%s' (id %s): %v", p.Name, p.ID, err)), deleteOutput{}, nil
	}
	return textResult(fmt.Sprintf("Deleted playlist '%s'. Its tracks are still in the library.", p.Name)), deleteOutput{Deleted: p.Name}, nil
}

// arcOrder arranges a playlist's own tracks as warm-up, peak, cooldown. The
// upbeat tracks are the peak (bpm ascending); the rest are ranked by energy
// and split in half: the more energetic half is the warm-up (bpm ascending),
// the calmer half the cooldown (bpm descending). With no upbeat tracks it is
// just warm-up then cooldown. Unlike Arc it uses every track.
func arcOrder(ts []Track) []MixTrack {
	peak := FindTracks(ts, Filter{Mood: Upbeat}, len(ts))
	rest := []Track{}
	for _, tr := range ts {
		if !Upbeat.Match(tr) {
			rest = append(rest, tr)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool {
		a, b := rest[i], rest[j]
		if a.Energy != b.Energy {
			return a.Energy > b.Energy
		}
		if a.BPM != b.BPM {
			return a.BPM > b.BPM
		}
		return a.ID < b.ID
	})
	w := (len(rest) + 1) / 2
	warm := slices.Clone(rest[:w])
	cool := slices.Clone(rest[w:])
	sortByBPM(warm, false)
	sortByBPM(cool, true)
	return append(append(tag(warm, Warmup), tag(peak, Peak)...), tag(cool, Cooldown)...)
}

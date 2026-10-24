package curated

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeAPI is an in-memory API seeded with tracks. It records every mutating call.
type fakeAPI struct {
	mu             sync.Mutex
	tracks         []Track
	playlists      []Playlist
	calls          []string
	created        []Playlist
	failPlayerLink bool
	n              int
}

func newFake(t *testing.T) *fakeAPI { return &fakeAPI{tracks: loadFixture(t)} }

func (f *fakeAPI) id(title string) string {
	for _, t := range f.tracks {
		if t.Title == title {
			return t.ID
		}
	}
	panic("no track " + title)
}

func (f *fakeAPI) ids(titles ...string) []string {
	out := []string{}
	for _, t := range titles {
		out = append(out, f.id(t))
	}
	return out
}

// seed adds a playlist directly, without recording a call.
func (f *fakeAPI) seed(name string, trackIDs []string) Playlist {
	f.n++
	p := Playlist{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", f.n), Name: name, TrackIDs: append([]string{}, trackIDs...)}
	f.playlists = append(f.playlists, p)
	return p
}

func (f *fakeAPI) find(id string) (int, error) {
	for i, p := range f.playlists {
		if p.ID == id {
			return i, nil
		}
	}
	return -1, ErrNotFound
}

func (f *fakeAPI) called(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{}
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeAPI) Tracks(context.Context) ([]Track, error) {
	return append([]Track{}, f.tracks...), nil
}

func (f *fakeAPI) Playlists(context.Context) ([]Playlist, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Playlist{}, f.playlists...), nil
}

func (f *fakeAPI) Playlist(_ context.Context, id string) (Playlist, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, err := f.find(id)
	if err != nil {
		return Playlist{}, err
	}
	return f.playlists[i], nil
}

func (f *fakeAPI) CreatePlaylist(_ context.Context, name, desc string, ids []string) (Playlist, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "CreatePlaylist "+name+" "+strings.Join(ids, ","))
	p := f.seed(name, ids)
	p.Description = desc
	f.created = append(f.created, p)
	return p, nil
}

func (f *fakeAPI) AddTracks(_ context.Context, id string, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "AddTracks "+strings.Join(ids, ","))
	i, err := f.find(id)
	if err != nil {
		return err
	}
	f.playlists[i].TrackIDs = append(f.playlists[i].TrackIDs, ids...)
	return nil
}

func (f *fakeAPI) RemoveTrack(_ context.Context, id, trackID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "RemoveTrack "+trackID)
	i, err := f.find(id)
	if err != nil {
		return err
	}
	var keep []string
	for _, x := range f.playlists[i].TrackIDs {
		if x != trackID {
			keep = append(keep, x)
		}
	}
	f.playlists[i].TrackIDs = keep
	return nil
}

func (f *fakeAPI) Reorder(_ context.Context, id string, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "Reorder "+strings.Join(ids, ","))
	i, err := f.find(id)
	if err != nil {
		return err
	}
	f.playlists[i].TrackIDs = append([]string{}, ids...)
	return nil
}

func (f *fakeAPI) DeletePlaylist(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "DeletePlaylist "+id)
	i, err := f.find(id)
	if err != nil {
		return err
	}
	f.playlists = append(f.playlists[:i], f.playlists[i+1:]...)
	return nil
}

func (f *fakeAPI) PlayerLink(_ context.Context, id string) (string, error) {
	if f.failPlayerLink {
		return "", errors.New("create player link: API returned 503")
	}
	return "https://player.example/play/" + id[:8], nil
}

func connect(t *testing.T, api API, logger *log.Logger) *mcp.ClientSession {
	t.Helper()
	srv := httptest.NewServer(Handler(api, logger))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return r
}

func resText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	var sb strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// structured decodes the structured output into a generic map.
func structured(t *testing.T, r *mcp.CallToolResult) map[string]any {
	t.Helper()
	if r.IsError {
		t.Fatalf("tool error: %s", resText(t, r))
	}
	b, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func outTitles(t *testing.T, m map[string]any, key string) []string {
	t.Helper()
	out := []string{}
	for _, x := range m[key].([]any) {
		switch v := x.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			out = append(out, v["title"].(string))
		}
	}
	return out
}

func TestSixToolsWithHints(t *testing.T) {
	cs := connect(t, newFake(t), nil)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	by := map[string]*mcp.Tool{}
	for _, tl := range list.Tools {
		names = append(names, tl.Name)
		by[tl.Name] = tl
	}
	sort.Strings(names)
	want := []string{"build_workout_mix", "delete_playlist", "edit_playlist", "find_tracks", "list_playlists", "show_playlist"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("tools = %v", names)
	}
	for _, n := range names {
		a := by[n].Annotations
		if a == nil || a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("%s: OpenWorldHint must be false: %+v", n, a)
			continue
		}
		switch n {
		case "find_tracks", "list_playlists", "show_playlist":
			if !a.ReadOnlyHint {
				t.Errorf("%s must be read-only", n)
			}
		case "build_workout_mix", "edit_playlist":
			if a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint {
				t.Errorf("%s must be a non-destructive write: %+v", n, a)
			}
		case "delete_playlist":
			if a.ReadOnlyHint || a.DestructiveHint == nil || !*a.DestructiveHint {
				t.Errorf("delete_playlist must be destructive: %+v", a)
			}
		}
	}
	for n, tl := range by {
		if !strings.Contains(tl.Description, "won't") && !strings.Contains(tl.Description, "never") && !strings.Contains(tl.Description, "Never") {
			t.Errorf("%s description says nothing about what it won't do", n)
		}
	}
	for _, n := range []string{"find_tracks", "build_workout_mix"} {
		if !strings.Contains(by[n].Description, "120+ BPM and energy 0.6+") {
			t.Errorf("%s description must quote the upbeat rule", n)
		}
	}
}

func TestNoMultiTypeSchemas(t *testing.T) {
	cs := connect(t, newFake(t), nil)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			if _, ok := x["type"].([]any); ok {
				t.Errorf("%s: multi-type %v", path, x["type"])
			}
			for k, c := range x {
				walk(path+"."+k, c)
			}
		case []any:
			for i, c := range x {
				walk(fmt.Sprintf("%s[%d]", path, i), c)
			}
		}
	}
	for _, tl := range list.Tools {
		for kind, s := range map[string]any{"input": tl.InputSchema, "output": tl.OutputSchema} {
			b, _ := json.Marshal(s)
			var v any
			_ = json.Unmarshal(b, &v)
			walk(tl.Name+" "+kind, v)
		}
		if tl.OutputSchema == nil {
			t.Errorf("%s has no output schema", tl.Name)
		}
	}
}

func TestToolsListSize(t *testing.T) {
	cs := connect(t, newFake(t), nil)
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(list.Tools)
	t.Logf("tools/list tools JSON: %d bytes, ~%d tokens (bytes/4), %d tools", len(b), len(b)/4, len(list.Tools))
}

func TestFindTracksUpbeat(t *testing.T) {
	cs := connect(t, newFake(t), nil)
	m := structured(t, call(t, cs, "find_tracks", map[string]any{"mood": "upbeat"}))
	if m["count"].(float64) != 5 {
		t.Fatalf("count = %v", m["count"])
	}
	want := []string{"Scary Intro", "Q", "Studiokolomna Track 1", "Arpmedia Track 1", "Prettyjohn1 Track 1"}
	if got := outTitles(t, m, "tracks"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestFindTracksBadInput(t *testing.T) {
	cs := connect(t, newFake(t), nil)
	r := call(t, cs, "find_tracks", map[string]any{"mood": "chill"})
	if !r.IsError || !strings.Contains(resText(t, r), "upbeat|calm|any") {
		t.Fatalf("mood: %+v %s", r.IsError, resText(t, r))
	}
	r = call(t, cs, "find_tracks", map[string]any{"limit": 101})
	if !r.IsError || !strings.Contains(resText(t, r), "limit must be between 1 and 100") {
		t.Fatalf("limit: %s", resText(t, r))
	}
	// Empty result is a plain empty array, not null.
	m := structured(t, call(t, cs, "find_tracks", map[string]any{"bpm_min": 500}))
	if m["count"].(float64) != 0 || m["tracks"] == nil {
		t.Fatalf("empty = %v", m)
	}
}

func TestBuildSteadyTodaysLibrary(t *testing.T) {
	f := newFake(t)
	cs := connect(t, f, nil)
	r := call(t, cs, "build_workout_mix", map[string]any{})
	m := structured(t, r)
	want := []string{"Scary Intro", "Q", "Studiokolomna Track 1", "Arpmedia Track 1", "Prettyjohn1 Track 1"}
	if got := outTitles(t, m, "tracks"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if m["name"] != "Upbeat Workout Mix" {
		t.Errorf("name %v", m["name"])
	}
	if link, _ := m["player_link"].(string); !strings.Contains(link, "/play/") {
		t.Errorf("player_link %q", link)
	}
	cp := f.called("CreatePlaylist")
	wantCall := "CreatePlaylist Upbeat Workout Mix " + strings.Join(f.ids(want...), ",")
	if len(cp) != 1 || cp[0] != wantCall {
		t.Fatalf("calls = %v", cp)
	}
	if len(f.called("AddTracks")) != 0 {
		t.Errorf("unexpected AddTracks")
	}
}

func TestBuildShortfallNote(t *testing.T) {
	f := newFake(t)
	cs := connect(t, f, nil)
	r := call(t, cs, "build_workout_mix", map[string]any{"minutes": 30})
	m := structured(t, r)
	want := Steady(f.tracks, 30).Note
	if want == "" || m["note"] != want || !strings.Contains(resText(t, r), want) {
		t.Fatalf("note %q, text %q", m["note"], resText(t, r))
	}
	if !strings.Contains(want, "9.1") {
		t.Errorf("note = %q", want)
	}
}

func TestBuildArc(t *testing.T) {
	f := newFake(t)
	cs := connect(t, f, nil)
	m := structured(t, call(t, cs, "build_workout_mix", map[string]any{"shape": "arc"}))
	wantT := []string{"Audiocoffee Track 1", "Guitar Chrome Etiquette", "Scary Intro", "Q", "Studiokolomna Track 1", "Arpmedia Track 1", "Prettyjohn1 Track 1", "Morning Fog", "Leberch Track 1"}
	if got := outTitles(t, m, "tracks"); !reflect.DeepEqual(got, wantT) {
		t.Fatalf("got %v", got)
	}
	secs := []string{}
	for _, x := range m["tracks"].([]any) {
		secs = append(secs, x.(map[string]any)["section"].(string))
	}
	wantS := []string{"warm-up", "warm-up", "peak", "peak", "peak", "peak", "peak", "cooldown", "cooldown"}
	if !reflect.DeepEqual(secs, wantS) {
		t.Fatalf("sections %v", secs)
	}
}

func TestBuildUniqueName(t *testing.T) {
	f := newFake(t)
	f.seed("Upbeat Workout Mix", nil)
	cs := connect(t, f, nil)
	if m := structured(t, call(t, cs, "build_workout_mix", map[string]any{})); m["name"] != "Upbeat Workout Mix 2" {
		t.Fatalf("name %v", m["name"])
	}
	if m := structured(t, call(t, cs, "build_workout_mix", map[string]any{})); m["name"] != "Upbeat Workout Mix 3" {
		t.Fatalf("name %v", m["name"])
	}
}

func TestBuildMinutesOutOfRange(t *testing.T) {
	f := newFake(t)
	cs := connect(t, f, nil)
	for _, v := range []float64{0, -5, 601} {
		r := call(t, cs, "build_workout_mix", map[string]any{"minutes": v})
		if !r.IsError || !strings.Contains(resText(t, r), "minutes must be between 1 and 600") {
			t.Errorf("minutes %v: %v %q", v, r.IsError, resText(t, r))
		}
	}
	if len(f.called("CreatePlaylist")) != 0 {
		t.Error("created a playlist for bad minutes")
	}
}

func TestBuildBadShape(t *testing.T) {
	cs := connect(t, newFake(t), nil)
	r := call(t, cs, "build_workout_mix", map[string]any{"shape": "wave"})
	if !r.IsError || !strings.Contains(resText(t, r), "steady|arc") {
		t.Fatalf("%q", resText(t, r))
	}
}

func TestBuildPartialFailureNamesPlaylist(t *testing.T) {
	f := newFake(t)
	f.failPlayerLink = true
	cs := connect(t, f, nil)
	r := call(t, cs, "build_workout_mix", map[string]any{"name": "Gym Mix"})
	txt := resText(t, r)
	if !r.IsError || !strings.Contains(txt, "Gym Mix") || !strings.Contains(txt, "was created") {
		t.Fatalf("%v %q", r.IsError, txt)
	}
	if len(f.created) != 1 || !strings.Contains(txt, f.created[0].ID) {
		t.Errorf("error should include the created playlist id: %q", txt)
	}
}

func TestResolveByIDBeatsName(t *testing.T) {
	ps := []Playlist{{ID: "aaa", Name: "bbb"}, {ID: "bbb", Name: "Other"}}
	p, err := ResolvePlaylist(ps, "bbb")
	if err != nil || p.ID != "bbb" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestResolveCaseInsensitiveName(t *testing.T) {
	ps := []Playlist{{ID: "1", Name: "Upbeat Workout Mix"}, {ID: "2", Name: "Calm"}}
	p, err := ResolvePlaylist(ps, "upbeat workout MIX")
	if err != nil || p.ID != "1" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := ResolvePlaylist(ps, "upbeat"); err == nil {
		t.Error("partial names must not match")
	}
}

func TestResolveMissListsNames(t *testing.T) {
	_, err := ResolvePlaylist([]Playlist{{ID: "1", Name: "A"}, {ID: "2", Name: "B"}}, "Gym")
	if err == nil || err.Error() != "no playlist named 'Gym'; playlists: A, B" {
		t.Fatalf("%v", err)
	}
}

func TestResolveAmbiguousListsIDs(t *testing.T) {
	_, err := ResolvePlaylist([]Playlist{{ID: "id-1", Name: "Mix"}, {ID: "id-2", Name: "mix"}}, "MIX")
	if err == nil || !strings.Contains(err.Error(), "id-1") || !strings.Contains(err.Error(), "id-2") {
		t.Fatalf("%v", err)
	}
}

func TestEditOrder(t *testing.T) {
	f := newFake(t)
	p := f.seed("Gym", f.ids("Arpmedia Track 1", "Abstract Piano", "Q"))
	cs := connect(t, f, nil)
	r := call(t, cs, "edit_playlist", map[string]any{
		"playlist":                    "gym",
		"remove_shorter_than_seconds": 120,
		"add_track_ids":               f.ids("Scary Intro"),
		"order":                       "bpm_asc",
	})
	m := structured(t, r)
	if got := outTitles(t, m, "removed"); !reflect.DeepEqual(got, []string{"Abstract Piano"}) {
		t.Errorf("removed %v", got)
	}
	if got := outTitles(t, m, "added"); !reflect.DeepEqual(got, []string{"Scary Intro"}) {
		t.Errorf("added %v", got)
	}
	if got := outTitles(t, m, "tracks"); !reflect.DeepEqual(got, []string{"Scary Intro", "Q", "Arpmedia Track 1"}) {
		t.Errorf("tracks %v", got)
	}
	var kinds []string
	for _, c := range f.calls {
		kinds = append(kinds, strings.Fields(c)[0])
	}
	if !reflect.DeepEqual(kinds, []string{"RemoveTrack", "AddTracks", "Reorder"}) {
		t.Errorf("call order %v", kinds)
	}
	got, _ := f.Playlist(context.Background(), p.ID)
	if !reflect.DeepEqual(got.TrackIDs, f.ids("Scary Intro", "Q", "Arpmedia Track 1")) {
		t.Errorf("playlist now %v", got.TrackIDs)
	}

	// Unknown track id: error naming it, nothing changed.
	before := len(f.calls)
	r = call(t, cs, "edit_playlist", map[string]any{"playlist": "Gym", "add_track_ids": []string{"nope-123"}})
	if !r.IsError || !strings.Contains(resText(t, r), "nope-123") {
		t.Errorf("unknown id: %q", resText(t, r))
	}
	if len(f.calls) != before {
		t.Errorf("mutated despite bad id")
	}
}

func TestEditOrderArc(t *testing.T) {
	f := newFake(t)
	f.seed("Mix", f.ids("Arpmedia Track 1", "Q", "Leberch Track 1", "Audiocoffee Track 1", "Morning Fog", "Alex Morgan Track 1"))
	cs := connect(t, f, nil)
	m := structured(t, call(t, cs, "edit_playlist", map[string]any{"playlist": "Mix", "order": "arc"}))
	want := []string{"Audiocoffee Track 1", "Alex Morgan Track 1", "Q", "Arpmedia Track 1", "Morning Fog", "Leberch Track 1"}
	if got := outTitles(t, m, "tracks"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestEditBadOrder(t *testing.T) {
	f := newFake(t)
	f.seed("Mix", nil)
	cs := connect(t, f, nil)
	r := call(t, cs, "edit_playlist", map[string]any{"playlist": "Mix", "order": "shuffle"})
	if !r.IsError || !strings.Contains(resText(t, r), "keep|bpm_asc|bpm_desc|arc") {
		t.Fatalf("%q", resText(t, r))
	}
}

func TestEditRequiresPlaylist(t *testing.T) {
	f := newFake(t)
	f.seed("Mix", nil)
	cs := connect(t, f, nil)
	r := call(t, cs, "edit_playlist", map[string]any{"order": "arc"})
	if !r.IsError {
		t.Fatalf("want error, got %q", resText(t, r))
	}
}

func TestListAndShow(t *testing.T) {
	f := newFake(t)
	p := f.seed("Gym", f.ids("Q", "Scary Intro"))
	cs := connect(t, f, nil)
	m := structured(t, call(t, cs, "list_playlists", map[string]any{}))
	pl := m["playlists"].([]any)[0].(map[string]any)
	if pl["name"] != "Gym" || pl["track_count"].(float64) != 2 || pl["minutes"].(float64) != 3.2 {
		t.Fatalf("%v", pl)
	}
	m = structured(t, call(t, cs, "show_playlist", map[string]any{"playlist": p.ID}))
	if got := outTitles(t, m, "tracks"); !reflect.DeepEqual(got, []string{"Q", "Scary Intro"}) || m["player_link"] == "" {
		t.Fatalf("%v", m)
	}
	r := call(t, cs, "show_playlist", map[string]any{"playlist": "Nope"})
	if !r.IsError || !strings.Contains(resText(t, r), "no playlist named 'Nope'; playlists: Gym") {
		t.Fatalf("%q", resText(t, r))
	}
}

func TestDeletePlaylist(t *testing.T) {
	f := newFake(t)
	f.seed("Keep", nil)
	del := f.seed("Gym", f.ids("Q"))
	cs := connect(t, f, nil)
	n := len(f.tracks)
	m := structured(t, call(t, cs, "delete_playlist", map[string]any{"playlist": "gym"}))
	if m["deleted"] != "Gym" {
		t.Fatalf("%v", m)
	}
	if c := f.called("DeletePlaylist"); len(c) != 1 || c[0] != "DeletePlaylist "+del.ID {
		t.Fatalf("calls %v", c)
	}
	if len(f.playlists) != 1 || f.playlists[0].Name != "Keep" || len(f.tracks) != n {
		t.Errorf("state: %+v", f.playlists)
	}
}

func TestArcOrderNoUpbeat(t *testing.T) {
	lib := loadFixture(t)
	var ts []Track
	for _, tr := range lib {
		if tr.Title == "Leberch Track 1" || tr.Title == "Morning Fog" || tr.Title == "Abstract Piano" {
			ts = append(ts, tr)
		}
	}
	got := []string{}
	for _, m := range arcOrder(ts) {
		got = append(got, string(m.Section)+":"+m.Title)
	}
	// Energy: Abstract Piano .51, Leberch .31, Morning Fog .19. Warm-up takes the top half.
	want := []string{"warm-up:Leberch Track 1", "warm-up:Abstract Piano", "cooldown:Morning Fog"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if len(arcOrder(nil)) != 0 {
		t.Error("empty")
	}
}

func TestLogsToolLinesWithoutSecrets(t *testing.T) {
	f := newFake(t)
	p := f.seed("Gym", f.ids("Q"))
	var buf bytes.Buffer
	cs := connect(t, f, log.New(&buf, "", 0))
	call(t, cs, "find_tracks", map[string]any{"mood": "upbeat"})
	call(t, cs, "show_playlist", map[string]any{"playlist": p.ID})
	call(t, cs, "edit_playlist", map[string]any{"playlist": "Gym", "add_track_ids": []string{f.id("Scary Intro")}})
	out := buf.String()
	if !strings.Contains(out, `tool "find_tracks"`) || !strings.Contains(out, `tool "show_playlist"`) {
		t.Fatalf("log = %q", out)
	}
	if !strings.Contains(out, p.ID[:8]) {
		t.Errorf("short id missing: %q", out)
	}
	for _, bad := range []string{"Bearer", "http", p.ID, f.id("Scary Intro")} {
		if strings.Contains(out, bad) {
			t.Errorf("log contains %q: %q", bad, out)
		}
	}
}

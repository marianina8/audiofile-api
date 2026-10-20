package api

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeTag(t *testing.T) {
	ok := map[string]string{"Workout": "workout", " favorite ": "favorite", "Road Trip": "road-trip", "a-1": "a-1"}
	for in, want := range ok {
		if got, err := normalizeTag(in); err != nil || got != want {
			t.Errorf("normalizeTag(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	long := "abcdefghijklmnopqrstuvwxyzabcdefg" // 33 chars
	for _, in := range []string{"", "   ", "bad tag!", "under_score", "café", long} {
		if got, err := normalizeTag(in); err == nil {
			t.Errorf("normalizeTag(%q) = %q, want error", in, got)
		}
	}
	if _, err := normalizeTag(long[:32]); err != nil {
		t.Errorf("32 chars rejected: %v", err)
	}
}

func TestTags(t *testing.T) {
	e := newEnv()
	seedLibrary(t, e)
	rec := e.do("POST", "/tracks/c/tags", `{"tags":["Workout"," favorite "]}`)
	wantStatus(t, rec, 200)
	if got := decode(t, rec)["tags"]; !reflect.DeepEqual(got, []any{"favorite", "workout"}) {
		t.Errorf("tags = %v", got)
	}
	st, _ := e.st.GetTrack(context.Background(), "c")
	if !reflect.DeepEqual(st.Tags, []string{"favorite", "workout"}) {
		t.Errorf("stored = %v", st.Tags)
	}

	// merge, dedupe, any status
	rec = e.do("POST", "/tracks/f/tags", `{"tags":["GYM","new","new"]}`)
	wantStatus(t, rec, 200)
	if got := decode(t, rec)["tags"]; !reflect.DeepEqual(got, []any{"gym", "new"}) {
		t.Errorf("merged = %v", got)
	}

	// counts come from ready tracks only: gym on a and b
	counts := func() map[string]float64 {
		m := map[string]float64{}
		for _, it := range decode(t, e.do("GET", "/tags", ""))["items"].([]any) {
			x := it.(map[string]any)
			m[x["name"].(string)] = x["track_count"].(float64)
		}
		return m
	}
	if got, want := counts(), map[string]float64{"favorite": 1, "workout": 1, "gym": 2, "loud": 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("counts = %v, want %v", got, want)
	}

	wantStatus(t, e.do("DELETE", "/tracks/a/tags/GYM", ""), 204)
	if got, want := counts(), map[string]float64{"favorite": 1, "workout": 1, "gym": 1, "loud": 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("counts after delete = %v, want %v", got, want)
	}
	wantErrCode(t, e.do("DELETE", "/tracks/a/tags/gym", ""), 404, "not_found")
	wantErrCode(t, e.do("DELETE", "/tracks/nope/tags/gym", ""), 404, "not_found")
	wantErrCode(t, e.do("DELETE", "/tracks/a/tags/bad%20tag!", ""), 400, "bad_request")
}

func TestTags_ListSorted(t *testing.T) {
	e := newEnv()
	seedLibrary(t, e)
	var names []string
	for _, it := range decode(t, e.do("GET", "/tags", ""))["items"].([]any) {
		names = append(names, it.(map[string]any)["name"].(string))
	}
	if want := []string{"gym", "loud"}; !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func TestTags_Validation(t *testing.T) {
	e := newEnv()
	seedLibrary(t, e)
	bad := map[string]string{
		"bad tag":       `{"tags":["ok","bad tag!"]}`,
		"empty array":   `{"tags":[]}`,
		"missing":       `{}`,
		"empty tag":     `{"tags":["  "]}`,
		"too long":      `{"tags":["abcdefghijklmnopqrstuvwxyzabcdefg"]}`,
		"unknown field": `{"tags":["a"],"x":1}`,
		"bad json":      `{`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			wantErrCode(t, e.do("POST", "/tracks/a/tags", body), 400, "bad_request")
		})
	}
	st, _ := e.st.GetTrack(context.Background(), "a")
	if !reflect.DeepEqual(st.Tags, []string{"gym", "loud"}) {
		t.Errorf("tags changed by rejected requests: %v", st.Tags)
	}
	wantErrCode(t, e.do("POST", "/tracks/nope/tags", `{"tags":["a"]}`), 404, "not_found")
}

func TestTags_BadRequestNamesTag(t *testing.T) {
	e := newEnv()
	seedLibrary(t, e)
	rec := e.do("POST", "/tracks/a/tags", `{"tags":["bad tag!"]}`)
	if msg := decode(t, rec)["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "bad tag!") {
		t.Errorf("message %q does not name the tag", msg)
	}
}

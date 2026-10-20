package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTrackStatusValid(t *testing.T) {
	for _, s := range []TrackStatus{StatusProcessing, StatusReady, StatusFailed} {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	if TrackStatus("done").Valid() {
		t.Error("done should be invalid")
	}
}

func TestBPMSourceValid(t *testing.T) {
	for _, s := range []BPMSource{BPMTag, BPMMeasured, BPMManual} {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	if BPMSource("guess").Valid() {
		t.Error("guess should be invalid")
	}
}

func TestAudioFromTrack(t *testing.T) {
	a := AudioFromTrack(Track{ID: "1", S3Key: "tracks/1.mp3", Status: StatusProcessing})
	if a.Status != "Initiating" {
		t.Errorf("status = %q", a.Status)
	}
	if a.Error == nil || len(a.Error) != 0 {
		t.Errorf("error = %#v, want empty non-nil", a.Error)
	}
	if a.Path != "tracks/1.mp3" || a.Id != "1" {
		t.Errorf("id/path = %q/%q", a.Id, a.Path)
	}

	f := AudioFromTrack(Track{ID: "2", Status: StatusFailed, Error: "boom"})
	if f.Status != "Complete" || len(f.Error) != 1 || f.Error[0] != "boom" {
		t.Errorf("failed = %#v", f)
	}
	if r := AudioFromTrack(Track{Status: StatusReady}); r.Status != "Complete" || len(r.Error) != 0 {
		t.Errorf("ready = %#v", r)
	}

	js, err := a.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"transcript": ""`, `"Id"`, `"Path"`, `"Metadata"`, `"Status"`, `"Error"`} {
		if !strings.Contains(js, want) {
			t.Errorf("JSON missing %s:\n%s", want, js)
		}
	}
	if !strings.Contains(js, "\n    \"Id\"") {
		t.Errorf("expected 4-space indent:\n%s", js)
	}
	var back Audio
	if err := json.Unmarshal([]byte(js), &back); err != nil {
		t.Fatal(err)
	}
}

func TestJSONTags(t *testing.T) {
	got := Track{}.JSONTags()
	if got == nil || len(got) != 0 {
		t.Errorf("JSONTags() = %#v, want empty non-nil", got)
	}
	if got := (Track{Tags: []string{"a"}}).JSONTags(); len(got) != 1 {
		t.Errorf("got %#v", got)
	}
}

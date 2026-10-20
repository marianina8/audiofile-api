package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func TestDecodeKey(t *testing.T) {
	cases := map[string]string{
		"tracks/abc.mp3":             "tracks/abc.mp3",
		"tracks/my+song.mp3":         "tracks/my song.mp3",
		"tracks/a%2Bb.mp3":           "tracks/a+b.mp3",
		"tracks/caf%C3%A9%20one.mp3": "tracks/café one.mp3",
	}
	for in, want := range cases {
		got, err := decodeKey(in)
		if err != nil || got != want {
			t.Errorf("decodeKey(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := decodeKey("tracks/bad%zz.mp3"); err == nil {
		t.Error("decodeKey accepted an invalid escape")
	}
}

func record(key string) events.S3EventRecord {
	var r events.S3EventRecord
	r.S3.Object.Key = key
	return r
}

func TestHandle(t *testing.T) {
	var got []string
	process := func(_ context.Context, key string) error {
		got = append(got, key)
		return nil
	}
	ev := events.S3Event{Records: []events.S3EventRecord{
		record("tracks/one+two.mp3"), record("tracks/bad%zz.mp3"), record("tracks/x.mp3"),
	}}
	if err := handle(context.Background(), ev, process); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if want := []string{"tracks/one two.mp3", "tracks/x.mp3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("processed %v, want %v", got, want)
	}
}

func TestHandleReturnsProcessError(t *testing.T) {
	boom := errors.New("store down")
	calls := 0
	process := func(context.Context, string) error { calls++; return boom }
	ev := events.S3Event{Records: []events.S3EventRecord{record("tracks/a.mp3"), record("tracks/b.mp3")}}
	if err := handle(context.Background(), ev, process); !errors.Is(err, boom) {
		t.Errorf("handle error = %v, want %v", err, boom)
	}
	if calls != 2 {
		t.Errorf("process called %d times, want 2 (every record is attempted)", calls)
	}
}

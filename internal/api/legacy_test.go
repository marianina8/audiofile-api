package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marianina8/audiofile-api/internal/blob"
	"github.com/marianina8/audiofile-api/internal/model"
	"github.com/marianina8/audiofile-api/internal/store"
)

func (e *env) upload(field, filename string, data []byte) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile(field, filename)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestLegacyUpload(t *testing.T) {
	e := newEnv()
	data := bytes.Repeat([]byte("a"), 1024)
	rec := e.upload("file", "x.MP3", data)
	wantStatus(t, rec, 200)
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
	if rec.Body.String() != "id1" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if got := e.bl.Objects["tracks/id1.mp3"]; !bytes.Equal(got, data) {
		t.Errorf("blob = %d bytes", len(got))
	}
	if ct := e.bl.ContentTypes["tracks/id1.mp3"]; ct != "audio/mpeg" {
		t.Errorf("blob content type = %q", ct)
	}
	tr, err := e.st.GetTrack(context.Background(), "id1")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if tr.Status != model.StatusProcessing || tr.OriginalFilename != "x.MP3" || tr.S3Key != "tracks/id1.mp3" ||
		tr.SizeBytes != 1024 || tr.ContentSHA256 != hex.EncodeToString(sum[:]) ||
		tr.CreatedAt != "2026-10-20T12:00:00Z" || tr.UpdatedAt != tr.CreatedAt {
		t.Errorf("track = %+v", tr)
	}
}

func TestLegacyUpload_TooLarge(t *testing.T) {
	e := newEnv()
	rec := e.upload("file", "x.mp3", make([]byte, 4500001))
	wantErrCode(t, rec, 413, "payload_too_large")
	if !strings.Contains(rec.Body.String(), "POST /tracks/uploads") {
		t.Errorf("message = %s", rec.Body.String())
	}
	if len(e.bl.Objects) != 0 {
		t.Error("blob stored")
	}
	// Exactly at the cap is accepted.
	wantStatus(t, e.upload("file", "x.mp3", make([]byte, 4500000)), 200)
}

func TestLegacyUpload_BodyOverCap(t *testing.T) {
	e := newEnv()
	wantErrCode(t, e.upload("file", "x.mp3", make([]byte, 5000000)), 413, "payload_too_large")
}

func TestLegacyUpload_Invalid(t *testing.T) {
	e := newEnv()
	wantErrCode(t, e.upload("other", "x.mp3", []byte("a")), 400, "bad_request")
	wantErrCode(t, e.upload("file", "x.wav", []byte("a")), 400, "bad_request")
	wantErrCode(t, e.do("POST", "/upload", "not multipart"), 400, "bad_request")
}

func TestLegacyRequest(t *testing.T) {
	e := newEnv()
	seedTrack(t, e, "a", func(tr *model.Track) { tr.Artist = "Ann"; tr.Year = 1999 })
	rec := e.do("GET", "/request?id=a", "")
	wantStatus(t, rec, 200)
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type = %q", ct)
	}
	var a model.Audio
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	if a.Id != "a" || a.Path != "tracks/a.mp3" || a.Status != "Complete" || a.Metadata.Tags.Artist != "Ann" || a.Metadata.Tags.Year != 1999 {
		t.Errorf("audio = %+v", a)
	}
	if !strings.Contains(rec.Body.String(), "\n    \"Id\": \"a\"") {
		t.Errorf("not 4-space indented: %s", rec.Body.String())
	}
	wantErrCode(t, e.do("GET", "/request", ""), 400, "bad_request")
	wantErrCode(t, e.do("GET", "/request?id=", ""), 400, "bad_request")
	wantErrCode(t, e.do("GET", "/request?id=zzz", ""), 404, "not_found")
}

func TestLegacyList(t *testing.T) {
	e := newEnv()
	rec := e.do("GET", "/list", "")
	wantStatus(t, rec, 200)
	if rec.Body.String() != "[]" {
		t.Errorf("empty body = %q", rec.Body.String())
	}
	seedTrack(t, e, "b", func(tr *model.Track) { tr.CreatedAt = "2026-01-02T00:00:00Z" })
	seedTrack(t, e, "a", func(tr *model.Track) { tr.CreatedAt = "2026-01-02T00:00:00Z" })
	seedTrack(t, e, "c", func(tr *model.Track) {
		tr.CreatedAt = "2026-01-01T00:00:00Z"
		tr.Status = model.StatusProcessing
	})
	rec = e.do("GET", "/list", "")
	wantStatus(t, rec, 200)
	var as []model.Audio
	if err := json.Unmarshal(rec.Body.Bytes(), &as); err != nil {
		t.Fatal(err)
	}
	if len(as) != 3 || as[0].Id != "c" || as[1].Id != "a" || as[2].Id != "b" || as[0].Status != "Initiating" {
		t.Errorf("list = %+v", as)
	}
	if !strings.HasPrefix(rec.Body.String(), "[\n    {\n        \"Id\"") {
		t.Errorf("not 4-space indented: %s", rec.Body.String())
	}
}

type allFailStore struct{ *store.Memory }

func (allFailStore) AllTracks(context.Context) ([]model.Track, error) {
	return nil, errors.New("secret dynamo detail")
}

func TestLegacyList_StoreFailure(t *testing.T) {
	h := New(Deps{Store: allFailStore{store.NewMemory()}, Blob: blob.NewFake(), Now: func() time.Time { return fixedNow }, NewID: func() string { return "x" }})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/list", nil))
	wantErrCode(t, rec, 500, "internal")
	if strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("leaks error text: %s", rec.Body.String())
	}
}

func TestRouteCount(t *testing.T) {
	if n := len(Routes()); n != 33 {
		t.Errorf("len(Routes()) = %d, want 33", n)
	}
}

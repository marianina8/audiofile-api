package blob

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestS3_PresignPutSignsTypeAndLength(t *testing.T) {
	client := s3.New(s3.Options{
		Region:      "us-west-2",
		Credentials: credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", ""),
	})
	b := NewS3(client, "test-bucket")
	url, headers, err := b.PresignPut(context.Background(), "uploads/a.mp3", "audio/mpeg", 1234, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(url, "X-Amz-Expires=900") {
		t.Errorf("url missing expires: %s", url)
	}
	i := strings.Index(url, "X-Amz-SignedHeaders=")
	if i < 0 {
		t.Fatalf("no signed headers: %s", url)
	}
	sh := url[i:]
	for _, h := range []string{"content-length", "content-type"} {
		if !strings.Contains(sh, h) {
			t.Errorf("signed headers missing %s: %s", h, sh)
		}
	}
	want := map[string]string{"Content-Type": "audio/mpeg", "Content-Length": "1234"}
	if !reflect.DeepEqual(headers, want) {
		t.Errorf("headers = %v, want %v", headers, want)
	}
}

func TestFake(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	if err := f.Put(ctx, "k", "audio/mpeg", []byte("abc")); err != nil {
		t.Fatal(err)
	}
	if f.ContentTypes["k"] != "audio/mpeg" || string(f.Objects["k"]) != "abc" {
		t.Fatal("not stored")
	}
	dst := filepath.Join(t.TempDir(), "out")
	if err := f.Download(ctx, "k", dst); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "abc" {
		t.Errorf("downloaded %q", got)
	}
	if err := f.Download(ctx, "nope", dst); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	u, _ := f.PresignGet(ctx, "k", 15*time.Minute)
	if u != "https://fake/k?ttl=900" {
		t.Errorf("get url %q", u)
	}
	u, h, _ := f.PresignPut(ctx, "k2", "audio/mpeg", 5, time.Hour)
	if u != "https://fake/k2?ttl=3600" || h["Content-Type"] != "audio/mpeg" || h["Content-Length"] != "5" {
		t.Errorf("put %q %v", u, h)
	}
	if err := f.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if err := f.Delete(ctx, "k"); err != nil {
		t.Errorf("delete missing: %v", err)
	}
}

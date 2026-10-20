package blob

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// Fake is an in-memory Blob for tests. Safe for concurrent use; the exported
// maps may be inspected directly only when no other goroutine is writing.
type Fake struct {
	mu           sync.Mutex
	Objects      map[string][]byte
	ContentTypes map[string]string
}

var _ Blob = (*Fake)(nil)

// NewFake returns an empty Fake.
func NewFake() *Fake {
	return &Fake{Objects: map[string][]byte{}, ContentTypes: map[string]string{}}
}

func fakeURL(key string, ttl time.Duration) string {
	return fmt.Sprintf("https://fake/%s?ttl=%d", key, int64(ttl.Seconds()))
}

func (f *Fake) PresignPut(_ context.Context, key, contentType string, size int64, ttl time.Duration) (string, map[string]string, error) {
	return fakeURL(key, ttl), map[string]string{
		"Content-Type":   contentType,
		"Content-Length": fmt.Sprint(size),
	}, nil
}

func (f *Fake) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	return fakeURL(key, ttl), nil
}

func (f *Fake) Put(_ context.Context, key, contentType string, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Objects[key] = append([]byte(nil), body...)
	f.ContentTypes[key] = contentType
	return nil
}

func (f *Fake) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Objects, key)
	delete(f.ContentTypes, key)
	return nil
}

func (f *Fake) Download(_ context.Context, key, dstPath string) error {
	f.mu.Lock()
	data, ok := f.Objects[key]
	f.mu.Unlock()
	if !ok {
		return fmt.Errorf("download %q: %w", key, ErrNotFound)
	}
	return os.WriteFile(dstPath, data, 0o600)
}

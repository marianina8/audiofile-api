// Package blob abstracts object storage for audio files.
package blob

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a key does not exist.
var ErrNotFound = errors.New("blob: not found")

// Blob is the object storage used by the API and workers.
type Blob interface {
	PresignPut(ctx context.Context, key, contentType string, size int64, ttl time.Duration) (url string, headers map[string]string, err error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	Put(ctx context.Context, key, contentType string, body []byte) error
	// PutWithMetadata is Put plus user-defined object metadata (keys are
	// lower-case; S3 returns them lower-cased).
	PutWithMetadata(ctx context.Context, key, contentType string, body []byte, metadata map[string]string) error
	// Get returns the object body and its user-defined metadata, or an error
	// wrapping ErrNotFound when the key does not exist.
	Get(ctx context.Context, key string) ([]byte, map[string]string, error)
	Delete(ctx context.Context, key string) error
	Download(ctx context.Context, key, dstPath string) error
}

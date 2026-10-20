package main

import (
	"context"
	"errors"
	"log/slog"
	"net/url"

	"github.com/aws/aws-lambda-go/events"
)

// decodeKey URL-decodes an S3 event object key (S3 encodes spaces as '+').
func decodeKey(key string) (string, error) {
	return url.QueryUnescape(key)
}

// handle processes every record in ev. A key that cannot be decoded is logged
// and skipped (retrying would not help); the error returned joins only the
// errors from process, so Lambda retries just when the store failed.
func handle(ctx context.Context, ev events.S3Event, process func(context.Context, string) error) error {
	var errs []error
	for _, r := range ev.Records {
		key, err := decodeKey(r.S3.Object.Key)
		if err != nil {
			slog.Error("processor: undecodable key", "key", r.S3.Object.Key, "err", err)
			continue
		}
		if err := process(ctx, key); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

package bucketstore

import (
	"context"
	"fmt"
	kitlog "github.com/go-kit/log"
	"github.com/thanos-io/objstore/providers/s3"
	"io"
	"net/http"
	"os"
)

type BucketStore struct {
	Bucket *s3.Bucket
}

func New(cfg s3.Config) (*BucketStore, error) {
	wrt := func(rt http.RoundTripper) http.RoundTripper {
		return rt
	}

	kitlogger := kitlog.NewJSONLogger(kitlog.NewSyncWriter(os.Stdout))
	client, err := s3.NewBucketWithConfig(kitlogger, cfg, "component", wrt)
	if err != nil {
		return nil, fmt.Errorf("cannot configure bucket store: %w", err)
	}

	return &BucketStore{Bucket: client}, nil
}

func (b *BucketStore) Close() {}

func (b *BucketStore) Ping(ctx context.Context) error {
	_, err := b.Bucket.Exists(ctx, "ping")
	return err
}

func (b *BucketStore) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	return b.Bucket.Get(ctx, name)
}

func (b *BucketStore) Upload(ctx context.Context, name string, r io.Reader) error {
	return b.Bucket.Upload(ctx, name, r)
}

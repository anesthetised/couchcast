// Package storetest gives tests a throwaway bucket on the S3 server named
// by COUCHCAST_TEST_S3_ENDPOINT (SeaweedFS in compose and CI).
package storetest

import (
	"context"
	"os"
	"strings"
	"testing"

	"uuid"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/anesthetised/couchcast/internal/config"
	"github.com/anesthetised/couchcast/internal/mediastore"
)

// Config returns the connection settings with a fresh bucket name, and
// empties and removes that bucket when the test ends if anything created
// it (the test or the code under test). The test is skipped when no
// server is configured.
func Config(t *testing.T) config.S3Config {
	t.Helper()
	endpoint := os.Getenv("COUCHCAST_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("COUCHCAST_TEST_S3_ENDPOINT not set")
	}
	cfg := config.S3Config{
		Endpoint:  endpoint,
		Bucket:    "test-" + strings.ReplaceAll(uuid.New().String(), "-", "")[:20],
		AccessKey: os.Getenv("COUCHCAST_TEST_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("COUCHCAST_TEST_S3_SECRET_KEY"),
	}
	t.Cleanup(func() {
		ctx := context.Background()
		client, err := minio.New(cfg.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, "")})
		if err != nil {
			return
		}
		if ok, err := client.BucketExists(ctx, cfg.Bucket); err != nil || !ok {
			return
		}
		if s, err := mediastore.New(cfg); err == nil {
			_ = s.DeletePrefix(ctx, "")
		}
		_ = client.RemoveBucket(ctx, cfg.Bucket)
	})
	return cfg
}

// New connects a store to a fresh bucket that is emptied and removed when
// the test ends. Objects lists what the bucket holds under a prefix.
func New(t *testing.T) (*mediastore.Store, func(prefix string) []string) {
	t.Helper()
	cfg := Config(t)
	s, err := mediastore.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, "")})
	if err != nil {
		t.Fatal(err)
	}
	list := func(prefix string) []string {
		var out []string
		for obj := range client.ListObjects(ctx, cfg.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
			if obj.Err != nil {
				t.Fatal(obj.Err)
			}
			out = append(out, obj.Key)
		}
		return out
	}
	return s, list
}

package mediastore

import (
	"context"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/anesthetised/couchcast/internal/config"
)

// Store is the S3 bucket holding packaged media under media/<id>/.
type Store struct {
	client *minio.Client
	bucket string
}

// New connects to the S3-compatible endpoint. It does not create the
// bucket: that is an infrastructure concern (see compose minio-init).
func New(cfg config.S3Config) (*Store, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("mediastore: %w", err)
	}
	return &Store{client: client, bucket: cfg.Bucket}, nil
}

// Prefix returns the object key prefix for a media id.
func Prefix(mediaID string) string { return "media/" + mediaID + "/" }

// Ping verifies the bucket is reachable.
func (s *Store) Ping(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("mediastore: %w", err)
	}
	if !ok {
		return fmt.Errorf("mediastore: bucket %q does not exist", s.bucket)
	}
	return nil
}

// UploadDir stores every file in dir under prefix and returns the total
// bytes uploaded.
func (s *Store) UploadDir(ctx context.Context, prefix, dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		key := prefix + filepath.ToSlash(rel)

		f, err := os.Open(p) //nolint:gosec // paths come from our own work dir walk
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()

		info, err := f.Stat()
		if err != nil {
			return err
		}

		_, err = s.client.PutObject(ctx, s.bucket, key, f, info.Size(), minio.PutObjectOptions{
			ContentType: ContentType(rel),
		})
		if err != nil {
			return fmt.Errorf("upload %s: %w", key, err)
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("mediastore: %w", err)
	}
	return total, nil
}

// Open returns a seekable reader for one object. The caller closes it.
func (s *Store) Open(ctx context.Context, key string) (*minio.Object, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// DeletePrefix removes every object under prefix.
func (s *Store) DeletePrefix(ctx context.Context, prefix string) error {
	objects := s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})

	toDelete := make(chan minio.ObjectInfo)
	go func() {
		defer close(toDelete)
		for obj := range objects {
			if obj.Err == nil {
				toDelete <- obj
			}
		}
	}()

	for res := range s.client.RemoveObjects(ctx, s.bucket, toDelete, minio.RemoveObjectsOptions{}) {
		if res.Err != nil {
			return fmt.Errorf("mediastore: delete %s: %w", res.ObjectName, res.Err)
		}
	}
	return nil
}

// IsThumbnail reports whether a media object name is the poster the
// ingest worker stores as thumb.<ext>; posters are served without a token.
func IsThumbnail(name string) bool {
	return strings.HasPrefix(name, "thumb.")
}

// ContentType maps DASH file names to media types. mime.TypeByExtension
// does not know .mpd and .m4s.
func ContentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".vtt":
		return "text/vtt; charset=utf-8"
	case ".mpd":
		return "application/dash+xml"
	case ".m4s":
		return "video/iso.segment"
	case ".mp4":
		return "video/mp4"
	case ".m4a":
		return "audio/mp4"
	case ".webm":
		return "video/webm"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	}
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

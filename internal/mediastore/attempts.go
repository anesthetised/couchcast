package mediastore

import (
	"context"
	"strings"
	"time"

	"uuid"

	"github.com/minio/minio-go/v7"
)

// AttemptReferences identifies packages that cannot be published anymore.
type AttemptReferences interface {
	UnusedMediaAttempts(ctx context.Context, prefixes []string) ([]string, error)
}

// PruneAttempts removes old packages left by failed or crashed attempts.
// Candidates are listed before checking their references: a new or active
// upload cannot be mistaken for an abandoned one. Legacy packages do not
// have a lease directory and are never candidates.
func (s *Store) PruneAttempts(ctx context.Context, refs AttemptReferences, olderThan time.Time) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// ponytail: scan the media bucket; track attempt prefixes separately if
	// a full listing no longer fits the periodic cleanup window.
	latest := map[string]time.Time{}
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: "media/", Recursive: true}) {
		if obj.Err != nil {
			return 0, obj.Err
		}
		parts := strings.SplitN(obj.Key, "/", 4)
		if len(parts) != 4 {
			continue
		}
		if _, err := uuid.Parse(parts[1]); err != nil {
			continue
		}
		if _, err := uuid.Parse(parts[2]); err != nil {
			continue
		}
		prefix := AttemptPrefix(parts[1], parts[2])
		if obj.LastModified.After(latest[prefix]) {
			latest[prefix] = obj.LastModified
		}
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var candidates []string
	for prefix, modified := range latest {
		if modified.Before(olderThan) {
			candidates = append(candidates, prefix)
		}
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	unused, err := refs.UnusedMediaAttempts(ctx, candidates)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, prefix := range unused {
		if err := s.DeletePrefix(ctx, prefix); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

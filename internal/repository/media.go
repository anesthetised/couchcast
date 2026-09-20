package repository

import (
	"context"
	"encoding/json"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/anesthetised/couchcast/internal/entity"
)

const mediaColumns = `id, source_key, source_url, coalesce(title, ''), coalesce(duration_ms, 0), coalesce(thumbnail_url, ''),
	status, progress, coalesce(error, ''), coalesce(size_bytes, 0), renditions, subtitles, coalesce(s3_prefix, ''),
	created_at, updated_at, last_accessed_at`

func scanMedia(row pgx.Row) (*entity.Media, error) {
	var (
		m                     entity.Media
		renditions, subtitles []byte
	)
	err := row.Scan(&m.ID, &m.SourceKey, &m.SourceURL, &m.Title, &m.DurationMs, &m.ThumbnailURL,
		&m.Status, &m.Progress, &m.Error, &m.SizeBytes, &renditions, &subtitles, &m.S3Prefix,
		&m.CreatedAt, &m.UpdatedAt, &m.LastAccessedAt)
	if err != nil {
		return nil, wrapErr(err)
	}
	if len(renditions) > 0 {
		if err := json.Unmarshal(renditions, &m.Renditions); err != nil {
			return nil, err
		}
	}
	if len(subtitles) > 0 {
		if err := json.Unmarshal(subtitles, &m.Subtitles); err != nil {
			return nil, err
		}
	}
	return &m, nil
}

// CreateMedia inserts a queued media row for the source key, or returns the
// existing one. created reports whether a new row was inserted, so the
// caller knows whether to enqueue an ingest job.
func (r *Repo) CreateMedia(ctx context.Context, q Querier, sourceKey, sourceURL string) (m *entity.Media, created bool, err error) {
	const insert = `
		INSERT INTO media (source_key, source_url) VALUES ($1, $2)
		ON CONFLICT (source_key) DO NOTHING
		RETURNING ` + mediaColumns

	m, err = scanMedia(q.QueryRow(ctx, insert, sourceKey, sourceURL))
	if err == nil {
		return m, true, nil
	}
	if err != ErrNotFound { //nolint:errorlint // sentinel returned directly by scanMedia
		return nil, false, err
	}

	const sel = `SELECT ` + mediaColumns + ` FROM media WHERE source_key = $1`
	m, err = scanMedia(q.QueryRow(ctx, sel, sourceKey))
	return m, false, err
}

// GetMediaByKey returns the media row for a source key or ErrNotFound.
func (r *Repo) GetMediaByKey(ctx context.Context, sourceKey string) (*entity.Media, error) {
	const q = `SELECT ` + mediaColumns + ` FROM media WHERE source_key = $1`
	return scanMedia(r.pool.QueryRow(ctx, q, sourceKey))
}

// GetMedia returns a media row or ErrNotFound.
func (r *Repo) GetMedia(ctx context.Context, id uuid.UUID) (*entity.Media, error) {
	const q = `SELECT ` + mediaColumns + ` FROM media WHERE id = $1`
	return scanMedia(r.pool.QueryRow(ctx, q, id))
}

// GetMediaBatch returns the given media rows keyed by id.
func (r *Repo) GetMediaBatch(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*entity.Media, error) {
	const q = `SELECT ` + mediaColumns + ` FROM media WHERE id = ANY($1)`
	rows, err := r.pool.Query(ctx, q, ids)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	out := make(map[uuid.UUID]*entity.Media, len(ids))
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		out[m.ID] = m
	}
	return out, rows.Err()
}

// SetMediaStatus moves the item to a new step and resets step progress.
func (r *Repo) SetMediaStatus(ctx context.Context, id uuid.UUID, status entity.MediaStatus) error {
	const q = `UPDATE media SET status = $2, progress = 0, error = NULL, updated_at = now() WHERE id = $1`
	return r.exec(ctx, q, id, status)
}

// SetMediaProgress updates progress within the current step.
func (r *Repo) SetMediaProgress(ctx context.Context, id uuid.UUID, progress float32) error {
	const q = `UPDATE media SET progress = $2, updated_at = now() WHERE id = $1`
	return r.exec(ctx, q, id, progress)
}

// SetMediaProbed stores the metadata learned from the source.
func (r *Repo) SetMediaProbed(ctx context.Context, id uuid.UUID, title string, durationMs int64, thumbnailURL string) error {
	const q = `UPDATE media SET title = $2, duration_ms = $3, thumbnail_url = $4, updated_at = now() WHERE id = $1`
	return r.exec(ctx, q, id, title, durationMs, thumbnailURL)
}

// SetMediaSubtitles records the text tracks packaged with the media.
func (r *Repo) SetMediaSubtitles(ctx context.Context, id uuid.UUID, subtitles []entity.Subtitle) error {
	if subtitles == nil {
		subtitles = []entity.Subtitle{}
	}
	b, err := json.Marshal(subtitles)
	if err != nil {
		return err
	}
	const q = `UPDATE media SET subtitles = $2, updated_at = now() WHERE id = $1`
	return r.exec(ctx, q, id, b)
}

// SetMediaReady marks the item playable.
func (r *Repo) SetMediaReady(ctx context.Context, id uuid.UUID, renditions []entity.Rendition, sizeBytes int64, s3Prefix string) error {
	b, err := json.Marshal(renditions)
	if err != nil {
		return err
	}
	const q = `
		UPDATE media SET status = 'ready', progress = 1, error = NULL, renditions = $2, size_bytes = $3,
		       s3_prefix = $4, updated_at = now(), last_accessed_at = now()
		WHERE id = $1
	`
	return r.exec(ctx, q, id, b, sizeBytes, s3Prefix)
}

// SetMediaFailed records the failure reason.
func (r *Repo) SetMediaFailed(ctx context.Context, id uuid.UUID, reason string) error {
	const q = `UPDATE media SET status = 'failed', error = $2, updated_at = now() WHERE id = $1`
	return r.exec(ctx, q, id, reason)
}

// TouchMediaAccess bumps last_accessed_at; the eviction janitor reads it.
func (r *Repo) TouchMediaAccess(ctx context.Context, id uuid.UUID, at time.Time) error {
	const q = `UPDATE media SET last_accessed_at = $2 WHERE id = $1 AND last_accessed_at < $2`
	_, err := r.pool.Exec(ctx, q, id, at)
	return wrapErr(err)
}

// DeleteMedia removes the row; queue items referencing it cascade.
func (r *Repo) DeleteMedia(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM media WHERE id = $1`
	return r.exec(ctx, q, id)
}

// IsSourceBlocked reports whether the source key is on the blocklist.
func (r *Repo) IsSourceBlocked(ctx context.Context, sourceKey string) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM media_blocklist WHERE source_key = $1)`
	var blocked bool
	err := r.pool.QueryRow(ctx, q, sourceKey).Scan(&blocked)
	return blocked, wrapErr(err)
}

// BlockSource adds a source key to the blocklist.
func (r *Repo) BlockSource(ctx context.Context, sourceKey, reason string, createdBy *uuid.UUID) error {
	const q = `
		INSERT INTO media_blocklist (source_key, reason, created_by) VALUES ($1, $2, $3)
		ON CONFLICT (source_key) DO UPDATE SET reason = EXCLUDED.reason
	`
	_, err := r.pool.Exec(ctx, q, sourceKey, reason, createdBy)
	return wrapErr(err)
}

// SumReadyMediaBytes returns the total size of packaged media.
func (r *Repo) SumReadyMediaBytes(ctx context.Context) (int64, error) {
	const q = `SELECT coalesce(sum(size_bytes), 0) FROM media WHERE status = 'ready'`
	var n int64
	err := r.pool.QueryRow(ctx, q).Scan(&n)
	return n, wrapErr(err)
}

// ListEvictableMedia returns ready media that no queue references,
// least recently accessed first.
func (r *Repo) ListEvictableMedia(ctx context.Context, limit int) ([]entity.Media, error) {
	const q = `
		SELECT ` + mediaColumns + `
		FROM media
		WHERE status = 'ready' AND NOT EXISTS (SELECT 1 FROM queue_items qi WHERE qi.media_id = media.id)
		ORDER BY last_accessed_at
		LIMIT $1
	`
	rows, err := r.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []entity.Media
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

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
	status, progress, coalesce(speed_bps, 0), coalesce(eta_ms, 0), coalesce(error, ''), coalesce(size_bytes, 0), renditions, subtitles, chapters, storyboard, coalesce(s3_prefix, ''),
	created_at, updated_at, last_accessed_at`

// mediaRow receives one mediaColumns row; the JSON columns are decoded by
// media(). Queries that select extra columns append their own targets.
type mediaRow struct {
	m                                           entity.Media
	renditions, subtitles, chapters, storyboard []byte
}

func (mr *mediaRow) targets() []any {
	m := &mr.m
	return []any{&m.ID, &m.SourceKey, &m.SourceURL, &m.Title, &m.DurationMs, &m.ThumbnailURL,
		&m.Status, &m.Progress, &m.SpeedBps, &m.EtaMs, &m.Error, &m.SizeBytes, &mr.renditions, &mr.subtitles, &mr.chapters, &mr.storyboard, &m.S3Prefix,
		&m.CreatedAt, &m.UpdatedAt, &m.LastAccessedAt}
}

func (mr *mediaRow) media() (*entity.Media, error) {
	m := mr.m
	for _, f := range []struct {
		raw []byte
		dst any
	}{{mr.renditions, &m.Renditions}, {mr.subtitles, &m.Subtitles}, {mr.chapters, &m.Chapters}, {mr.storyboard, &m.Storyboard}} {
		if len(f.raw) == 0 {
			continue
		}
		if err := json.Unmarshal(f.raw, f.dst); err != nil {
			return nil, err
		}
	}
	return &m, nil
}

func scanMedia(row pgx.Row) (*entity.Media, error) {
	var mr mediaRow
	if err := row.Scan(mr.targets()...); err != nil {
		return nil, wrapErr(err)
	}
	return mr.media()
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

// MediaPrefix returns the published package, or an empty string when none
// exists. Segment requests need only this column, not the media metadata.
func (r *Repo) MediaPrefix(ctx context.Context, id uuid.UUID) (string, error) {
	const q = `SELECT coalesce(s3_prefix, '') FROM media WHERE id = $1`
	var prefix string
	err := r.pool.QueryRow(ctx, q, id).Scan(&prefix)
	if err == pgx.ErrNoRows { //nolint:errorlint // pgx returns this sentinel directly
		return "", nil
	}
	return prefix, wrapErr(err)
}

// UnusedMediaAttempts returns prefixes that no published media or running
// lease references. Lease tokens are never reused: an unreferenced attempt
// cannot become publishable after this check, even if it resumes uploading.
func (r *Repo) UnusedMediaAttempts(ctx context.Context, prefixes []string) ([]string, error) {
	const q = `
		SELECT prefix FROM unnest($1::text[]) AS candidate(prefix)
		WHERE NOT EXISTS (SELECT 1 FROM media WHERE s3_prefix = prefix)
		  AND NOT EXISTS (SELECT 1 FROM jobs WHERE status = 'running' AND lease = split_part(prefix, '/', 3)::uuid)
	`
	rows, err := r.pool.Query(ctx, q, prefixes)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()
	return pgx.CollectRows(rows, pgx.RowTo[string])
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
	const q = `UPDATE media SET status = $2, progress = 0, speed_bps = NULL, eta_ms = NULL, error = NULL, updated_at = now() WHERE id = $1`
	return r.exec(ctx, q, id, status)
}

// SetMediaProgress updates progress within the current step; zero speed
// or ETA means unknown.
func (r *Repo) SetMediaProgress(ctx context.Context, id uuid.UUID, progress float32, speedBps, etaMs int64) error {
	const q = `UPDATE media SET progress = $2, speed_bps = nullif($3, 0), eta_ms = nullif($4, 0), updated_at = now() WHERE id = $1`
	return r.exec(ctx, q, id, progress, speedBps, etaMs)
}

// SetMediaProbed stores the metadata learned from the source.
func (r *Repo) SetMediaProbed(ctx context.Context, id uuid.UUID, title string, durationMs int64, thumbnailURL string, chapters []entity.Chapter) error {
	if chapters == nil {
		chapters = []entity.Chapter{}
	}
	b, err := json.Marshal(chapters)
	if err != nil {
		return err
	}
	const q = `UPDATE media SET title = $2, duration_ms = $3, thumbnail_url = $4, chapters = $5, updated_at = now() WHERE id = $1`
	return r.exec(ctx, q, id, title, durationMs, thumbnailURL, b)
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
	return r.FailMedia(ctx, r.pool, id, reason)
}

// FailMedia is SetMediaFailed on q (a transaction, for fenced writes).
func (r *Repo) FailMedia(ctx context.Context, q Querier, id uuid.UUID, reason string) error {
	const fail = `UPDATE media SET status = 'failed', error = $2, updated_at = now() WHERE id = $1`
	return execOn(ctx, q, fail, id, reason)
}

// PublishedMedia is what a finished ingest records about its output.
type PublishedMedia struct {
	Renditions   []entity.Rendition
	SizeBytes    int64
	S3Prefix     string
	Subtitles    []entity.Subtitle
	Storyboard   *entity.Storyboard // nil: none
	ThumbnailURL string             // our copy of the poster; empty keeps the source's
}

// PublishMedia records a finished ingest and marks the media ready in one
// statement on q, so the result appears at once and, run inside
// jobs.Queue.Fenced, only for the attempt that owns the job.
func (r *Repo) PublishMedia(ctx context.Context, q Querier, id uuid.UUID, p PublishedMedia) error {
	renditions, err := json.Marshal(p.Renditions)
	if err != nil {
		return err
	}
	if p.Subtitles == nil {
		p.Subtitles = []entity.Subtitle{}
	}
	subtitles, err := json.Marshal(p.Subtitles)
	if err != nil {
		return err
	}
	var storyboard []byte
	if p.Storyboard != nil {
		if storyboard, err = json.Marshal(p.Storyboard); err != nil {
			return err
		}
	}
	const publish = `
		UPDATE media SET status = 'ready', progress = 1, error = NULL, renditions = $2, size_bytes = $3,
		       s3_prefix = $4, subtitles = $5, storyboard = $6, thumbnail_url = coalesce(nullif($7, ''), thumbnail_url),
		       updated_at = now(), last_accessed_at = now()
		WHERE id = $1
	`
	return execOn(ctx, q, publish, id, renditions, p.SizeBytes, p.S3Prefix, subtitles, storyboard, p.ThumbnailURL)
}

// execOn runs a single-row write on q; ErrNotFound when nothing matched.
func execOn(ctx context.Context, q Querier, sql string, args ...any) error {
	tag, err := q.Exec(ctx, sql, args...)
	if err != nil {
		return wrapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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

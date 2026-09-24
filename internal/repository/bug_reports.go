package repository

import (
	"context"
	"encoding/json"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/anesthetised/couchcast/internal/entity"
)

const bugReportColumns = `b.id, b.user_id, coalesce(u.username, ''), b.room_id, coalesce(r.slug, ''), b.media_id, coalesce(m.title, ''),
	b.category, b.description, b.client, b.server, b.frame IS NOT NULL, b.created_at, b.resolved_at, b.resolved_by, b.note`

const bugReportFrom = `
	FROM bug_reports b
	LEFT JOIN users u ON u.id = b.user_id
	LEFT JOIN rooms r ON r.id = b.room_id
	LEFT JOIN media m ON m.id = b.media_id
`

func scanBugReport(row pgx.Row) (*entity.BugReport, error) {
	var (
		b              entity.BugReport
		client, server []byte
	)
	if err := row.Scan(&b.ID, &b.UserID, &b.Username, &b.RoomID, &b.RoomSlug, &b.MediaID, &b.MediaTitle,
		&b.Category, &b.Description, &client, &server, &b.HasFrame, &b.CreatedAt, &b.ResolvedAt, &b.ResolvedBy, &b.Note); err != nil {
		return nil, wrapErr(err)
	}
	b.Client, b.Server = json.RawMessage(client), json.RawMessage(server)
	return &b, nil
}

// CreateBugReport stores a report; Client and Server must be JSON
// objects, Frame a JPEG or nil.
func (r *Repo) CreateBugReport(ctx context.Context, b *entity.BugReport, frame []byte) (*entity.BugReport, error) {
	const q = `
		INSERT INTO bug_reports (user_id, room_id, media_id, category, description, client, server, frame)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id
	`
	var id uuid.UUID
	if err := r.pool.QueryRow(ctx, q, b.UserID, b.RoomID, b.MediaID, b.Category, b.Description, []byte(b.Client), []byte(b.Server), frame).Scan(&id); err != nil {
		return nil, wrapErr(err)
	}
	return r.GetBugReport(ctx, id)
}

// GetBugReport returns one report or ErrNotFound.
func (r *Repo) GetBugReport(ctx context.Context, id uuid.UUID) (*entity.BugReport, error) {
	return scanBugReport(r.pool.QueryRow(ctx, `SELECT `+bugReportColumns+bugReportFrom+` WHERE b.id = $1`, id))
}

// BugReportQuery pages the reports newest first; Before is the created_at
// of the last entry of the previous page (zero for the first page).
type BugReportQuery struct {
	Resolved bool
	Before   time.Time
	Limit    int
}

// ListBugReports returns open or resolved reports, newest first. The
// JSON halves are included: the list shows a device summary.
func (r *Repo) ListBugReports(ctx context.Context, q BugReportQuery) ([]entity.BugReport, error) {
	const sql = `SELECT ` + bugReportColumns + bugReportFrom + `
		WHERE (b.resolved_at IS NOT NULL) = $1 AND ($2::timestamptz IS NULL OR b.created_at < $2)
		ORDER BY b.created_at DESC
		LIMIT $3`
	var before *time.Time
	if !q.Before.IsZero() {
		before = &q.Before
	}
	rows, err := r.pool.Query(ctx, sql, q.Resolved, before, q.Limit)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()
	var out []entity.BugReport
	for rows.Next() {
		b, err := scanBugReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// GetBugReportFrame returns the attached JPEG or ErrNotFound.
func (r *Repo) GetBugReportFrame(ctx context.Context, id uuid.UUID) ([]byte, error) {
	var frame []byte
	err := r.pool.QueryRow(ctx, `SELECT frame FROM bug_reports WHERE id = $1 AND frame IS NOT NULL`, id).Scan(&frame)
	if err != nil {
		return nil, wrapErr(err)
	}
	return frame, nil
}

// ResolveBugReport marks a report handled; ErrNotFound when it does not
// exist or is already resolved.
func (r *Repo) ResolveBugReport(ctx context.Context, id, by uuid.UUID, note string) error {
	const q = `UPDATE bug_reports SET resolved_at = now(), resolved_by = $2, note = $3 WHERE id = $1 AND resolved_at IS NULL`
	return r.exec(ctx, q, id, by, note)
}

// PurgeBugReports deletes reports created before the cutoff.
func (r *Repo) PurgeBugReports(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM bug_reports WHERE created_at < $1`, before)
	if err != nil {
		return 0, wrapErr(err)
	}
	return tag.RowsAffected(), nil
}

// IngestJobState is the latest ingest job of a media item.
type IngestJobState struct {
	Status    string    `json:"status"`
	Attempts  int       `json:"attempts"`
	LastError string    `json:"lastError,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// LatestIngestJob returns the newest ingest job for the media or
// ErrNotFound.
func (r *Repo) LatestIngestJob(ctx context.Context, mediaID uuid.UUID) (*IngestJobState, error) {
	const q = `
		SELECT status, attempts, coalesce(last_error, ''), updated_at
		FROM jobs WHERE payload->>'mediaId' = $1::text
		ORDER BY created_at DESC LIMIT 1
	`
	var j IngestJobState
	if err := r.pool.QueryRow(ctx, q, mediaID).Scan(&j.Status, &j.Attempts, &j.LastError, &j.UpdatedAt); err != nil {
		return nil, wrapErr(err)
	}
	return &j, nil
}

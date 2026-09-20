package repository

import (
	"context"
	"encoding/json"
	"errors"

	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/anesthetised/couchcast/internal/entity"
)

// CreateReport files a report; one per user per media (ErrConflict).
// roomID and addedBy record where it was seen and who queued it there.
func (r *Repo) CreateReport(ctx context.Context, mediaID, reporterID uuid.UUID, roomID, addedBy *uuid.UUID, reason entity.ReportReason, comment string) error {
	const q = `
		INSERT INTO media_reports (media_id, reporter_id, room_id, added_by, reason, comment)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := r.pool.Exec(ctx, q, mediaID, reporterID, roomID, addedBy, reason, comment)
	return wrapErr(err)
}

// QueueAdderInRoom returns who added the media to the room's queue, if it
// is queued there.
func (r *Repo) QueueAdderInRoom(ctx context.Context, roomID, mediaID uuid.UUID) (*uuid.UUID, bool, error) {
	const q = `SELECT added_by FROM queue_items WHERE room_id = $1 AND media_id = $2 ORDER BY created_at LIMIT 1`
	var addedBy *uuid.UUID
	err := r.pool.QueryRow(ctx, q, roomID, mediaID).Scan(&addedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, wrapErr(err)
	}
	return addedBy, true, nil
}

// ListPlacements returns the rooms currently queuing the media.
func (r *Repo) ListPlacements(ctx context.Context, mediaID uuid.UUID) ([]entity.Placement, error) {
	const q = `
		SELECT DISTINCT ON (rm.id) rm.id, rm.slug, rm.name, coalesce(u.username, '')
		FROM queue_items qi
		JOIN rooms rm ON rm.id = qi.room_id
		LEFT JOIN users u ON u.id = qi.added_by
		WHERE qi.media_id = $1
		ORDER BY rm.id, qi.created_at
	`
	rows, err := r.pool.Query(ctx, q, mediaID)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []entity.Placement
	for rows.Next() {
		var p entity.Placement
		if err := rows.Scan(&p.RoomID, &p.RoomSlug, &p.RoomName, &p.AddedBy); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListReportedMedia returns media with open reports, most reported first.
func (r *Repo) ListReportedMedia(ctx context.Context, limit int) ([]entity.ReportedMedia, error) {
	const q = `
		SELECT ` + mediaColumns + `, (SELECT count(*) FROM media_reports mr WHERE mr.media_id = media.id AND mr.resolved_at IS NULL) AS open
		FROM media
		WHERE EXISTS (SELECT 1 FROM media_reports mr WHERE mr.media_id = media.id AND mr.resolved_at IS NULL)
		ORDER BY open DESC, created_at
		LIMIT $1
	`
	rows, err := r.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []entity.ReportedMedia
	for rows.Next() {
		var (
			m     entity.Media
			rend  []byte
			count int
		)
		if err := rows.Scan(&m.ID, &m.SourceKey, &m.SourceURL, &m.Title, &m.DurationMs, &m.ThumbnailURL,
			&m.Status, &m.Progress, &m.Error, &m.SizeBytes, &rend, &m.S3Prefix,
			&m.CreatedAt, &m.UpdatedAt, &m.LastAccessedAt, &count); err != nil {
			return nil, err
		}
		if len(rend) > 0 {
			_ = json.Unmarshal(rend, &m.Renditions)
		}
		out = append(out, entity.ReportedMedia{Media: m, Count: count})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		reports, err := r.listOpenReports(ctx, out[i].Media.ID)
		if err != nil {
			return nil, err
		}
		out[i].Reports = reports
		placements, err := r.ListPlacements(ctx, out[i].Media.ID)
		if err != nil {
			return nil, err
		}
		out[i].Placements = placements
	}
	return out, nil
}

func (r *Repo) listOpenReports(ctx context.Context, mediaID uuid.UUID) ([]entity.MediaReport, error) {
	const q = `
		SELECT mr.id, mr.media_id, mr.reporter_id, u.username, mr.room_id, coalesce(rm.slug, ''), coalesce(rm.name, ''),
		       coalesce(a.username, ''), mr.reason, coalesce(mr.comment, ''), mr.created_at
		FROM media_reports mr
		JOIN users u ON u.id = mr.reporter_id
		LEFT JOIN rooms rm ON rm.id = mr.room_id
		LEFT JOIN users a ON a.id = mr.added_by
		WHERE mr.media_id = $1 AND mr.resolved_at IS NULL
		ORDER BY mr.created_at
	`
	rows, err := r.pool.Query(ctx, q, mediaID)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []entity.MediaReport
	for rows.Next() {
		var rep entity.MediaReport
		if err := rows.Scan(&rep.ID, &rep.MediaID, &rep.ReporterID, &rep.Reporter, &rep.RoomID, &rep.RoomSlug, &rep.RoomName,
			&rep.AddedBy, &rep.Reason, &rep.Comment, &rep.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rep)
	}
	return out, rows.Err()
}

// ResolveReports closes every open report on a media item.
func (r *Repo) ResolveReports(ctx context.Context, mediaID, resolvedBy uuid.UUID) (int64, error) {
	const q = `UPDATE media_reports SET resolved_at = now(), resolved_by = $2 WHERE media_id = $1 AND resolved_at IS NULL`
	tag, err := r.pool.Exec(ctx, q, mediaID, resolvedBy)
	if err != nil {
		return 0, wrapErr(err)
	}
	return tag.RowsAffected(), nil
}

// ListBlocklist returns blocked sources, newest first.
func (r *Repo) ListBlocklist(ctx context.Context) ([]entity.BlocklistEntry, error) {
	const q = `
		SELECT b.source_key, coalesce(b.reason, ''), coalesce(u.username, ''), b.created_at
		FROM media_blocklist b LEFT JOIN users u ON u.id = b.created_by
		ORDER BY b.created_at DESC
	`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []entity.BlocklistEntry
	for rows.Next() {
		var e entity.BlocklistEntry
		if err := rows.Scan(&e.SourceKey, &e.Reason, &e.CreatedBy, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UnblockSource removes a blocklist entry.
func (r *Repo) UnblockSource(ctx context.Context, sourceKey string) error {
	const q = `DELETE FROM media_blocklist WHERE source_key = $1`
	return r.exec(ctx, q, sourceKey)
}

// Stats gathers the dashboard counters in one round trip per table.
func (r *Repo) Stats(ctx context.Context) (*entity.Stats, error) {
	s := &entity.Stats{MediaByStatus: map[string]int{}}

	const users = `SELECT count(*), count(*) FILTER (WHERE banned_at IS NOT NULL) FROM users`
	if err := r.pool.QueryRow(ctx, users).Scan(&s.Users, &s.BannedUsers); err != nil {
		return nil, wrapErr(err)
	}
	const rooms = `SELECT count(*), count(*) FILTER (WHERE visibility = 'private') FROM rooms`
	if err := r.pool.QueryRow(ctx, rooms).Scan(&s.Rooms, &s.PrivateRooms); err != nil {
		return nil, wrapErr(err)
	}
	const jobs = `
		SELECT count(*) FILTER (WHERE status = 'pending'), count(*) FILTER (WHERE status = 'running'),
		       count(*) FILTER (WHERE status = 'failed') FROM jobs
	`
	if err := r.pool.QueryRow(ctx, jobs).Scan(&s.PendingJobs, &s.RunningJobs, &s.FailedJobs); err != nil {
		return nil, wrapErr(err)
	}
	const reports = `SELECT count(*) FROM media_reports WHERE resolved_at IS NULL`
	if err := r.pool.QueryRow(ctx, reports).Scan(&s.OpenReports); err != nil {
		return nil, wrapErr(err)
	}
	const bytes = `SELECT coalesce(sum(size_bytes), 0) FROM media WHERE status = 'ready'`
	if err := r.pool.QueryRow(ctx, bytes).Scan(&s.MediaBytes); err != nil {
		return nil, wrapErr(err)
	}

	const media = `SELECT status, count(*) FROM media GROUP BY status`
	rows, err := r.pool.Query(ctx, media)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			status string
			n      int
		)
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		s.MediaByStatus[status] = n
	}
	return s, rows.Err()
}

// ListUsers returns users matching the query (prefix on username), with
// the number of rooms they belong to.
func (r *Repo) ListUsers(ctx context.Context, query string, limit int) ([]entity.AdminUser, error) {
	const q = `
		SELECT ` + userColumns + `, (SELECT count(*) FROM room_members m WHERE m.user_id = users.id)
		FROM users
		WHERE $1 = '' OR lower(username) LIKE lower($1) || '%'
		ORDER BY created_at DESC
		LIMIT $2
	`
	rows, err := r.pool.Query(ctx, q, query, limit)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []entity.AdminUser
	for rows.Next() {
		var au entity.AdminUser
		u := &au.User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.BannedAt, &u.BannedReason, &u.BannedBy, &u.CreatedAt, &u.AvatarColor, &au.RoomCount); err != nil {
			return nil, err
		}
		out = append(out, au)
	}
	return out, rows.Err()
}

// ListRooms returns rooms matching the query (prefix on slug or name).
func (r *Repo) ListRooms(ctx context.Context, query string, limit int) ([]entity.Room, error) {
	const q = `
		SELECT ` + roomColumns + ` FROM rooms
		WHERE $1 = '' OR slug LIKE $1 || '%' OR name ILIKE $1 || '%'
		ORDER BY created_at DESC
		LIMIT $2
	`
	rows, err := r.pool.Query(ctx, q, query, limit)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []entity.Room
	for rows.Next() {
		rm, err := scanRoom(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rm)
	}
	return out, rows.Err()
}

// ListAudit returns the newest audit entries, optionally filtered.
func (r *Repo) ListAudit(ctx context.Context, roomID *uuid.UUID, limit int) ([]entity.AuditEntry, error) {
	const q = `
		SELECT a.id, a.actor_id, a.action, a.target_type, a.target_id, a.room_id, a.meta, a.created_at
		FROM audit_log a
		WHERE $1::uuid IS NULL OR a.room_id = $1
		ORDER BY a.id DESC
		LIMIT $2
	`
	rows, err := r.pool.Query(ctx, q, roomID, limit)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []entity.AuditEntry
	for rows.Next() {
		var (
			e    entity.AuditEntry
			meta []byte
		)
		if err := rows.Scan(&e.ID, &e.ActorID, &e.Action, &e.TargetType, &e.TargetID, &e.RoomID, &meta, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(meta, &e.Meta)
		out = append(out, e)
	}
	return out, rows.Err()
}

// UsernamesByID resolves ids to usernames for display.
func (r *Repo) UsernamesByID(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	const q = `SELECT id, username FROM users WHERE id = ANY($1)`
	rows, err := r.pool.Query(ctx, q, ids)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	out := make(map[uuid.UUID]string, len(ids))
	for rows.Next() {
		var (
			id   uuid.UUID
			name string
		)
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

package repository

import (
	"context"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/anesthetised/couchcast/internal/entity"
)

// quoteColumns joins the replied-to message; the quote is dropped when
// that message was deleted or belongs to another room.
const quoteColumns = `q.id, coalesce(qu.username, ''), q.body`

// CreateMessage stores a chat line and returns it with the username.
// replyTo must be a visible message of the same room; ErrNotFound when
// it is not.
func (r *Repo) CreateMessage(ctx context.Context, roomID, userID uuid.UUID, body string, replyTo *int64) (*entity.Message, error) {
	const q = `
		WITH ins AS (
			INSERT INTO messages (room_id, user_id, body, reply_to)
			SELECT $1, $2, $3, $4
			WHERE $4::bigint IS NULL OR EXISTS (SELECT 1 FROM messages p WHERE p.id = $4 AND p.room_id = $1 AND p.deleted_at IS NULL)
			RETURNING id, room_id, user_id, body, reply_to, created_at
		)
		SELECT ins.id, ins.room_id, ins.user_id, u.username, u.avatar_color, ins.body, ins.created_at, ` + quoteColumns + `
		FROM ins
		JOIN users u ON u.id = ins.user_id
		LEFT JOIN messages q ON q.id = ins.reply_to
		LEFT JOIN users qu ON qu.id = q.user_id
	`
	return scanMessage(r.pool.QueryRow(ctx, q, roomID, userID, body, replyTo))
}

// scanMessage reads one inserted message with its author and quote.
func scanMessage(row pgx.Row) (*entity.Message, error) {
	var (
		m     entity.Message
		qID   *int64
		qUser *string
		qBody *string
	)
	if err := row.Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Color, &m.Body, &m.CreatedAt, &qID, &qUser, &qBody); err != nil {
		return nil, wrapErr(err)
	}
	if qID != nil {
		m.ReplyTo = &entity.Quote{ID: *qID, Username: *qUser, Body: *qBody}
	}
	return &m, nil
}

// CreateSystemMessage stores an authorless room log line.
func (r *Repo) CreateSystemMessage(ctx context.Context, roomID uuid.UUID, body string) (*entity.Message, error) {
	const q = `
		INSERT INTO messages (room_id, user_id, body, system) VALUES ($1, NULL, $2, true)
		RETURNING id, room_id, body, created_at
	`
	var m entity.Message
	if err := r.pool.QueryRow(ctx, q, roomID, body).Scan(&m.ID, &m.RoomID, &m.Body, &m.CreatedAt); err != nil {
		return nil, wrapErr(err)
	}
	m.System = true
	return &m, nil
}

// messageSelect lists visible messages with author and quote.
const messageSelect = `
	SELECT m.id, m.room_id, m.user_id, coalesce(u.username, '') AS username, coalesce(u.avatar_color, '') AS color, m.body, m.system, m.created_at,
	       q.id AS reply_id, coalesce(qu.username, '') AS reply_username, q.body AS reply_body
	FROM messages m
	LEFT JOIN users u ON u.id = m.user_id
	LEFT JOIN messages q ON q.id = m.reply_to AND q.deleted_at IS NULL
	LEFT JOIN users qu ON qu.id = q.user_id
`

func scanMessages(rows pgx.Rows) ([]entity.Message, error) {
	defer rows.Close()
	var out []entity.Message
	for rows.Next() {
		var (
			m     entity.Message
			qID   *int64
			qUser *string
			qBody *string
		)
		if err := rows.Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Color, &m.Body, &m.System, &m.CreatedAt, &qID, &qUser, &qBody); err != nil {
			return nil, err
		}
		if qID != nil {
			m.ReplyTo = &entity.Quote{ID: *qID, Username: *qUser, Body: *qBody}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListRecentMessages returns the last limit visible messages, oldest first.
func (r *Repo) ListRecentMessages(ctx context.Context, roomID uuid.UUID, limit int) ([]entity.Message, error) {
	const q = `SELECT * FROM (` + messageSelect + ` WHERE m.room_id = $1 AND m.deleted_at IS NULL ORDER BY m.id DESC LIMIT $2) recent ORDER BY id`
	rows, err := r.pool.Query(ctx, q, roomID, limit)
	if err != nil {
		return nil, wrapErr(err)
	}
	return scanMessages(rows)
}

// GetMessage returns one visible message of the room or ErrNotFound.
func (r *Repo) GetMessage(ctx context.Context, roomID uuid.UUID, id int64) (*entity.Message, error) {
	const q = messageSelect + ` WHERE m.room_id = $1 AND m.id = $2 AND m.deleted_at IS NULL`
	rows, err := r.pool.Query(ctx, q, roomID, id)
	if err != nil {
		return nil, wrapErr(err)
	}
	out, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return &out[0], nil
}

// SetPinnedMessage pins a message of the room (nil unpins).
func (r *Repo) SetPinnedMessage(ctx context.Context, roomID uuid.UUID, id *int64) error {
	const q = `UPDATE rooms SET pinned_message_id = $2 WHERE id = $1`
	return r.exec(ctx, q, roomID, id)
}

// DeleteMessage soft-deletes a message in the room. ErrNotFound when the
// message does not exist, belongs elsewhere, is already deleted or — with
// onlyOwn — was written by someone else. A pinned message is unpinned.
func (r *Repo) DeleteMessage(ctx context.Context, roomID uuid.UUID, id int64, deletedBy uuid.UUID, onlyOwn bool) error {
	const q = `
		WITH del AS (
			UPDATE messages SET deleted_at = now(), deleted_by = $3
			WHERE id = $2 AND room_id = $1 AND deleted_at IS NULL AND (NOT $4 OR user_id = $3)
			RETURNING id
		), unpin AS (
			UPDATE rooms SET pinned_message_id = NULL WHERE id = $1 AND pinned_message_id IN (SELECT id FROM del)
		)
		SELECT count(*) FROM del
	`
	var n int
	if err := r.pool.QueryRow(ctx, q, roomID, id, deletedBy, onlyOwn).Scan(&n); err != nil {
		return wrapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearMessages soft-deletes every visible message in the room and
// unpins.
func (r *Repo) ClearMessages(ctx context.Context, roomID, deletedBy uuid.UUID) (int64, error) {
	const q = `
		WITH del AS (
			UPDATE messages SET deleted_at = now(), deleted_by = $2 WHERE room_id = $1 AND deleted_at IS NULL RETURNING id
		), unpin AS (
			UPDATE rooms SET pinned_message_id = NULL WHERE id = $1
		)
		SELECT count(*) FROM del
	`
	var n int64
	if err := r.pool.QueryRow(ctx, q, roomID, deletedBy).Scan(&n); err != nil {
		return 0, wrapErr(err)
	}
	return n, nil
}

// PurgeMessagesBefore hard-deletes messages older than the cutoff.
func (r *Repo) PurgeMessagesBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	const q = `DELETE FROM messages WHERE created_at < $1`
	tag, err := r.pool.Exec(ctx, q, cutoff)
	if err != nil {
		return 0, wrapErr(err)
	}
	return tag.RowsAffected(), nil
}

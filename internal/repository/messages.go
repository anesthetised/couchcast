package repository

import (
	"context"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
)

// CreateMessage stores a chat line and returns it with the username.
func (r *Repo) CreateMessage(ctx context.Context, roomID, userID uuid.UUID, body string) (*entity.Message, error) {
	const q = `
		WITH ins AS (
			INSERT INTO messages (room_id, user_id, body) VALUES ($1, $2, $3)
			RETURNING id, room_id, user_id, body, created_at
		)
		SELECT ins.id, ins.room_id, ins.user_id, u.username, ins.body, ins.created_at
		FROM ins JOIN users u ON u.id = ins.user_id
	`
	var m entity.Message
	err := r.pool.QueryRow(ctx, q, roomID, userID, body).Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Body, &m.CreatedAt)
	if err != nil {
		return nil, wrapErr(err)
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

// ListRecentMessages returns the last limit visible messages, oldest first.
func (r *Repo) ListRecentMessages(ctx context.Context, roomID uuid.UUID, limit int) ([]entity.Message, error) {
	const q = `
		SELECT * FROM (
			SELECT m.id, m.room_id, m.user_id, coalesce(u.username, ''), m.body, m.system, m.created_at
			FROM messages m LEFT JOIN users u ON u.id = m.user_id
			WHERE m.room_id = $1 AND m.deleted_at IS NULL
			ORDER BY m.id DESC
			LIMIT $2
		) recent ORDER BY id
	`
	rows, err := r.pool.Query(ctx, q, roomID, limit)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []entity.Message
	for rows.Next() {
		var m entity.Message
		if err := rows.Scan(&m.ID, &m.RoomID, &m.UserID, &m.Username, &m.Body, &m.System, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteMessage soft-deletes a message in the room. ErrNotFound when the
// message does not exist, belongs elsewhere or is already deleted.
func (r *Repo) DeleteMessage(ctx context.Context, roomID uuid.UUID, id int64, deletedBy uuid.UUID) error {
	const q = `
		UPDATE messages SET deleted_at = now(), deleted_by = $3
		WHERE id = $2 AND room_id = $1 AND deleted_at IS NULL
	`
	return r.exec(ctx, q, roomID, id, deletedBy)
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

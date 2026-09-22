package repository

import (
	"context"
	"fmt"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
)

const queueColumns = `q.id, q.room_id, q.media_id, q.added_by, coalesce(u.username, ''), q.rank, q.created_at, q.played_at,
	(SELECT count(*) FROM queue_votes v WHERE v.item_id = q.id)`

// ListQueue returns the room's unplayed items in rank order with vote counts.
func (r *Repo) ListQueue(ctx context.Context, roomID uuid.UUID) ([]entity.QueueItem, error) {
	const q = `
		SELECT ` + queueColumns + `
		FROM queue_items q LEFT JOIN users u ON u.id = q.added_by
		WHERE q.room_id = $1 AND q.played_at IS NULL
		ORDER BY q.rank, q.created_at
	`
	return r.listQueue(ctx, q, roomID)
}

// ListPlayed returns the room's most recently played items, newest first.
func (r *Repo) ListPlayed(ctx context.Context, roomID uuid.UUID, limit int) ([]entity.QueueItem, error) {
	const q = `
		SELECT ` + queueColumns + `
		FROM queue_items q LEFT JOIN users u ON u.id = q.added_by
		WHERE q.room_id = $1 AND q.played_at IS NOT NULL
		ORDER BY q.played_at DESC
		LIMIT $2
	`
	return r.listQueue(ctx, q, roomID, limit)
}

func (r *Repo) listQueue(ctx context.Context, q string, args ...any) ([]entity.QueueItem, error) {
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []entity.QueueItem
	for rows.Next() {
		var it entity.QueueItem
		if err := rows.Scan(&it.ID, &it.RoomID, &it.MediaID, &it.AddedBy, &it.AddedByName, &it.Rank, &it.CreatedAt, &it.PlayedAt, &it.Votes); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// MarkQueueItemPlayed moves an item to the room's history.
func (r *Repo) MarkQueueItemPlayed(ctx context.Context, roomID, itemID uuid.UUID, at time.Time) error {
	const q = `UPDATE queue_items SET played_at = $3 WHERE room_id = $1 AND id = $2 AND played_at IS NULL`
	return r.exec(ctx, q, roomID, itemID, at)
}

// RequeuePlayed puts every played item back into the queue in the order it
// was played, ranked after whatever is still queued.
func (r *Repo) RequeuePlayed(ctx context.Context, roomID uuid.UUID) error {
	const q = `
		WITH base AS (
			SELECT coalesce(max(rank)::bigint, 0) AS n FROM queue_items WHERE room_id = $1 AND played_at IS NULL
		), ordered AS (
			SELECT id, row_number() OVER (ORDER BY played_at, created_at) AS rn
			FROM queue_items WHERE room_id = $1 AND played_at IS NOT NULL
		)
		UPDATE queue_items q SET played_at = NULL, rank = lpad((base.n + ordered.rn)::text, 8, '0')
		FROM ordered, base WHERE q.id = ordered.id
	`
	_, err := r.pool.Exec(ctx, q, roomID)
	return wrapErr(err)
}

// ClearPlayed deletes the room's history.
func (r *Repo) ClearPlayed(ctx context.Context, roomID uuid.UUID) error {
	const q = `DELETE FROM queue_items WHERE room_id = $1 AND played_at IS NOT NULL`
	_, err := r.pool.Exec(ctx, q, roomID)
	return wrapErr(err)
}

// PurgePlayed drops history older than the cutoff and anything beyond the
// newest keep items per room.
func (r *Repo) PurgePlayed(ctx context.Context, before time.Time, keep int) (int64, error) {
	const q = `
		DELETE FROM queue_items WHERE id IN (
			SELECT id FROM (
				SELECT id, played_at, row_number() OVER (PARTITION BY room_id ORDER BY played_at DESC) AS rn
				FROM queue_items WHERE played_at IS NOT NULL
			) t WHERE t.rn > $2 OR t.played_at < $1
		)
	`
	tag, err := r.pool.Exec(ctx, q, before, keep)
	if err != nil {
		return 0, wrapErr(err)
	}
	return tag.RowsAffected(), nil
}

// AddQueueItem appends an item at the end of the queue.
func (r *Repo) AddQueueItem(ctx context.Context, q Querier, roomID, mediaID uuid.UUID, addedBy *uuid.UUID) (*entity.QueueItem, error) {
	const insert = `
		INSERT INTO queue_items (room_id, media_id, added_by, rank)
		VALUES ($1, $2, $3, (
			SELECT lpad((coalesce(max(rank)::bigint, 0) + 1)::text, 8, '0') FROM queue_items WHERE room_id = $1 AND played_at IS NULL
		))
		RETURNING id, room_id, media_id, added_by, rank, created_at
	`
	var it entity.QueueItem
	err := q.QueryRow(ctx, insert, roomID, mediaID, addedBy).Scan(&it.ID, &it.RoomID, &it.MediaID, &it.AddedBy, &it.Rank, &it.CreatedAt)
	if err != nil {
		return nil, wrapErr(err)
	}
	return &it, nil
}

// ClearQueue drops every unplayed item except keep (the current one).
func (r *Repo) ClearQueue(ctx context.Context, roomID uuid.UUID, keep *uuid.UUID) error {
	const q = `DELETE FROM queue_items WHERE room_id = $1 AND played_at IS NULL AND ($2::uuid IS NULL OR id <> $2)`
	_, err := r.pool.Exec(ctx, q, roomID, keep)
	return wrapErr(err)
}

// DeleteQueueItem removes an item; returns ErrNotFound when absent.
func (r *Repo) DeleteQueueItem(ctx context.Context, roomID, itemID uuid.UUID) error {
	const q = `DELETE FROM queue_items WHERE room_id = $1 AND id = $2`
	return r.exec(ctx, q, roomID, itemID)
}

// SetQueueRanks rewrites the ranks of the given items in one transaction:
// item ids in the desired order get ranks 00000001, 00000002, ...
func (r *Repo) SetQueueRanks(ctx context.Context, roomID uuid.UUID, ordered []uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	const q = `UPDATE queue_items SET rank = $3 WHERE room_id = $1 AND id = $2`
	for i, id := range ordered {
		if _, err := tx.Exec(ctx, q, roomID, id, fmt.Sprintf("%08d", i+1)); err != nil {
			return wrapErr(err)
		}
	}
	return tx.Commit(ctx)
}

// ToggleQueueVote adds the user's vote or removes it if present, returning
// whether the vote now exists.
func (r *Repo) ToggleQueueVote(ctx context.Context, itemID, userID uuid.UUID) (bool, error) {
	const del = `DELETE FROM queue_votes WHERE item_id = $1 AND user_id = $2`
	tag, err := r.pool.Exec(ctx, del, itemID, userID)
	if err != nil {
		return false, wrapErr(err)
	}
	if tag.RowsAffected() > 0 {
		return false, nil
	}
	const ins = `INSERT INTO queue_votes (item_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`
	if _, err := r.pool.Exec(ctx, ins, itemID, userID); err != nil {
		return false, wrapErr(err)
	}
	return true, nil
}

// ListQueueVotesByUser returns the item ids the user has voted for in a room.
func (r *Repo) ListQueueVotesByUser(ctx context.Context, roomID, userID uuid.UUID) ([]uuid.UUID, error) {
	const q = `
		SELECT v.item_id FROM queue_votes v JOIN queue_items i ON i.id = v.item_id
		WHERE i.room_id = $1 AND v.user_id = $2
	`
	rows, err := r.pool.Query(ctx, q, roomID, userID)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// UpdateRoomPlayback persists the authoritative playback state.
func (r *Repo) UpdateRoomPlayback(ctx context.Context, roomID uuid.UUID, p entity.PlaybackState) error {
	const q = `
		UPDATE rooms SET current_item_id = $2, playing = $3, position_ms = $4, position_at = $5, rate = $6, updated_at = now()
		WHERE id = $1
	`
	_, err := r.pool.Exec(ctx, q, roomID, p.CurrentItemID, p.Playing, p.PositionMs, p.PositionAt, p.Rate)
	return wrapErr(err)
}

// ListRoomIDsWithMedia returns rooms whose queue contains the media.
func (r *Repo) ListRoomIDsWithMedia(ctx context.Context, mediaID uuid.UUID) ([]uuid.UUID, error) {
	const q = `SELECT DISTINCT room_id FROM queue_items WHERE media_id = $1`
	rows, err := r.pool.Query(ctx, q, mediaID)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

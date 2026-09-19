package repository

import (
	"context"
	"fmt"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
)

const queueColumns = `q.id, q.room_id, q.media_id, q.added_by, coalesce(u.username, ''), q.rank, q.created_at,
	(SELECT count(*) FROM queue_votes v WHERE v.item_id = q.id)`

// ListQueue returns the room's items in rank order with vote counts.
func (r *Repo) ListQueue(ctx context.Context, roomID uuid.UUID) ([]entity.QueueItem, error) {
	const q = `
		SELECT ` + queueColumns + `
		FROM queue_items q LEFT JOIN users u ON u.id = q.added_by
		WHERE q.room_id = $1
		ORDER BY q.rank, q.created_at
	`
	rows, err := r.pool.Query(ctx, q, roomID)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []entity.QueueItem
	for rows.Next() {
		var it entity.QueueItem
		if err := rows.Scan(&it.ID, &it.RoomID, &it.MediaID, &it.AddedBy, &it.AddedByName, &it.Rank, &it.CreatedAt, &it.Votes); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// AddQueueItem appends an item at the end of the queue.
func (r *Repo) AddQueueItem(ctx context.Context, q Querier, roomID, mediaID uuid.UUID, addedBy *uuid.UUID) (*entity.QueueItem, error) {
	const insert = `
		INSERT INTO queue_items (room_id, media_id, added_by, rank)
		VALUES ($1, $2, $3, (
			SELECT lpad((coalesce(max(rank)::bigint, 0) + 1)::text, 8, '0') FROM queue_items WHERE room_id = $1
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
		UPDATE rooms SET current_item_id = $2, playing = $3, position_ms = $4, position_at = $5, updated_at = now()
		WHERE id = $1
	`
	_, err := r.pool.Exec(ctx, q, roomID, p.CurrentItemID, p.Playing, p.PositionMs, p.PositionAt)
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

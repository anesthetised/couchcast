package repository

import (
	"context"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
)

// GetMember returns the membership row or ErrNotFound.
func (r *Repo) GetMember(ctx context.Context, roomID, userID uuid.UUID) (*entity.RoomMember, error) {
	const q = `
		SELECT m.room_id, m.user_id, u.username, m.role, m.joined_at
		FROM room_members m JOIN users u ON u.id = m.user_id
		WHERE m.room_id = $1 AND m.user_id = $2
	`
	var m entity.RoomMember
	err := r.pool.QueryRow(ctx, q, roomID, userID).Scan(&m.RoomID, &m.UserID, &m.Username, &m.Role, &m.JoinedAt)
	if err != nil {
		return nil, wrapErr(err)
	}
	return &m, nil
}

// ListMembers returns members ordered owner, moderators, members, then by
// username.
func (r *Repo) ListMembers(ctx context.Context, roomID uuid.UUID) ([]entity.RoomMember, error) {
	const q = `
		SELECT m.room_id, m.user_id, u.username, m.role, m.joined_at
		FROM room_members m JOIN users u ON u.id = m.user_id
		WHERE m.room_id = $1
		ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'moderator' THEN 1 ELSE 2 END, u.username
	`
	rows, err := r.pool.Query(ctx, q, roomID)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []entity.RoomMember
	for rows.Next() {
		var m entity.RoomMember
		if err := rows.Scan(&m.RoomID, &m.UserID, &m.Username, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpsertMember adds a member or changes an existing member's role. The
// owner row is never touched through this path.
func (r *Repo) UpsertMember(ctx context.Context, roomID, userID uuid.UUID, role entity.RoomRole) error {
	const q = `
		INSERT INTO room_members (room_id, user_id, role) VALUES ($1, $2, $3)
		ON CONFLICT (room_id, user_id) DO UPDATE SET role = EXCLUDED.role
		WHERE room_members.role <> 'owner'
	`
	_, err := r.pool.Exec(ctx, q, roomID, userID, role)
	return wrapErr(err)
}

// DeleteMember removes a membership; the owner cannot be removed.
func (r *Repo) DeleteMember(ctx context.Context, roomID, userID uuid.UUID) error {
	const q = `DELETE FROM room_members WHERE room_id = $1 AND user_id = $2 AND role <> 'owner'`
	return r.exec(ctx, q, roomID, userID)
}

// GetBan returns the room ban or ErrNotFound.
func (r *Repo) GetBan(ctx context.Context, roomID, userID uuid.UUID) (*entity.RoomBan, error) {
	const q = `
		SELECT b.room_id, b.user_id, u.username, b.banned_by, coalesce(b.reason, ''), b.created_at
		FROM room_bans b JOIN users u ON u.id = b.user_id
		WHERE b.room_id = $1 AND b.user_id = $2
	`
	var b entity.RoomBan
	err := r.pool.QueryRow(ctx, q, roomID, userID).Scan(&b.RoomID, &b.UserID, &b.Username, &b.BannedBy, &b.Reason, &b.CreatedAt)
	if err != nil {
		return nil, wrapErr(err)
	}
	return &b, nil
}

// ListBans returns the room's bans, newest first.
func (r *Repo) ListBans(ctx context.Context, roomID uuid.UUID) ([]entity.RoomBan, error) {
	const q = `
		SELECT b.room_id, b.user_id, u.username, b.banned_by, coalesce(b.reason, ''), b.created_at
		FROM room_bans b JOIN users u ON u.id = b.user_id
		WHERE b.room_id = $1
		ORDER BY b.created_at DESC
	`
	rows, err := r.pool.Query(ctx, q, roomID)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []entity.RoomBan
	for rows.Next() {
		var b entity.RoomBan
		if err := rows.Scan(&b.RoomID, &b.UserID, &b.Username, &b.BannedBy, &b.Reason, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CreateBan records a ban; re-banning updates the reason.
func (r *Repo) CreateBan(ctx context.Context, roomID, userID, bannedBy uuid.UUID, reason string) error {
	const q = `
		INSERT INTO room_bans (room_id, user_id, banned_by, reason) VALUES ($1, $2, $3, $4)
		ON CONFLICT (room_id, user_id) DO UPDATE SET banned_by = EXCLUDED.banned_by, reason = EXCLUDED.reason, created_at = now()
	`
	_, err := r.pool.Exec(ctx, q, roomID, userID, bannedBy, reason)
	return wrapErr(err)
}

// DeleteBan lifts a ban; returns ErrNotFound when there was none.
func (r *Repo) DeleteBan(ctx context.Context, roomID, userID uuid.UUID) error {
	const q = `DELETE FROM room_bans WHERE room_id = $1 AND user_id = $2`
	return r.exec(ctx, q, roomID, userID)
}

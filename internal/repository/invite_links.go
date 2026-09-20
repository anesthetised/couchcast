package repository

import (
	"context"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
)

const inviteLinkColumns = `l.id, l.room_id, r.slug, r.name, l.created_by, coalesce(u.username, ''), l.expires_at, l.max_uses, l.uses, l.revoked_at, l.created_at`

const inviteLinkFrom = `
	FROM room_invite_links l
	JOIN rooms r ON r.id = l.room_id
	LEFT JOIN users u ON u.id = l.created_by
`

func scanInviteLink(row interface{ Scan(dest ...any) error }) (*entity.InviteLink, error) {
	var l entity.InviteLink
	err := row.Scan(&l.ID, &l.RoomID, &l.RoomSlug, &l.RoomName, &l.CreatedBy, &l.Creator, &l.ExpiresAt, &l.MaxUses, &l.Uses, &l.RevokedAt, &l.CreatedAt)
	if err != nil {
		return nil, wrapErr(err)
	}
	return &l, nil
}

// CreateInviteLink stores a link by its token hash.
func (r *Repo) CreateInviteLink(ctx context.Context, roomID uuid.UUID, tokenHash []byte, createdBy uuid.UUID, expiresAt *time.Time, maxUses *int) (*entity.InviteLink, error) {
	const q = `
		INSERT INTO room_invite_links (room_id, token_hash, created_by, expires_at, max_uses)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`
	var id uuid.UUID
	if err := r.pool.QueryRow(ctx, q, roomID, tokenHash, createdBy, expiresAt, maxUses).Scan(&id); err != nil {
		return nil, wrapErr(err)
	}
	return r.GetInviteLink(ctx, id)
}

// GetInviteLink returns a link by id or ErrNotFound.
func (r *Repo) GetInviteLink(ctx context.Context, id uuid.UUID) (*entity.InviteLink, error) {
	const q = `SELECT ` + inviteLinkColumns + inviteLinkFrom + ` WHERE l.id = $1`
	return scanInviteLink(r.pool.QueryRow(ctx, q, id))
}

// GetInviteLinkByToken resolves a token hash or ErrNotFound.
func (r *Repo) GetInviteLinkByToken(ctx context.Context, tokenHash []byte) (*entity.InviteLink, error) {
	const q = `SELECT ` + inviteLinkColumns + inviteLinkFrom + ` WHERE l.token_hash = $1`
	return scanInviteLink(r.pool.QueryRow(ctx, q, tokenHash))
}

// ListInviteLinks returns a room's links, newest first, revoked included.
func (r *Repo) ListInviteLinks(ctx context.Context, roomID uuid.UUID) ([]entity.InviteLink, error) {
	const q = `SELECT ` + inviteLinkColumns + inviteLinkFrom + ` WHERE l.room_id = $1 ORDER BY l.created_at DESC`
	rows, err := r.pool.Query(ctx, q, roomID)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()
	var out []entity.InviteLink
	for rows.Next() {
		l, err := scanInviteLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// RevokeInviteLink marks a link unusable; ErrNotFound when it does not
// belong to the room.
func (r *Repo) RevokeInviteLink(ctx context.Context, roomID, id uuid.UUID) error {
	const q = `UPDATE room_invite_links SET revoked_at = now() WHERE room_id = $1 AND id = $2 AND revoked_at IS NULL`
	return r.exec(ctx, q, roomID, id)
}

// UseInviteLink admits a user through a link in one transaction: the use
// counter is charged only while the link is within its limit and the
// membership is created (existing members are left as they are).
// ErrConflict means the link ran out between the check and the use.
func (r *Repo) UseInviteLink(ctx context.Context, linkID, userID uuid.UUID, now time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	const use = `
		UPDATE room_invite_links SET uses = uses + 1
		WHERE id = $1 AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > $2)
		  AND (max_uses IS NULL OR uses < max_uses)
		RETURNING room_id
	`
	var roomID uuid.UUID
	if err := tx.QueryRow(ctx, use, linkID, now).Scan(&roomID); err != nil {
		werr := wrapErr(err)
		if werr == ErrNotFound { //nolint:errorlint // sentinel returned directly by wrapErr
			return ErrConflict
		}
		return werr
	}
	const member = `INSERT INTO room_members (room_id, user_id, role) VALUES ($1, $2, 'member') ON CONFLICT DO NOTHING`
	if _, err := tx.Exec(ctx, member, roomID, userID); err != nil {
		return wrapErr(err)
	}
	return tx.Commit(ctx)
}

package repository

import (
	"context"
	"errors"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
)

const inviteColumns = `i.id, i.room_id, r.slug, r.name, i.invitee_id, i.inviter_id, coalesce(u.username, ''), i.status, i.created_at`

const inviteFrom = `
	FROM invites i
	JOIN rooms r ON r.id = i.room_id
	LEFT JOIN users u ON u.id = i.inviter_id
`

// CreateInvite creates a pending invite. A declined or accepted invite for
// the same user is reset to pending (callers reject current members); a
// pending one yields ErrConflict.
func (r *Repo) CreateInvite(ctx context.Context, roomID, inviteeID, inviterID uuid.UUID) (*entity.Invite, error) {
	const q = `
		INSERT INTO invites (room_id, invitee_id, inviter_id) VALUES ($1, $2, $3)
		ON CONFLICT (room_id, invitee_id) DO UPDATE
			SET status = 'pending', inviter_id = EXCLUDED.inviter_id, created_at = now()
			WHERE invites.status <> 'pending'
		RETURNING id
	`
	var id uuid.UUID
	if err := r.pool.QueryRow(ctx, q, roomID, inviteeID, inviterID).Scan(&id); err != nil {
		// ON CONFLICT ... WHERE false returns no row: a pending invite exists.
		werr := wrapErr(err)
		if errors.Is(werr, ErrNotFound) {
			werr = ErrConflict
		}
		return nil, werr
	}
	return r.GetInvite(ctx, id)
}

// GetInvite returns an invite or ErrNotFound.
func (r *Repo) GetInvite(ctx context.Context, id uuid.UUID) (*entity.Invite, error) {
	const q = `SELECT ` + inviteColumns + inviteFrom + ` WHERE i.id = $1`
	var inv entity.Invite
	err := r.pool.QueryRow(ctx, q, id).Scan(&inv.ID, &inv.RoomID, &inv.RoomSlug, &inv.RoomName, &inv.InviteeID,
		&inv.InviterID, &inv.Inviter, &inv.Status, &inv.CreatedAt)
	if err != nil {
		return nil, wrapErr(err)
	}
	return &inv, nil
}

// ListPendingInvites returns the user's pending invites, newest first.
func (r *Repo) ListPendingInvites(ctx context.Context, inviteeID uuid.UUID) ([]entity.Invite, error) {
	const q = `SELECT ` + inviteColumns + inviteFrom + ` WHERE i.invitee_id = $1 AND i.status = 'pending' ORDER BY i.created_at DESC`
	rows, err := r.pool.Query(ctx, q, inviteeID)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []entity.Invite
	for rows.Next() {
		var inv entity.Invite
		if err := rows.Scan(&inv.ID, &inv.RoomID, &inv.RoomSlug, &inv.RoomName, &inv.InviteeID,
			&inv.InviterID, &inv.Inviter, &inv.Status, &inv.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// AcceptInvite marks the invite accepted and adds the member row in one
// transaction. Only pending invites can be accepted.
func (r *Repo) AcceptInvite(ctx context.Context, id uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	const update = `
		UPDATE invites SET status = 'accepted' WHERE id = $1 AND status = 'pending'
		RETURNING room_id, invitee_id
	`
	var roomID, userID uuid.UUID
	if err := tx.QueryRow(ctx, update, id).Scan(&roomID, &userID); err != nil {
		return wrapErr(err)
	}

	const member = `
		INSERT INTO room_members (room_id, user_id, role) VALUES ($1, $2, 'member')
		ON CONFLICT (room_id, user_id) DO NOTHING
	`
	if _, err := tx.Exec(ctx, member, roomID, userID); err != nil {
		return wrapErr(err)
	}

	return tx.Commit(ctx)
}

// DeclineInvite marks a pending invite declined.
func (r *Repo) DeclineInvite(ctx context.Context, id uuid.UUID) error {
	const q = `UPDATE invites SET status = 'declined' WHERE id = $1 AND status = 'pending'`
	return r.exec(ctx, q, id)
}

package repository

import (
	"context"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
)

// CreateSession stores a session for the hashed token.
func (r *Repo) CreateSession(ctx context.Context, tokenHash []byte, userID uuid.UUID, expiresAt time.Time) error {
	const q = `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES ($1, $2, $3)
	`
	_, err := r.pool.Exec(ctx, q, tokenHash, userID, expiresAt)
	return wrapErr(err)
}

// GetSessionUser resolves a live session to its user in one round trip.
// Expired sessions are treated as missing.
func (r *Repo) GetSessionUser(ctx context.Context, tokenHash []byte, now time.Time) (*entity.Session, *entity.User, error) {
	const q = `
		SELECT s.token_hash, s.user_id, s.created_at, s.last_seen_at, s.expires_at,
		       u.id, u.username, u.password_hash, u.role, u.banned_at, u.banned_reason, u.banned_by, u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > $2
	`

	var (
		s entity.Session
		u entity.User
	)
	err := r.pool.QueryRow(ctx, q, tokenHash, now).Scan(
		&s.TokenHash, &s.UserID, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt,
		&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.BannedAt, &u.BannedReason, &u.BannedBy, &u.CreatedAt,
	)
	if err != nil {
		return nil, nil, wrapErr(err)
	}

	return &s, &u, nil
}

// TouchSession extends a sliding session. Called at most once per touch
// interval by the auth layer to keep write volume low.
func (r *Repo) TouchSession(ctx context.Context, tokenHash []byte, now, expiresAt time.Time) error {
	const q = `UPDATE sessions SET last_seen_at = $2, expires_at = $3 WHERE token_hash = $1`
	_, err := r.pool.Exec(ctx, q, tokenHash, now, expiresAt)
	return wrapErr(err)
}

// DeleteSession removes one session; missing rows are not an error.
func (r *Repo) DeleteSession(ctx context.Context, tokenHash []byte) error {
	const q = `DELETE FROM sessions WHERE token_hash = $1`
	_, err := r.pool.Exec(ctx, q, tokenHash)
	return wrapErr(err)
}

// DeleteUserSessions logs the user out everywhere (used on ban and on
// password change).
func (r *Repo) DeleteUserSessions(ctx context.Context, userID uuid.UUID) (int64, error) {
	const q = `DELETE FROM sessions WHERE user_id = $1`
	tag, err := r.pool.Exec(ctx, q, userID)
	if err != nil {
		return 0, wrapErr(err)
	}
	return tag.RowsAffected(), nil
}

// DeleteExpiredSessions is run periodically by the web server.
func (r *Repo) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	const q = `DELETE FROM sessions WHERE expires_at <= $1`
	tag, err := r.pool.Exec(ctx, q, now)
	if err != nil {
		return 0, wrapErr(err)
	}
	return tag.RowsAffected(), nil
}

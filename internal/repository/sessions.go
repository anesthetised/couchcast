package repository

import (
	"context"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
)

// CreateSession stores a session for the hashed token.
func (r *Repo) CreateSession(ctx context.Context, tokenHash []byte, userID uuid.UUID, userAgent string, expiresAt time.Time) error {
	const q = `
		INSERT INTO sessions (token_hash, user_id, user_agent, expires_at)
		VALUES ($1, $2, $3, $4)
	`
	_, err := r.pool.Exec(ctx, q, tokenHash, userID, userAgent, expiresAt)
	return wrapErr(err)
}

// GetSessionUser resolves a live session to its user in one round trip.
// Expired sessions are treated as missing.
func (r *Repo) GetSessionUser(ctx context.Context, tokenHash []byte, now time.Time) (*entity.Session, *entity.User, error) {
	const q = `
		SELECT s.token_hash, s.id, s.user_id, s.user_agent, s.created_at, s.last_seen_at, s.expires_at,
		       u.id, u.username, u.password_hash, u.role, u.banned_at, u.banned_reason, u.banned_by, u.created_at, u.avatar_color
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > $2
	`

	var (
		s entity.Session
		u entity.User
	)
	err := r.pool.QueryRow(ctx, q, tokenHash, now).Scan(
		&s.TokenHash, &s.ID, &s.UserID, &s.UserAgent, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt,
		&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.BannedAt, &u.BannedReason, &u.BannedBy, &u.CreatedAt, &u.AvatarColor,
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

// ListUserSessions returns the user's live sessions, most recent first.
func (r *Repo) ListUserSessions(ctx context.Context, userID uuid.UUID, now time.Time) ([]entity.Session, error) {
	const q = `
		SELECT token_hash, id, user_id, user_agent, created_at, last_seen_at, expires_at
		FROM sessions WHERE user_id = $1 AND expires_at > $2
		ORDER BY last_seen_at DESC, created_at DESC
	`
	rows, err := r.pool.Query(ctx, q, userID, now)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()
	var out []entity.Session
	for rows.Next() {
		var s entity.Session
		if err := rows.Scan(&s.TokenHash, &s.ID, &s.UserID, &s.UserAgent, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt); err != nil {
			return nil, wrapErr(err)
		}
		out = append(out, s)
	}
	return out, wrapErr(rows.Err())
}

// DeleteUserSession signs one of the user's sessions out by its public
// id; ErrNotFound when it is not theirs or already gone.
func (r *Repo) DeleteUserSession(ctx context.Context, userID, id uuid.UUID) error {
	const q = `DELETE FROM sessions WHERE user_id = $1 AND id = $2`
	tag, err := r.pool.Exec(ctx, q, userID, id)
	if err != nil {
		return wrapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteOtherSessions signs the user out everywhere but the kept session.
func (r *Repo) DeleteOtherSessions(ctx context.Context, userID uuid.UUID, keepHash []byte) (int64, error) {
	const q = `DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2`
	tag, err := r.pool.Exec(ctx, q, userID, keepHash)
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

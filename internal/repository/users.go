package repository

import (
	"context"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/anesthetised/couchcast/internal/entity"
)

const userColumns = `id, username, password_hash, role, banned_at, banned_reason, banned_by, created_at, avatar_color`

func scanUser(row pgx.Row) (*entity.User, error) {
	var u entity.User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.BannedAt, &u.BannedReason, &u.BannedBy, &u.CreatedAt, &u.AvatarColor)
	if err != nil {
		return nil, wrapErr(err)
	}
	return &u, nil
}

// CreateUser inserts a new account. Returns ErrConflict when the username
// is taken.
func (r *Repo) CreateUser(ctx context.Context, username, passwordHash string) (*entity.User, error) {
	const q = `
		INSERT INTO users (username, password_hash)
		VALUES ($1, $2)
		RETURNING ` + userColumns

	return scanUser(r.pool.QueryRow(ctx, q, username, passwordHash))
}

// GetUserByID returns a user or ErrNotFound.
func (r *Repo) GetUserByID(ctx context.Context, id uuid.UUID) (*entity.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = $1`
	return scanUser(r.pool.QueryRow(ctx, q, id))
}

// GetUserByUsername returns a user or ErrNotFound. The match ignores case.
func (r *Repo) GetUserByUsername(ctx context.Context, username string) (*entity.User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE lower(username) = lower($1)`
	return scanUser(r.pool.QueryRow(ctx, q, username))
}

// SetUserRole changes the site-wide role.
func (r *Repo) SetUserRole(ctx context.Context, id uuid.UUID, role entity.Role) error {
	const q = `UPDATE users SET role = $2 WHERE id = $1`
	return r.exec(ctx, q, id, role)
}

// BanUser marks the account as banned. Sessions are revoked by the caller.
func (r *Repo) BanUser(ctx context.Context, id, bannedBy uuid.UUID, reason string, at time.Time) error {
	const q = `UPDATE users SET banned_at = $2, banned_by = $3, banned_reason = $4 WHERE id = $1`
	return r.exec(ctx, q, id, at, bannedBy, reason)
}

// UnbanUser clears the ban.
func (r *Repo) UnbanUser(ctx context.Context, id uuid.UUID) error {
	const q = `UPDATE users SET banned_at = NULL, banned_by = NULL, banned_reason = NULL WHERE id = $1`
	return r.exec(ctx, q, id)
}

// exec runs a statement that must affect exactly one row.
func (r *Repo) exec(ctx context.Context, q string, args ...any) error {
	tag, err := r.pool.Exec(ctx, q, args...)
	if err != nil {
		return wrapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserAvatarColor stores the palette key chosen on the profile page.
func (r *Repo) SetUserAvatarColor(ctx context.Context, id uuid.UUID, color string) error {
	const q = `UPDATE users SET avatar_color = $2 WHERE id = $1`
	_, err := r.pool.Exec(ctx, q, id, color)
	return wrapErr(err)
}

// SetUserPassword replaces the password hash.
func (r *Repo) SetUserPassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	const q = `UPDATE users SET password_hash = $2 WHERE id = $1`
	_, err := r.pool.Exec(ctx, q, id, passwordHash)
	return wrapErr(err)
}

// SearchUsernames returns up to limit usernames starting with prefix
// (case-insensitive), excluding banned accounts.
func (r *Repo) SearchUsernames(ctx context.Context, prefix string, limit int) ([]string, error) {
	const q = `
		SELECT username FROM users
		WHERE banned_at IS NULL AND lower(username) LIKE lower($1) || '%' ESCAPE '\'
		ORDER BY username
		LIMIT $2
	`
	rows, err := r.pool.Query(ctx, q, escapeLike(prefix), limit)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

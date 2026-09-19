// Package repository is the persistence layer: plain SQL over pgx, one file
// per aggregate. Methods return entity types and translate pgx.ErrNoRows
// into ErrNotFound so callers never import pgx.
package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when an insert violates a unique constraint.
var ErrConflict = errors.New("already exists")

// Repo wraps the connection pool. It is safe for concurrent use.
type Repo struct {
	pool *pgxpool.Pool
}

// New creates a repository over the pool.
func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// Pool exposes the pool for components that need transactions or LISTEN.
func (r *Repo) Pool() *pgxpool.Pool { return r.pool }

// Ping implements apihttp.Pinger.
func (r *Repo) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }

// Querier is the subset of pgxpool.Pool and pgx.Tx used by repository
// methods, so the same code runs inside and outside a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// wrapErr maps driver errors onto the package sentinels.
func wrapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case isUniqueViolation(err):
		return ErrConflict
	default:
		return err
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

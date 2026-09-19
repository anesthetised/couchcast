// Package jobs is a small PostgreSQL-backed job queue: claims use
// FOR UPDATE SKIP LOCKED, workers are woken by LISTEN/NOTIFY with a polling
// fallback, failures retry with exponential backoff, and locks held by dead
// workers are recovered after a timeout.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Status is the lifecycle of a job row.
type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Job is one row of the jobs table.
type Job struct {
	ID          uuid.UUID
	Kind        string
	Payload     json.RawMessage
	Status      Status
	Attempts    int
	MaxAttempts int
	RunAt       time.Time
	LockedAt    *time.Time
	LockedBy    string
	LastError   string
	CreatedAt   time.Time
}

// ErrNoJobs is returned by Claim when nothing is runnable.
var ErrNoJobs = errors.New("jobs: no runnable job")

// Channel is the NOTIFY channel used to wake workers.
const Channel = "jobs"

// DefaultMaxAttempts applies when Enqueue is given 0.
const DefaultMaxAttempts = 3

// StaleAfter is how long a running job may hold its lock before it is
// assumed dead and returned to pending.
const StaleAfter = 30 * time.Minute

// Querier is satisfied by *pgxpool.Pool and pgx.Tx.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Queue operates on the jobs table.
type Queue struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New creates a queue over the pool.
func New(pool *pgxpool.Pool) *Queue {
	return &Queue{pool: pool, now: time.Now}
}

const jobColumns = `id, kind, payload, status, attempts, max_attempts, run_at, locked_at, coalesce(locked_by, ''), coalesce(last_error, ''), created_at`

func scanJob(row pgx.Row) (*Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Kind, &j.Payload, &j.Status, &j.Attempts, &j.MaxAttempts, &j.RunAt, &j.LockedAt, &j.LockedBy, &j.LastError, &j.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// Enqueue inserts a pending job and notifies workers. Pass a transaction
// as q to make the insert atomic with other writes; the notification is
// delivered when that transaction commits.
func (q *Queue) Enqueue(ctx context.Context, tx Querier, kind string, payload any, maxAttempts int) (uuid.UUID, error) {
	if tx == nil {
		tx = q.pool
	}
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil(), fmt.Errorf("jobs: encode payload: %w", err)
	}

	const insert = `INSERT INTO jobs (kind, payload, max_attempts, run_at) VALUES ($1, $2, $3, $4) RETURNING id`
	var id uuid.UUID
	if err := tx.QueryRow(ctx, insert, kind, b, maxAttempts, q.now()).Scan(&id); err != nil {
		return uuid.Nil(), fmt.Errorf("jobs: enqueue: %w", err)
	}

	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, Channel, id.String()); err != nil {
		return uuid.Nil(), fmt.Errorf("jobs: notify: %w", err)
	}

	return id, nil
}

// Claim atomically takes the oldest runnable job of the given kinds.
func (q *Queue) Claim(ctx context.Context, worker string, kinds []string) (*Job, error) {
	const claim = `
		UPDATE jobs SET status = 'running', locked_at = $3, locked_by = $2, attempts = attempts + 1, updated_at = $3
		WHERE id = (
			SELECT id FROM jobs
			WHERE status = 'pending' AND run_at <= $3 AND kind = ANY($1)
			ORDER BY run_at, created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING ` + jobColumns

	j, err := scanJob(q.pool.QueryRow(ctx, claim, kinds, worker, q.now()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoJobs
	}
	if err != nil {
		return nil, fmt.Errorf("jobs: claim: %w", err)
	}
	return j, nil
}

// Complete marks the job done.
func (q *Queue) Complete(ctx context.Context, id uuid.UUID) error {
	const done = `UPDATE jobs SET status = 'done', locked_at = NULL, locked_by = NULL, updated_at = $2 WHERE id = $1`
	_, err := q.pool.Exec(ctx, done, id, q.now())
	return err
}

// Fail records the error and either schedules a retry with exponential
// backoff or, when attempts are exhausted or the error is permanent, marks
// the job failed. It returns whether a retry was scheduled.
func (q *Queue) Fail(ctx context.Context, j *Job, cause error) (bool, error) {
	msg := cause.Error()
	if len(msg) > 2000 {
		msg = msg[:2000]
	}

	retry := j.Attempts < j.MaxAttempts && !IsPermanent(cause)
	if !retry {
		const failed = `UPDATE jobs SET status = 'failed', locked_at = NULL, locked_by = NULL, last_error = $2, updated_at = $3 WHERE id = $1`
		_, err := q.pool.Exec(ctx, failed, j.ID, msg, q.now())
		return false, err
	}

	const pending = `UPDATE jobs SET status = 'pending', locked_at = NULL, locked_by = NULL, last_error = $2, run_at = $3, updated_at = $4 WHERE id = $1`
	now := q.now()
	_, err := q.pool.Exec(ctx, pending, j.ID, msg, now.Add(Backoff(j.Attempts)), now)
	return true, err
}

// Backoff returns the delay before retry number attempt (1-based):
// 30s, 1m, 2m, 4m, ... capped at 30 minutes.
func Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := 30 * time.Second * time.Duration(math.Pow(2, float64(attempt-1)))
	if d > 30*time.Minute {
		d = 30 * time.Minute
	}
	return d
}

// RecoverStale returns jobs whose lock is older than StaleAfter to pending
// so another worker can pick them up. Attempts already count the crashed
// run, so a job that keeps killing its worker eventually fails.
func (q *Queue) RecoverStale(ctx context.Context) (int64, error) {
	const recover = `
		UPDATE jobs SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'pending' END,
		       locked_at = NULL, locked_by = NULL, last_error = 'worker lock expired', updated_at = $2
		WHERE status = 'running' AND locked_at < $1
	`
	now := q.now()
	tag, err := q.pool.Exec(ctx, recover, now.Add(-StaleAfter), now)
	if err != nil {
		return 0, fmt.Errorf("jobs: recover stale: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Retry resets a failed job to pending with a fresh attempt budget.
func (q *Queue) Retry(ctx context.Context, id uuid.UUID) error {
	const retry = `UPDATE jobs SET status = 'pending', attempts = 0, run_at = $2, last_error = NULL, updated_at = $2 WHERE id = $1 AND status = 'failed'`
	if _, err := q.pool.Exec(ctx, retry, id, q.now()); err != nil {
		return err
	}
	_, err := q.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, Channel, id.String())
	return err
}

// Notify publishes on an arbitrary channel (used for media progress).
func (q *Queue) Notify(ctx context.Context, channel, payload string) error {
	_, err := q.pool.Exec(ctx, `SELECT pg_notify($1, $2)`, channel, payload)
	return err
}

// permanentError marks failures that must not be retried.
type permanentError struct{ err error }

func (p permanentError) Error() string { return p.err.Error() }
func (p permanentError) Unwrap() error { return p.err }

// Permanent wraps err so Fail does not schedule a retry.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

// IsPermanent reports whether err was wrapped by Permanent.
func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

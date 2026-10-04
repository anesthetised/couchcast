package jobs

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

func TestBackoff(t *testing.T) {
	assert.Equal(t, 30*time.Second, Backoff(0))
	assert.Equal(t, 30*time.Second, Backoff(1))
	assert.Equal(t, time.Minute, Backoff(2))
	assert.Equal(t, 4*time.Minute, Backoff(4))
	assert.Equal(t, 30*time.Minute, Backoff(20))
}

func TestPermanent(t *testing.T) {
	base := errors.New("boom")
	assert.False(t, IsPermanent(base))
	p := Permanent(base)
	assert.True(t, IsPermanent(p))
	assert.ErrorIs(t, p, base)
	assert.Nil(t, Permanent(nil))
}

func TestQueueLifecycle(t *testing.T) {
	pool := repotest.Pool(t)
	ctx := context.Background()

	now := time.Now().Truncate(time.Millisecond)
	q := New(pool)
	q.now = func() time.Time { return now }

	_, err := q.Claim(ctx, "w1", []string{"ingest"})
	assert.ErrorIs(t, err, ErrNoJobs)

	id, err := q.Enqueue(ctx, nil, "ingest", map[string]string{"mediaId": "x"}, 2)
	require.NoError(t, err)

	// Wrong kind is invisible.
	_, err = q.Claim(ctx, "w1", []string{"other"})
	assert.ErrorIs(t, err, ErrNoJobs)

	job, err := q.Claim(ctx, "w1", []string{"ingest"})
	require.NoError(t, err)
	assert.Equal(t, id, job.ID)
	assert.Equal(t, 1, job.Attempts)
	assert.Equal(t, StatusRunning, job.Status)
	assert.JSONEq(t, `{"mediaId":"x"}`, string(job.Payload))

	// Locked: nobody else gets it.
	_, err = q.Claim(ctx, "w2", []string{"ingest"})
	assert.ErrorIs(t, err, ErrNoJobs)

	// First failure retries after backoff.
	retry, err := q.Fail(ctx, job, errors.New("transient"))
	require.NoError(t, err)
	assert.True(t, retry)
	_, err = q.Claim(ctx, "w1", []string{"ingest"})
	assert.ErrorIs(t, err, ErrNoJobs, "not runnable before run_at")

	now = now.Add(Backoff(1) + time.Second)
	job, err = q.Claim(ctx, "w1", []string{"ingest"})
	require.NoError(t, err)
	assert.Equal(t, 2, job.Attempts)
	assert.Equal(t, "transient", job.LastError)

	// Attempts exhausted: failed for good.
	retry, err = q.Fail(ctx, job, errors.New("still broken"))
	require.NoError(t, err)
	assert.False(t, retry)

	// Retry resets the budget.
	require.NoError(t, q.Retry(ctx, job.ID))
	job, err = q.Claim(ctx, "w1", []string{"ingest"})
	require.NoError(t, err)
	assert.Equal(t, 1, job.Attempts)

	// Permanent errors never retry.
	retry, err = q.Fail(ctx, job, Permanent(errors.New("bad url")))
	require.NoError(t, err)
	assert.False(t, retry)
	_, err = q.Claim(ctx, "w1", []string{"ingest"})
	assert.ErrorIs(t, err, ErrNoJobs)

	// Complete.
	id2, err := q.Enqueue(ctx, nil, "ingest", nil, 0)
	require.NoError(t, err)
	job, err = q.Claim(ctx, "w1", []string{"ingest"})
	require.NoError(t, err)
	assert.Equal(t, id2, job.ID)
	assert.Equal(t, DefaultMaxAttempts, job.MaxAttempts)
	require.NoError(t, q.Complete(ctx, job))
	_, err = q.Claim(ctx, "w1", []string{"ingest"})
	assert.ErrorIs(t, err, ErrNoJobs)
}

func TestRecoverStale(t *testing.T) {
	pool := repotest.Pool(t)
	ctx := context.Background()

	now := time.Now()
	q := New(pool)
	q.now = func() time.Time { return now }

	_, err := q.Enqueue(ctx, nil, "ingest", nil, 2)
	require.NoError(t, err)
	_, err = q.Claim(ctx, "dead-worker", []string{"ingest"})
	require.NoError(t, err)

	n, err := q.RecoverStale(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "fresh lock is kept")

	now = now.Add(q.Lease + time.Minute)
	n, err = q.RecoverStale(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	job, err := q.Claim(ctx, "w2", []string{"ingest"})
	require.NoError(t, err)
	assert.Equal(t, 2, job.Attempts)
	assert.Equal(t, "worker lock expired", job.LastError)

	// A second crash exhausts the budget.
	now = now.Add(q.Lease + time.Minute)
	n, err = q.RecoverStale(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	_, err = q.Claim(ctx, "w3", []string{"ingest"})
	assert.ErrorIs(t, err, ErrNoJobs, "marked failed")
}

func TestWorkerProcessesJobs(t *testing.T) {
	pool := repotest.Pool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	q := New(pool)
	var done atomic.Int32
	finished := make(chan struct{}, 10)

	w := NewWorker(q, slog.New(slog.DiscardHandler), "test", []string{"ingest"}, func(_ context.Context, j *Job) error {
		done.Add(1)
		finished <- struct{}{}
		if string(j.Payload) == `"fail"` {
			return Permanent(errors.New("nope"))
		}
		return nil
	})
	w.PollInterval = time.Hour // rely on notifications

	runErr := make(chan error, 1)
	go func() { runErr <- w.Run(ctx, 2) }()

	for _, p := range []string{"a", "b", "fail"} {
		_, err := q.Enqueue(ctx, nil, "ingest", p, 1)
		require.NoError(t, err)
	}

	for range 3 {
		select {
		case <-finished:
		case <-ctx.Done():
			t.Fatal("worker did not process jobs in time")
		}
	}

	// Give the worker a moment to record outcomes, then check the table.
	require.Eventually(t, func() bool {
		var doneCount, failedCount int
		_ = pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'done'), count(*) FILTER (WHERE status = 'failed') FROM jobs`).Scan(&doneCount, &failedCount)
		return doneCount == 2 && failedCount == 1
	}, 5*time.Second, 50*time.Millisecond)

	cancel()
	assert.ErrorIs(t, <-runErr, context.Canceled)
	assert.EqualValues(t, 3, done.Load())
}

// The regression from #120: attempt A outlives its lease, the job is
// recovered and claimed by B, and then A tries to act.
func TestStaleAttemptIsFenced(t *testing.T) {
	pool := repotest.Pool(t)
	ctx := context.Background()

	now := time.Now()
	q := New(pool)
	q.now = func() time.Time { return now }

	id, err := q.Enqueue(ctx, nil, "ingest", nil, 3)
	require.NoError(t, err)
	a, err := q.Claim(ctx, "w1", []string{"ingest"})
	require.NoError(t, err)

	now = now.Add(q.Lease + time.Second)
	n, err := q.RecoverStale(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	b, err := q.Claim(ctx, "w1", []string{"ingest"}) // the same worker's other slot
	require.NoError(t, err)
	require.Equal(t, id, b.ID)
	require.NotEqual(t, a.Lease, b.Lease)

	// A can neither renew, publish, fail nor complete B's job.
	assert.ErrorIs(t, q.Renew(ctx, a), ErrLeaseLost)
	ran := false
	assert.ErrorIs(t, q.Fenced(ctx, a, func(pgx.Tx) error { ran = true; return nil }), ErrLeaseLost)
	assert.False(t, ran, "a stale attempt's fenced write never runs")
	_, err = q.Fail(ctx, a, errors.New("late failure"))
	assert.ErrorIs(t, err, ErrLeaseLost)
	assert.ErrorIs(t, q.Complete(ctx, a), ErrLeaseLost)

	var status string
	var lastError *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status, last_error FROM jobs WHERE id = $1`, id).Scan(&status, &lastError))
	assert.Equal(t, "running", status, "B still runs it")
	assert.Equal(t, "worker lock expired", *lastError, "A's late failure was not recorded")

	// B owns it: renews, publishes inside the lock, completes.
	require.NoError(t, q.Renew(ctx, b))
	require.NoError(t, q.Fenced(ctx, b, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE jobs SET payload = '"published"' WHERE id = $1`, id)
		return err
	}))
	require.NoError(t, q.Complete(ctx, b))
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id = $1`, id).Scan(&status))
	assert.Equal(t, "done", status)
}

func TestFencedRollsBackOnError(t *testing.T) {
	pool := repotest.Pool(t)
	ctx := context.Background()
	q := New(pool)

	id, err := q.Enqueue(ctx, nil, "ingest", "before", 1)
	require.NoError(t, err)
	j, err := q.Claim(ctx, "w1", []string{"ingest"})
	require.NoError(t, err)

	boom := errors.New("boom")
	err = q.Fenced(ctx, j, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE jobs SET payload = '"after"' WHERE id = $1`, id)
		require.NoError(t, err)
		return boom
	})
	assert.ErrorIs(t, err, boom)
	var payload string
	require.NoError(t, pool.QueryRow(ctx, `SELECT payload #>> '{}' FROM jobs WHERE id = $1`, id).Scan(&payload))
	assert.Equal(t, "before", payload)
}

// A renewed lease is never recovered; an abandoned one is.
func TestRenewKeepsTheLease(t *testing.T) {
	pool := repotest.Pool(t)
	ctx := context.Background()

	now := time.Now()
	q := New(pool)
	q.now = func() time.Time { return now }

	_, err := q.Enqueue(ctx, nil, "ingest", nil, 3)
	require.NoError(t, err)
	j, err := q.Claim(ctx, "w1", []string{"ingest"})
	require.NoError(t, err)

	for range 5 { // well past one lease in total
		now = now.Add(q.Lease * 4 / 5)
		require.NoError(t, q.Renew(ctx, j))
		n, err := q.RecoverStale(ctx)
		require.NoError(t, err)
		require.Zero(t, n)
	}

	now = now.Add(q.Lease + time.Second)
	n, err := q.RecoverStale(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	assert.ErrorIs(t, q.Renew(ctx, j), ErrLeaseLost)
}

// A healthy job running for several leases keeps its one attempt, even
// with recovery running all along.
func TestWorkerRenewsALongJob(t *testing.T) {
	pool := repotest.Pool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	q := New(pool)
	q.Lease = 400 * time.Millisecond
	var interrupted atomic.Bool
	finished := make(chan struct{}, 1)
	w := NewWorker(q, slog.New(slog.DiscardHandler), "test", []string{"ingest"}, func(ctx context.Context, _ *Job) error {
		select {
		case <-time.After(4 * q.Lease):
		case <-ctx.Done():
			interrupted.Store(true)
		}
		finished <- struct{}{}
		return nil
	})
	w.PollInterval = 50 * time.Millisecond // recovery runs constantly
	runErr := make(chan error, 1)
	go func() { runErr <- w.Run(ctx, 2) }()

	id, err := q.Enqueue(ctx, nil, "ingest", nil, 3)
	require.NoError(t, err)
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("job did not finish")
	}
	assert.False(t, interrupted.Load(), "a renewed job is not cancelled")
	require.Eventually(t, func() bool {
		var status string
		var attempts int
		_ = pool.QueryRow(ctx, `SELECT status, attempts FROM jobs WHERE id = $1`, id).Scan(&status, &attempts)
		return status == "done" && attempts == 1
	}, 5*time.Second, 20*time.Millisecond)

	cancel()
	<-runErr
}

// When the job is taken away mid-run, the handler is cancelled with
// ErrLeaseLost and the worker records nothing for that attempt.
func TestWorkerDropsALostJob(t *testing.T) {
	pool := repotest.Pool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	q := New(pool)
	q.Lease = 300 * time.Millisecond
	started := make(chan struct{}, 1)
	cause := make(chan error, 1)
	w := NewWorker(q, slog.New(slog.DiscardHandler), "test", []string{"ingest"}, func(ctx context.Context, _ *Job) error {
		started <- struct{}{}
		<-ctx.Done()
		cause <- context.Cause(ctx)
		return errors.New("interrupted")
	})
	w.PollInterval = time.Hour
	runErr := make(chan error, 1)
	go func() { runErr <- w.Run(ctx, 1) }()

	id, err := q.Enqueue(ctx, nil, "ingest", nil, 3)
	require.NoError(t, err)
	<-started
	// As if recovery had run and another attempt had claimed it.
	_, err = pool.Exec(ctx, `UPDATE jobs SET lease = gen_random_uuid(), locked_at = now() WHERE id = $1`, id)
	require.NoError(t, err)

	select {
	case err := <-cause:
		assert.ErrorIs(t, err, ErrLeaseLost)
	case <-ctx.Done():
		t.Fatal("handler was not cancelled")
	}
	cancel()
	<-runErr

	var status string
	var lastError *string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT status, last_error FROM jobs WHERE id = $1`, id).Scan(&status, &lastError))
	assert.Equal(t, "running", status, "the other attempt's job is left alone")
	assert.Nil(t, lastError, "the lost attempt's error is not recorded")
}

// Without the database an attempt cannot know whether it still owns the
// job; it stops before recovery could hand the job to someone else.
func TestKeepLeaseStopsWhenRenewalsFail(t *testing.T) {
	shared := repotest.Pool(t)
	pool, err := pgxpool.New(context.Background(), shared.Config().ConnString())
	require.NoError(t, err)
	pool.Close() // every renewal fails

	q := New(pool)
	q.Lease = 250 * time.Millisecond
	w := &Worker{queue: q}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	start := time.Now()
	w.keepLease(ctx, &Job{ID: uuid.New(), Lease: uuid.New()}, cancel, slog.New(slog.DiscardHandler))
	assert.ErrorIs(t, context.Cause(ctx), ErrLeaseLost)
	assert.GreaterOrEqual(t, time.Since(start), q.Lease*3/5)
	assert.Less(t, time.Since(start), q.Lease, "stops before the lease could be recovered")
}

// terminateListener drops the server side of the connection listening on
// channel, as a network blip or a database restart would.
func terminateListener(t *testing.T, pool *pgxpool.Pool, channel string) {
	t.Helper()
	require.Eventually(t, func() bool {
		var n int
		err := pool.QueryRow(context.Background(), `
			SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity
			WHERE datname = current_database()
			  AND application_name = current_setting('application_name')
			  AND query = 'LISTEN "' || $1 || '"'`, channel).Scan(&n)
		return err == nil && n > 0
	}, 5*time.Second, 20*time.Millisecond)
}

func TestListenCatchesUpAfterReconnect(t *testing.T) {
	pool := repotest.Pool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var connects atomic.Int32
	got := make(chan string, 10)
	done := make(chan error, 1)
	go func() {
		done <- Listen(ctx, pool, "listen-test", slog.New(slog.DiscardHandler),
			func(p string) { got <- p }, func() { connects.Add(1) })
	}()
	require.Eventually(t, func() bool { return connects.Load() == 1 }, 5*time.Second, 10*time.Millisecond)

	terminateListener(t, pool, "listen-test")
	require.Eventually(t, func() bool { return connects.Load() == 2 }, 10*time.Second, 20*time.Millisecond,
		"connected runs again after the reconnect")

	_, err := pool.Exec(ctx, `SELECT pg_notify('listen-test', 'after')`)
	require.NoError(t, err)
	select {
	case p := <-got:
		assert.Equal(t, "after", p)
	case <-ctx.Done():
		t.Fatal("no notification after the reconnect")
	}
	cancel()
	<-done
}

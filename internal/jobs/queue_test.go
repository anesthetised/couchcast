package jobs

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

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
	require.NoError(t, q.Complete(ctx, job.ID))
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

	now = now.Add(StaleAfter + time.Minute)
	n, err = q.RecoverStale(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	job, err := q.Claim(ctx, "w2", []string{"ingest"})
	require.NoError(t, err)
	assert.Equal(t, 2, job.Attempts)
	assert.Equal(t, "worker lock expired", job.LastError)

	// A second crash exhausts the budget.
	now = now.Add(StaleAfter + time.Minute)
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

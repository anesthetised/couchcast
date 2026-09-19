package jobs

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"
)

// Handler processes one job. Return nil to complete it, an error to retry
// (or Permanent(err) to fail immediately).
type Handler func(ctx context.Context, job *Job) error

// Worker pulls jobs of the given kinds with a fixed concurrency.
type Worker struct {
	queue   *Queue
	logger  *slog.Logger
	name    string
	kinds   []string
	handler Handler

	// PollInterval is the fallback when no notification arrives.
	PollInterval time.Duration
}

// NewWorker creates a worker; name identifies it in locked_by.
func NewWorker(queue *Queue, logger *slog.Logger, name string, kinds []string, handler Handler) *Worker {
	return &Worker{queue: queue, logger: logger, name: name, kinds: kinds, handler: handler, PollInterval: 10 * time.Second}
}

// Run blocks until ctx is cancelled, running up to concurrency jobs at a
// time. Each in-flight job finishes (or is interrupted by ctx) before Run
// returns.
func (w *Worker) Run(ctx context.Context, concurrency int) error {
	wake := make(chan struct{}, 1)
	kick := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return Listen(ctx, w.queue.pool, Channel, w.logger, func(string) { kick() })
	})

	g.Go(func() error {
		t := time.NewTicker(w.PollInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				if n, err := w.queue.RecoverStale(ctx); err != nil {
					w.logger.Warn("recover stale jobs", "error", err)
				} else if n > 0 {
					w.logger.Info("recovered stale jobs", "count", n)
				}
				kick()
			}
		}
	})

	// Slots gate concurrency; every slot drains the queue when woken.
	for i := range concurrency {
		slot := i
		g.Go(func() error {
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-wake:
				}
				w.drain(ctx, slot)
				// Another slot may still have work: re-arm for the others.
				kick()
				if ctx.Err() != nil {
					return ctx.Err()
				}
				// Avoid a hot loop when the queue is empty.
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(200 * time.Millisecond):
				}
			}
		})
	}

	kick()
	return g.Wait()
}

// drain runs jobs until Claim finds nothing.
func (w *Worker) drain(ctx context.Context, slot int) {
	for ctx.Err() == nil {
		job, err := w.queue.Claim(ctx, w.name, w.kinds)
		if err != nil {
			if ctx.Err() == nil && err != ErrNoJobs { //nolint:errorlint // sentinel
				w.logger.Error("claim job", "error", err)
			}
			return
		}

		log := w.logger.With("job", job.ID, "kind", job.Kind, "attempt", job.Attempts, "slot", slot)
		log.Info("job started")
		start := time.Now()

		if err := w.handler(ctx, job); err != nil {
			retry, ferr := w.queue.Fail(context.WithoutCancel(ctx), job, err)
			if ferr != nil {
				log.Error("record job failure", "error", ferr)
			}
			log.Warn("job failed", "error", err, "retry", retry, "duration", time.Since(start))
			continue
		}

		if err := w.queue.Complete(context.WithoutCancel(ctx), job.ID); err != nil {
			log.Error("complete job", "error", err)
		}
		log.Info("job done", "duration", time.Since(start))
	}
}

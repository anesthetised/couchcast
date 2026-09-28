package jobs

import (
	"context"
	"errors"
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
		// A reconnect may have missed wake-ups: look at the queue again.
		return Listen(ctx, w.queue.pool, Channel, w.logger, func(string) { kick() }, kick)
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
		w.run(ctx, job, slot)
	}
}

// run executes one attempt while keeping its lease, then records the
// outcome unless the attempt lost the job meanwhile.
func (w *Worker) run(ctx context.Context, job *Job, slot int) {
	log := w.logger.With("job", job.ID, "kind", job.Kind, "attempt", job.Attempts, "slot", slot)
	log.Info("job started")
	start := time.Now()

	hctx, cancel := context.WithCancelCause(ctx)
	kept := make(chan struct{})
	go func() {
		defer close(kept)
		w.keepLease(hctx, job, cancel, log)
	}()
	err := w.handler(hctx, job)
	lost := errors.Is(context.Cause(hctx), ErrLeaseLost)
	cancel(nil)
	<-kept

	// Another attempt may own the job now: its outcome is not ours to write.
	if lost {
		log.Warn("job lost its lease; the result is dropped", "error", err, "duration", time.Since(start))
		return
	}
	if err != nil {
		retry, ferr := w.queue.Fail(context.WithoutCancel(ctx), job, err)
		if ferr != nil {
			log.Error("record job failure", "error", ferr)
		}
		log.Warn("job failed", "error", err, "retry", retry, "duration", time.Since(start))
		return
	}
	if err := w.queue.Complete(context.WithoutCancel(ctx), job); err != nil {
		log.Error("complete job", "error", err)
	}
	log.Info("job done", "duration", time.Since(start))
}

// keepLease renews the attempt's lease every fifth of its length until ctx
// ends. It cancels the handler with ErrLeaseLost when the job was taken
// away, or when no renewal succeeded for three fifths of the lease (the
// database is unreachable): stopping then leaves a margin before recovery
// could hand the job to another attempt.
func (w *Worker) keepLease(ctx context.Context, job *Job, cancel context.CancelCauseFunc, log *slog.Logger) {
	lease := w.queue.Lease
	t := time.NewTicker(lease / 5)
	defer t.Stop()
	renewed := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		err := w.queue.Renew(ctx, job)
		switch {
		case err == nil:
			renewed = time.Now()
		case errors.Is(err, ErrLeaseLost):
			log.Warn("job lease lost")
			cancel(ErrLeaseLost)
			return
		case ctx.Err() != nil:
			return
		default:
			log.Warn("renew job lease", "error", err)
			if time.Since(renewed) >= lease*3/5 {
				cancel(ErrLeaseLost)
				return
			}
		}
	}
}

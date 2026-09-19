package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	tkhttp "github.com/anesthetised/toolkit/net/http"
	"golang.org/x/sync/errgroup"

	"github.com/anesthetised/couchcast/internal/config"
	"github.com/anesthetised/couchcast/internal/metrics"
)

// ingest runs the worker process. The job loop itself lands in phase 4;
// for now the process connects, exposes metrics and waits for shutdown so
// the deployment shape is exercised from day one.
func ingest(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if err := errors.Join(cfg.Database.Validate(), cfg.S3.Validate(), cfg.Ingest.Validate()); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	pool, err := connectDB(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	m := metrics.New("ingest")

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", m.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{
		Addr:              cfg.Ingest.MetricsAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		logger.Info("ingest metrics listening", "addr", cfg.Ingest.MetricsAddr)
		return tkhttp.ListenAndServe(ctx, srv)
	})

	g.Go(func() error {
		logger.Info("ingest worker idle: job queue arrives in phase 4", "workers", cfg.Ingest.Workers)
		<-ctx.Done()
		return ctx.Err()
	})

	return g.Wait()
}

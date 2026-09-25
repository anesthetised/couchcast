package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	tkhttp "github.com/anesthetised/toolkit/net/http"
	"golang.org/x/sync/errgroup"

	"github.com/anesthetised/couchcast/internal/config"
	"github.com/anesthetised/couchcast/internal/ingest"
	"github.com/anesthetised/couchcast/internal/jobs"
	"github.com/anesthetised/couchcast/internal/mediastore"
	"github.com/anesthetised/couchcast/internal/metrics"
	"github.com/anesthetised/couchcast/internal/packager"
	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/source/ytdlp"
)

// ingestCmd runs the worker process. It talks to the web server only
// through PostgreSQL (job queue, progress notifications) and S3, so it can
// run on a different machine.
func ingestCmd(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if err := errors.Join(cfg.Database.Validate(), cfg.S3.Validate(), cfg.Ingest.Validate()); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	pool, err := connectDB(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	store, err := connectStore(ctx, cfg.S3, logger)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(cfg.Ingest.WorkDir, 0o750); err != nil {
		return fmt.Errorf("work dir: %w", err)
	}

	m := metrics.New("ingest")
	repo := repository.New(pool)
	queue := jobs.New(pool)
	extractor := ytdlp.New(cfg.Ingest.YTDLPPath, cfg.Ingest.YTDLPExtraArgs, logger)
	pkg := packager.New(cfg.Ingest.FFmpegPath, cfg.Ingest.SegmentSeconds)

	pipeline := ingest.NewWorker(repo, queue, extractor, pkg, store, cfg.Ingest.WorkDir, cfg.Ingest.QualityLadder, logger, m,
		ingest.SourcePolicy{AllowPrivate: cfg.Ingest.AllowPrivateSources})
	worker := jobs.NewWorker(queue, logger, workerName(), []string{ingest.JobKind}, pipeline.Handle)

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", m.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Addr: cfg.Ingest.MetricsAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		logger.Info("ingest metrics listening", "addr", cfg.Ingest.MetricsAddr)
		return tkhttp.ListenAndServe(ctx, srv)
	})

	g.Go(func() error {
		logger.Info("ingest worker running", "workers", cfg.Ingest.Workers, "ladder", cfg.Ingest.QualityLadder)
		return worker.Run(ctx, cfg.Ingest.Workers)
	})

	return g.Wait()
}

// connectStore opens the S3 client and makes sure the bucket exists.
func connectStore(ctx context.Context, cfg config.S3Config, logger *slog.Logger) (*mediastore.Store, error) {
	store, err := mediastore.New(cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := store.EnsureBucket(pingCtx); err != nil {
		return nil, err
	}
	logger.Info("object storage connected", "endpoint", cfg.Endpoint, "bucket", cfg.Bucket)
	return store, nil
}

func workerName() string {
	host, err := os.Hostname()
	if err != nil {
		host = "ingest"
	}
	return host + "/" + strconv.Itoa(os.Getpid())
}

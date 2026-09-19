package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	tkhttp "github.com/anesthetised/toolkit/net/http"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/anesthetised/couchcast/internal/apihttp"
	"github.com/anesthetised/couchcast/internal/config"
	"github.com/anesthetised/couchcast/internal/metrics"
	"github.com/anesthetised/couchcast/web"
)

func serve(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if err := errors.Join(cfg.Database.Validate(), cfg.S3.Validate(), cfg.Web.Validate()); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	pool, err := connectDB(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer pool.Close()

	m := metrics.New("web")

	apihttp.SetVersion(version)
	api := apihttp.New(apihttp.Deps{
		Logger:  logger,
		DB:      pool,
		Metrics: m,
		Static:  web.Dist(),
	})

	srv := &http.Server{
		Addr:              cfg.Web.Addr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: media proxying and WebSockets are long-lived.
	}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		logger.Info("web server listening", "addr", cfg.Web.Addr)
		return tkhttp.ListenAndServe(ctx, srv)
	})

	return g.Wait()
}

// connectDB opens the pool and verifies the connection so that a bad DSN
// fails fast at startup rather than on the first request.
func connectDB(ctx context.Context, cfg config.DatabaseConfig, logger *slog.Logger) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database ping: %w", err)
	}

	logger.Info("database connected")

	return pool, nil
}

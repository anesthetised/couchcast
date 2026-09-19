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
	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/config"
	"github.com/anesthetised/couchcast/internal/mediastore"
	"github.com/anesthetised/couchcast/internal/metrics"
	"github.com/anesthetised/couchcast/internal/ratelimit"
	"github.com/anesthetised/couchcast/internal/repository"
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

	store, err := connectStore(ctx, cfg.S3, logger)
	if err != nil {
		return err
	}

	m := metrics.New("web")
	repo := repository.New(pool)
	signer := mediastore.NewSigner(cfg.Web.MediaTokenSecret, cfg.Web.MediaTokenTTL)
	mediaHandler := mediastore.NewHandler(store, signer, repo, logger, m.MediaProxied)
	sessions := auth.NewSessions(repo, cfg.Web.SessionTTL, cfg.Web.SecureCookies)

	authLimiter := ratelimit.New(float64(cfg.Web.AuthRatePerMinute), cfg.Web.AuthRatePerMinute)
	loginLimiter := ratelimit.New(5, 5)

	apihttp.SetVersion(version)
	api := apihttp.New(apihttp.Deps{
		Logger:       logger,
		DB:           pool,
		Metrics:      m,
		Static:       web.Dist(),
		Users:        repo,
		Rooms:        repo,
		Sessions:     sessions,
		Media:        mediaHandler,
		AuthLimiter:  authLimiter,
		LoginLimiter: loginLimiter,
		TrustProxy:   cfg.Web.TrustProxy,
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

	g.Go(func() error {
		authLimiter.Run(ctx.Done())
		return nil
	})
	g.Go(func() error {
		loginLimiter.Run(ctx.Done())
		return nil
	})
	g.Go(func() error {
		return runPeriodic(ctx, time.Hour, func(ctx context.Context) {
			n, err := repo.DeleteExpiredSessions(ctx, time.Now())
			if err != nil {
				logger.Warn("delete expired sessions", "error", err)
			} else if n > 0 {
				logger.Info("deleted expired sessions", "count", n)
			}
		})
	})

	return g.Wait()
}

// runPeriodic calls fn every interval until the context is cancelled.
func runPeriodic(ctx context.Context, interval time.Duration, fn func(context.Context)) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			fn(ctx)
		}
	}
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

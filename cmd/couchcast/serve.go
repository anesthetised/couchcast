package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"uuid"

	tkhttp "github.com/anesthetised/toolkit/net/http"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/apihttp"
	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/config"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/hub"
	"github.com/anesthetised/couchcast/internal/ingest"
	"github.com/anesthetised/couchcast/internal/jobs"
	"github.com/anesthetised/couchcast/internal/mediastore"
	"github.com/anesthetised/couchcast/internal/metrics"
	"github.com/anesthetised/couchcast/internal/ratelimit"
	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/room"
	"github.com/anesthetised/couchcast/internal/source/ytdlp"
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
	roomCreateLimiter := ratelimit.PerHour(10, 10)
	inviteLimiter := ratelimit.PerHour(30, 30)
	reportLimiter := ratelimit.PerHour(10, 10)
	queueAddLimiter := ratelimit.New(10, 5)
	probeLimiter := ratelimit.New(20, 10)

	queue := jobs.New(pool)
	admit := ingest.NewService(repo, queue, ytdlp.New(cfg.Ingest.YTDLPPath, cfg.Ingest.YTDLPExtraArgs, logger))
	rooms := room.NewManager(room.Deps{Store: repo, Chat: repo, Admit: admit, Signer: signer, Logger: logger, QueueAddLimiter: queueAddLimiter})
	m.RegisterRoomsLoaded(func() float64 { return float64(rooms.Loaded()) })

	apihttp.SetVersion(version)
	var api *apihttp.Server
	wsHub := hub.New(rooms, func(ctx context.Context, rm *entity.Room, u *entity.User) (access.Actor, error) {
		return api.ActorFor(ctx, rm, u)
	}, logger, m)

	api = apihttp.New(apihttp.Deps{
		Logger:       logger,
		DB:           pool,
		Metrics:      m,
		Static:       web.Dist(),
		Users:        repo,
		Rooms:        repo,
		Admin:        repo,
		Sessions:     sessions,
		Directory:    repo,
		Live:         rooms,
		Signer:       signer,
		Admit:        admit,
		LiveQueue:    liveQueue{rooms},
		Prober:       admit,
		InviteLinks:  repo,
		Meta:         repo,
		Mutes:        repo,
		Stars:        repo,
		Media:        mediaHandler,
		MediaObjects: store,
		CacheBudget:  cfg.Web.MaxCacheBytes,
		RoomsLoaded:  rooms.Loaded,
		WS:           wsHub,
		OnBan:        func(roomID, userID uuid.UUID) { rooms.Kick(roomID, userID, "removed from room") },
		OnLeave:      func(roomID, userID uuid.UUID) { rooms.Kick(roomID, userID, "left") },
		OnMute:       func(roomID, _ uuid.UUID, line string) { rooms.Log(ctx, roomID, line) },
		OnRoomChanged: func(roomID uuid.UUID) {
			rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			rooms.Refresh(rctx, roomID)
		},
		OnRoomDeleted: func(roomID uuid.UUID) { rooms.Unload(roomID, "room deleted") },
		OnUserBanned:  func(userID uuid.UUID) { rooms.KickEverywhere(userID, "banned") },
		OnMediaDeleted: func(mediaID uuid.UUID) {
			rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			rooms.MediaDeleted(rctx, mediaID)
		},
		AuthLimiter:       authLimiter,
		LoginLimiter:      loginLimiter,
		TrustProxy:        cfg.Web.TrustProxy,
		RoomCreateLimiter: roomCreateLimiter,
		InviteLimiter:     inviteLimiter,
		ReportLimiter:     reportLimiter,
		ProbeLimiter:      probeLimiter,
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

	evictor := mediastore.NewEvictor(store, repo, cfg.Web.MaxCacheBytes, logger)
	g.Go(func() error {
		return runPeriodic(ctx, 30*time.Minute, func(ctx context.Context) {
			if n, err := evictor.Run(ctx); err != nil {
				logger.Warn("media eviction", "error", err)
			} else if n > 0 {
				logger.Info("media eviction pass", "removed", n)
			}
		})
	})

	if err := rooms.Warm(ctx); err != nil {
		logger.Warn("warm playing rooms", "error", err)
	}
	g.Go(func() error { return rooms.Run(ctx) })
	g.Go(func() error { return rooms.ListenProgress(ctx, pool) })

	g.Go(func() error {
		authLimiter.Run(ctx.Done())
		return nil
	})
	g.Go(func() error {
		loginLimiter.Run(ctx.Done())
		return nil
	})
	for _, l := range []*ratelimit.Limiter{roomCreateLimiter, inviteLimiter, reportLimiter, queueAddLimiter} {
		g.Go(func() error {
			l.Run(ctx.Done())
			return nil
		})
	}
	g.Go(func() error {
		return runPeriodic(ctx, time.Hour, func(ctx context.Context) {
			n, err := repo.DeleteExpiredSessions(ctx, time.Now())
			if err != nil {
				logger.Warn("delete expired sessions", "error", err)
			} else if n > 0 {
				logger.Info("deleted expired sessions", "count", n)
			}
			if n, err := repo.PurgePlayed(ctx, time.Now().AddDate(0, 0, -playedRetentionDays), playedKeptPerRoom); err != nil {
				logger.Warn("purge played items", "error", err)
			} else if n > 0 {
				logger.Info("purged played items", "count", n)
			}
			if cfg.Web.ChatRetentionDays > 0 {
				cutoff := time.Now().AddDate(0, 0, -cfg.Web.ChatRetentionDays)
				n, err := repo.PurgeMessagesBefore(ctx, cutoff)
				if err != nil {
					logger.Warn("purge old messages", "error", err)
				} else if n > 0 {
					logger.Info("purged old messages", "count", n)
				}
			}
		})
	})

	return g.Wait()
}

// runPeriodic calls fn every interval until the context is cancelled.
// Played queue items are history: keep a week and at most this many per
// room so the table does not grow with every session.
const (
	playedRetentionDays = 7
	playedKeptPerRoom   = 50
)

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

// liveQueue adapts the room manager to apihttp.LiveQueue.
type liveQueue struct{ rooms *room.Manager }

func (q liveQueue) QueueAdd(ctx context.Context, roomID uuid.UUID, actor access.Actor, rawURL string) error {
	r, err := q.rooms.Get(ctx, roomID)
	if err != nil {
		return err
	}
	return r.QueueAdd(ctx, actor, rawURL, false, true)
}

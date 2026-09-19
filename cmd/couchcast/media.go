package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/config"
	"github.com/anesthetised/couchcast/internal/ingest"
	"github.com/anesthetised/couchcast/internal/jobs"
	"github.com/anesthetised/couchcast/internal/mediastore"
	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/source/ytdlp"
)

// mediaCmd hosts developer utilities for exercising the pipeline without
// a room: enqueue a URL and mint a token to open the manifest.
func mediaCmd(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string) error {
	if len(args) < 1 {
		return errors.New("media: expected subcommand: enqueue <url> | retry <media-id> | show <media-id> | token <media-id>")
	}

	if err := cfg.Database.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	pool, err := connectDB(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer pool.Close()
	repo := repository.New(pool)

	switch args[0] {
	case "enqueue":
		if len(args) != 2 {
			return errors.New("media enqueue: expected exactly one url")
		}
		svc := ingest.NewService(repo, jobs.New(pool), ytdlp.New(cfg.Ingest.YTDLPPath, nil, logger))
		media, err := svc.EnsureMedia(ctx, pool, args[1])
		if err != nil {
			return err
		}
		fmt.Printf("media %s\tstatus=%s\tsource=%s\n", media.ID, media.Status, media.SourceKey)
		return nil

	case "retry":
		if len(args) != 2 {
			return errors.New("media retry: expected exactly one media id")
		}
		id, err := uuid.Parse(args[1])
		if err != nil {
			return err
		}
		media, err := repo.GetMedia(ctx, id)
		if err != nil {
			return err
		}
		svc := ingest.NewService(repo, jobs.New(pool), ytdlp.New(cfg.Ingest.YTDLPPath, nil, logger))
		if err := svc.Retry(ctx, media); err != nil {
			return err
		}
		fmt.Printf("media %s re-queued\n", media.ID)
		return nil

	case "show":
		if len(args) != 2 {
			return errors.New("media show: expected exactly one media id")
		}
		id, err := uuid.Parse(args[1])
		if err != nil {
			return err
		}
		media, err := repo.GetMedia(ctx, id)
		if err != nil {
			return err
		}
		fmt.Printf("id=%s\nstatus=%s progress=%.2f\ntitle=%s\nduration=%s\nerror=%s\nsize=%d\nrenditions=%+v\n",
			media.ID, media.Status, media.Progress, media.Title, time.Duration(media.DurationMs)*time.Millisecond,
			media.Error, media.SizeBytes, media.Renditions)
		return nil

	case "token":
		if len(args) != 2 {
			return errors.New("media token: expected exactly one media id")
		}
		if err := cfg.Web.Validate(); err != nil {
			return fmt.Errorf("config: %w", err)
		}
		id, err := uuid.Parse(args[1])
		if err != nil {
			return err
		}
		signer := mediastore.NewSigner(cfg.Web.MediaTokenSecret, cfg.Web.MediaTokenTTL)
		fmt.Printf("/media/%s/manifest.mpd?t=%s\n", id, signer.Sign(id, time.Now()))
		return nil

	default:
		return fmt.Errorf("media: unknown subcommand %q", args[0])
	}
}

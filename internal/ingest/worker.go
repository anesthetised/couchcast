package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/jobs"
	"github.com/anesthetised/couchcast/internal/mediastore"
	"github.com/anesthetised/couchcast/internal/metrics"
	"github.com/anesthetised/couchcast/internal/packager"
	"github.com/anesthetised/couchcast/internal/source"
)

// Worker runs the pipeline for one job at a time per slot.
type Worker struct {
	repo      MediaRepo
	queue     *jobs.Queue
	extractor source.Extractor
	packager  *packager.Packager
	store     *mediastore.Store
	workDir   string
	ladder    []int
	logger    *slog.Logger
	metrics   *metrics.Metrics
}

// NewWorker wires the pipeline. metrics may be nil.
func NewWorker(repo MediaRepo, queue *jobs.Queue, extractor source.Extractor, pkg *packager.Packager,
	store *mediastore.Store, workDir string, ladder []int, logger *slog.Logger, m *metrics.Metrics) *Worker {
	return &Worker{repo: repo, queue: queue, extractor: extractor, packager: pkg, store: store,
		workDir: workDir, ladder: ladder, logger: logger, metrics: m}
}

// Handle implements jobs.Handler.
func (w *Worker) Handle(ctx context.Context, job *jobs.Job) error {
	var payload Payload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return jobs.Permanent(fmt.Errorf("decode payload: %w", err))
	}

	media, err := w.repo.GetMedia(ctx, payload.MediaID)
	if err != nil {
		return jobs.Permanent(fmt.Errorf("load media %s: %w", payload.MediaID, err))
	}
	if media.IsReady() {
		return nil
	}

	log := w.logger.With("media", media.ID, "source", media.SourceKey)

	err = w.process(ctx, media, log)
	if err != nil {
		msg := err.Error()
		if len(msg) > 500 {
			msg = msg[:500]
		}
		if ferr := w.repo.SetMediaFailed(context.WithoutCancel(ctx), media.ID, msg); ferr != nil {
			log.Error("mark media failed", "error", ferr)
		}
		w.notify(context.WithoutCancel(ctx), media.ID)
		w.metrics.IngestJob("failed")
		return err
	}

	w.metrics.IngestJob("done")
	return nil
}

func (w *Worker) process(ctx context.Context, media *entity.Media, log *slog.Logger) error {
	dir := filepath.Join(w.workDir, media.ID.String())
	srcDir, outDir := filepath.Join(dir, "src"), filepath.Join(dir, "dash")
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(srcDir, 0o750); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	// --- probe ---------------------------------------------------------------
	if err := w.setStatus(ctx, media.ID, entity.MediaProbing); err != nil {
		return err
	}
	start := time.Now()
	probe, err := w.extractor.Probe(ctx, media.SourceURL)
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	sel, err := source.SelectFormats(probe, w.ladder)
	if err != nil {
		return jobs.Permanent(err)
	}
	if err := w.repo.SetMediaProbed(ctx, media.ID, probe.Title, probe.DurationMs, probe.ThumbnailURL); err != nil {
		return err
	}
	w.metrics.IngestStep("probe", time.Since(start))
	log.Info("probed", "title", probe.Title, "duration_ms", probe.DurationMs, "formats", sel.IDs())

	// --- download ------------------------------------------------------------
	if err := w.setStatus(ctx, media.ID, entity.MediaDownloading); err != nil {
		return err
	}
	start = time.Now()
	reporter := &progressReporter{repo: w.repo, queue: w.queue, mediaID: media.ID, interval: time.Second, now: time.Now}
	files, err := w.extractor.Download(ctx, media.SourceURL, sel.IDs(), srcDir, func(v float64) {
		reporter.report(ctx, v, false)
	})
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	reporter.report(ctx, 1, true)
	w.metrics.IngestStep("download", time.Since(start))

	// --- package -------------------------------------------------------------
	if err := w.setStatus(ctx, media.ID, entity.MediaPackaging); err != nil {
		return err
	}
	start = time.Now()
	inputs := make([]packager.Input, 0, len(sel.Video)+1)
	renditions := make([]entity.Rendition, 0, len(sel.Video))
	for i, v := range sel.Video {
		inputs = append(inputs, packager.Input{Path: files[v.ID], Width: v.Width, Height: v.Height})
		renditions = append(renditions, entity.Rendition{
			ID: strconv.Itoa(i), Height: v.Height, Width: v.Width, Codec: v.CodecFamily(), Bitrate: v.Bitrate,
		})
	}
	inputs = append(inputs, packager.Input{Path: files[sel.Audio.ID], Audio: true})

	if err := w.packager.Run(ctx, inputs, outDir); err != nil {
		return jobs.Permanent(fmt.Errorf("package: %w", err))
	}
	_ = os.RemoveAll(srcDir)
	w.metrics.IngestStep("package", time.Since(start))

	// --- upload --------------------------------------------------------------
	if err := w.setStatus(ctx, media.ID, entity.MediaUploading); err != nil {
		return err
	}
	start = time.Now()
	prefix := mediastore.Prefix(media.ID.String())
	size, err := w.store.UploadDir(ctx, prefix, outDir)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	w.metrics.IngestStep("upload", time.Since(start))

	if err := w.repo.SetMediaReady(ctx, media.ID, renditions, size, prefix); err != nil {
		return err
	}
	w.notify(ctx, media.ID)
	log.Info("media ready", "bytes", size, "renditions", len(renditions))

	return nil
}

func (w *Worker) setStatus(ctx context.Context, id uuid.UUID, status entity.MediaStatus) error {
	if err := w.repo.SetMediaStatus(ctx, id, status); err != nil {
		return err
	}
	w.notify(ctx, id)
	return nil
}

func (w *Worker) notify(ctx context.Context, id uuid.UUID) {
	if err := w.queue.Notify(ctx, ProgressChannel, id.String()); err != nil && !errors.Is(err, context.Canceled) {
		w.logger.Warn("notify progress", "error", err)
	}
}

// Package ingest owns the media pipeline: Service admits URLs into the
// media table and job queue (used by the web server), Worker runs the
// download → package → upload steps (used by the ingest process).
package ingest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/jobs"
	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/source"
)

// JobKind is the jobs.Job kind for ingest work.
const JobKind = "ingest"

// ProgressChannel is the NOTIFY channel carrying media ids whose status or
// progress changed.
const ProgressChannel = "media_progress"

// MaxAttempts bounds retries for one ingest job.
const MaxAttempts = 3

// Payload is the job payload.
type Payload struct {
	MediaID uuid.UUID `json:"mediaId"`
}

var (
	// ErrUnsupportedURL means no extractor handles the URL.
	ErrUnsupportedURL = errors.New("ingest: unsupported url")
	// ErrBlocked means an administrator has blocklisted the source.
	ErrBlocked = errors.New("ingest: source is blocked")
)

// MediaRepo is the persistence the package needs.
type MediaRepo interface {
	CreateMedia(ctx context.Context, q repository.Querier, sourceKey, sourceURL string) (*entity.Media, bool, error)
	GetMedia(ctx context.Context, id uuid.UUID) (*entity.Media, error)
	GetMediaByKey(ctx context.Context, sourceKey string) (*entity.Media, error)
	IsSourceBlocked(ctx context.Context, sourceKey string) (bool, error)
	SetMediaStatus(ctx context.Context, id uuid.UUID, status entity.MediaStatus) error
	SetMediaProgress(ctx context.Context, id uuid.UUID, progress float32, speedBps, etaMs int64) error
	SetMediaProbed(ctx context.Context, id uuid.UUID, title string, durationMs int64, thumbnailURL string, chapters []entity.Chapter) error
	SetMediaThumbnail(ctx context.Context, id uuid.UUID, thumbnailURL string) error
	SetMediaReady(ctx context.Context, id uuid.UUID, renditions []entity.Rendition, sizeBytes int64, s3Prefix string) error
	SetMediaSubtitles(ctx context.Context, id uuid.UUID, subtitles []entity.Subtitle) error
	SetMediaFailed(ctx context.Context, id uuid.UUID, reason string) error
}

// Service admits URLs.
type Service struct {
	repo      MediaRepo
	queue     *jobs.Queue
	extractor source.Extractor
}

// NewService creates the admission service.
func NewService(repo MediaRepo, queue *jobs.Queue, extractor source.Extractor) *Service {
	return &Service{repo: repo, queue: queue, extractor: extractor}
}

// Key returns the dedupe key for a URL, or ErrUnsupportedURL.
func (s *Service) Key(rawURL string) (string, error) {
	key, ok := s.extractor.Key(rawURL)
	if !ok {
		return "", ErrUnsupportedURL
	}
	return key, nil
}

// EnsureMedia returns the media row for the URL, creating it and enqueuing
// an ingest job when it is new. Pass a transaction as q to make the media
// row and the caller's own rows (queue items) atomic.
func (s *Service) EnsureMedia(ctx context.Context, q repository.Querier, rawURL string) (*entity.Media, error) {
	key, err := s.Key(rawURL)
	if err != nil {
		return nil, err
	}

	blocked, err := s.repo.IsSourceBlocked(ctx, key)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, ErrBlocked
	}

	media, created, err := s.repo.CreateMedia(ctx, q, key, rawURL)
	if err != nil {
		return nil, err
	}
	if created {
		if _, err := s.queue.Enqueue(ctx, q, JobKind, Payload{MediaID: media.ID}, MaxAttempts); err != nil {
			return nil, err
		}
	}

	return media, nil
}

// Preview is what the add form shows before a URL is queued.
type Preview struct {
	Title        string
	DurationMs   int64
	ThumbnailURL string
	// Status is set when the media is already known (queued, ready, …).
	Status entity.MediaStatus
}

// Preview describes a URL without queueing it: a known source answers from
// the database, a new one is probed through the extractor.
func (s *Service) Preview(ctx context.Context, rawURL string) (*Preview, error) {
	key, err := s.Key(rawURL)
	if err != nil {
		return nil, err
	}
	blocked, err := s.repo.IsSourceBlocked(ctx, key)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, ErrBlocked
	}

	media, err := s.repo.GetMediaByKey(ctx, key)
	switch {
	case err == nil && (media.Title != "" || media.Status == entity.MediaReady):
		return &Preview{Title: media.Title, DurationMs: media.DurationMs, ThumbnailURL: media.ThumbnailURL, Status: media.Status}, nil
	case err != nil && !errors.Is(err, repository.ErrNotFound):
		return nil, err
	}

	p, err := s.extractor.Probe(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	out := &Preview{Title: p.Title, DurationMs: p.DurationMs, ThumbnailURL: p.ThumbnailURL}
	if media != nil {
		out.Status = media.Status
	}
	return out, nil
}

// Retry re-queues a failed media item.
func (s *Service) Retry(ctx context.Context, media *entity.Media) error {
	if media.Status != entity.MediaFailed {
		return fmt.Errorf("ingest: media %s is %s, not failed", media.ID, media.Status)
	}
	if err := s.repo.SetMediaStatus(ctx, media.ID, entity.MediaQueued); err != nil {
		return err
	}
	_, err := s.queue.Enqueue(ctx, nil, JobKind, Payload{MediaID: media.ID}, MaxAttempts)
	return err
}

// progressReporter throttles progress writes and notifications to once
// per interval, always flushing the final value.
// progressReporter throttles step progress into the database and derives
// a rate from it: with totalBytes set the rate is a byte throughput, and
// the time left follows from the rate either way. The rate is smoothed
// over the last few samples so a stalled second does not zero the ETA.
type progressReporter struct {
	repo       MediaRepo
	queue      *jobs.Queue
	mediaID    uuid.UUID
	interval   time.Duration
	totalBytes int64
	last       time.Time
	now        func() time.Time

	samples []progressSample
}

type progressSample struct {
	at   time.Time
	frac float64
}

// rateWindow is how far back the rate looks.
const rateWindow = 8 * time.Second

func (p *progressReporter) report(ctx context.Context, v float64, force bool) {
	now := p.now()
	if !force && now.Sub(p.last) < p.interval {
		return
	}
	p.last = now
	speed, eta := p.estimate(now, v)
	if force && v >= 1 {
		speed, eta = 0, 0
	}
	_ = p.repo.SetMediaProgress(ctx, p.mediaID, float32(v), speed, eta)
	_ = p.queue.Notify(ctx, ProgressChannel, p.mediaID.String())
}

// estimate records the sample and returns bytes per second (0 without a
// known total) and the milliseconds left (0 when there is no rate yet).
func (p *progressReporter) estimate(now time.Time, frac float64) (speedBps, etaMs int64) {
	p.samples = append(p.samples, progressSample{at: now, frac: frac})
	cut := 0
	for cut < len(p.samples)-1 && now.Sub(p.samples[cut].at) > rateWindow {
		cut++
	}
	p.samples = p.samples[cut:]
	first := p.samples[0]
	dt := now.Sub(first.at).Seconds()
	dfrac := frac - first.frac
	if dt <= 0 || dfrac <= 0 {
		return 0, 0
	}
	rate := dfrac / dt // fraction per second
	if p.totalBytes > 0 {
		speedBps = int64(rate * float64(p.totalBytes))
	}
	etaMs = int64((1 - frac) / rate * 1000)
	return speedBps, etaMs
}

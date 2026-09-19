package mediastore

import (
	"context"
	"log/slog"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/entity"
)

// EvictionStore is what the janitor needs from the repository.
type EvictionStore interface {
	SumReadyMediaBytes(ctx context.Context) (int64, error)
	ListEvictableMedia(ctx context.Context, limit int) ([]entity.Media, error)
	DeleteMedia(ctx context.Context, id uuid.UUID) error
}

// Evictor keeps packaged media under a byte budget by removing the least
// recently accessed items that no room has queued.
type Evictor struct {
	store  *Store
	repo   EvictionStore
	budget int64
	logger *slog.Logger
	// MinAge protects freshly packaged media from immediate eviction.
	MinAge time.Duration
}

// NewEvictor creates a janitor; budget <= 0 disables it.
func NewEvictor(store *Store, repo EvictionStore, budget int64, logger *slog.Logger) *Evictor {
	return &Evictor{store: store, repo: repo, budget: budget, logger: logger, MinAge: time.Hour}
}

// Run performs one eviction pass and returns the number of items removed.
func (e *Evictor) Run(ctx context.Context) (int, error) {
	if e.budget <= 0 {
		return 0, nil
	}
	total, err := e.repo.SumReadyMediaBytes(ctx)
	if err != nil {
		return 0, err
	}
	if total <= e.budget {
		return 0, nil
	}

	candidates, err := e.repo.ListEvictableMedia(ctx, 50)
	if err != nil {
		return 0, err
	}

	removed := 0
	for _, m := range candidates {
		if total <= e.budget {
			break
		}
		if time.Since(m.LastAccessedAt) < e.MinAge {
			continue
		}
		if err := e.store.DeletePrefix(ctx, Prefix(m.ID.String())); err != nil {
			e.logger.Warn("evict media objects", "media", m.ID, "error", err)
			continue
		}
		if err := e.repo.DeleteMedia(ctx, m.ID); err != nil {
			e.logger.Warn("evict media row", "media", m.ID, "error", err)
			continue
		}
		total -= m.SizeBytes
		removed++
		e.logger.Info("evicted media", "media", m.ID, "title", m.Title, "bytes", m.SizeBytes)
	}

	if total > e.budget {
		e.logger.Warn("media cache over budget with nothing evictable", "bytes", total, "budget", e.budget)
	}
	return removed, nil
}

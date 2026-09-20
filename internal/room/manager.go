package room

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anesthetised/couchcast/internal/ingest"
	"github.com/anesthetised/couchcast/internal/jobs"
	"github.com/anesthetised/couchcast/internal/protocol"
)

// Manager loads rooms on demand, keeps them while viewers are connected and
// feeds them ingest progress.
type Manager struct {
	deps Deps

	mu    sync.Mutex
	rooms map[uuid.UUID]*Room

	// IdleAfter is how long an empty room stays in memory.
	IdleAfter time.Duration
}

// NewManager creates a manager.
func NewManager(deps Deps) *Manager {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.PersistEvery == 0 {
		deps.PersistEvery = 5 * time.Second
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &Manager{deps: deps, rooms: map[uuid.UUID]*Room{}, IdleAfter: 10 * time.Minute}
}

// Warm loads every room that was playing when the server last ran, so
// unattended playback resumes and keeps advancing after a restart.
func (m *Manager) Warm(ctx context.Context) error {
	ids, err := m.deps.Store.ListPlayingRoomIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := m.Get(ctx, id); err != nil {
			m.deps.Logger.Warn("warm room", "room", id, "error", err)
		}
	}
	if len(ids) > 0 {
		m.deps.Logger.Info("resumed playing rooms", "count", len(ids))
	}
	return nil
}

// Get returns the live room, loading it from the database on first use.
func (m *Manager) Get(ctx context.Context, id uuid.UUID) (*Room, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rooms[id]; ok {
		return r, nil
	}
	r, err := load(ctx, m.deps, id)
	if err != nil {
		return nil, err
	}
	m.rooms[id] = r
	return r, nil
}

// Peek returns the room only if it is already loaded.
func (m *Manager) Peek(id uuid.UUID) (*Room, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rooms[id]
	return r, ok
}

// LiveCounts returns connection counts for loaded rooms that have at
// least one viewer.
func (m *Manager) LiveCounts() map[uuid.UUID]int {
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()

	out := make(map[uuid.UUID]int, len(rooms))
	for _, r := range rooms {
		if n := r.Viewers(); n > 0 {
			out[r.ID()] = n
		}
	}
	return out
}

// Playback returns the live clock of a loaded room.
func (m *Manager) Playback(roomID uuid.UUID) (protocol.Playback, bool) {
	r, ok := m.Peek(roomID)
	if !ok {
		return protocol.Playback{}, false
	}
	return r.Playback(), true
}

// Loaded returns the number of rooms in memory.
func (m *Manager) Loaded() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rooms)
}

// Kick disconnects a user from a room if it is loaded.
func (m *Manager) Kick(roomID, userID uuid.UUID, reason string) {
	if r, ok := m.Peek(roomID); ok {
		r.Kick(userID, reason)
	}
}

// Log writes a system line into a room if it is loaded.
func (m *Manager) Log(ctx context.Context, roomID uuid.UUID, line string) {
	if r, ok := m.Peek(roomID); ok {
		r.Log(ctx, line)
	}
}

// KickEverywhere disconnects a user from every loaded room (site ban).
func (m *Manager) KickEverywhere(userID uuid.UUID, reason string) {
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()
	for _, r := range rooms {
		r.Kick(userID, reason)
	}
}

// MediaDeleted makes loaded rooms drop queue items of a removed media.
func (m *Manager) MediaDeleted(ctx context.Context, mediaID uuid.UUID) {
	m.mu.Lock()
	var targets []*Room
	for _, r := range m.rooms {
		r.mu.Lock()
		_, has := r.media[mediaID]
		r.mu.Unlock()
		if has {
			targets = append(targets, r)
		}
	}
	m.mu.Unlock()
	for _, r := range targets {
		if err := r.ReloadQueue(ctx); err != nil {
			m.deps.Logger.Warn("reload queue", "room", r.Slug(), "error", err)
		}
	}
}

// Refresh reloads a loaded room's metadata after a REST change.
func (m *Manager) Refresh(ctx context.Context, roomID uuid.UUID) {
	if r, ok := m.Peek(roomID); ok {
		if err := r.Refresh(ctx); err != nil {
			m.deps.Logger.Warn("refresh room", "room", roomID, "error", err)
		}
	}
}

// Unload drops a room from memory (after deletion).
func (m *Manager) Unload(roomID uuid.UUID, reason string) {
	m.mu.Lock()
	r, ok := m.rooms[roomID]
	delete(m.rooms, roomID)
	m.mu.Unlock()
	if !ok {
		return
	}
	r.mu.Lock()
	for c := range r.viewers {
		c.Close(reason)
	}
	r.viewers = map[Conn]*viewer{}
	if r.advance != nil {
		r.advance.Stop()
	}
	r.mu.Unlock()
}

// Run persists positions and evicts idle rooms until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) error {
	t := time.NewTicker(m.deps.PersistEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.shutdownAll()
			return ctx.Err()
		case <-t.C:
			m.tick(ctx)
		}
	}
}

func (m *Manager) tick(ctx context.Context) {
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()

	for _, r := range rooms {
		if r.Tick(ctx, m.IdleAfter) {
			m.mu.Lock()
			delete(m.rooms, r.ID())
			m.mu.Unlock()
			r.shutdown(ctx)
			m.deps.Logger.Info("room unloaded", "room", r.Slug())
		}
	}
}

func (m *Manager) shutdownAll() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.rooms {
		r.shutdown(ctx)
		delete(m.rooms, id)
	}
}

// ListenProgress relays ingest progress notifications to loaded rooms.
func (m *Manager) ListenProgress(ctx context.Context, pool *pgxpool.Pool) error {
	return jobs.Listen(ctx, pool, ingest.ProgressChannel, m.deps.Logger, func(payload string) {
		id, err := uuid.Parse(payload)
		if err != nil {
			return
		}
		m.mediaChanged(ctx, id)
	})
}

// mediaChanged reloads the media row and pushes it to every loaded room
// that has it queued.
func (m *Manager) mediaChanged(ctx context.Context, mediaID uuid.UUID) {
	m.mu.Lock()
	var targets []*Room
	for _, r := range m.rooms {
		r.mu.Lock()
		_, has := r.media[mediaID]
		r.mu.Unlock()
		if has {
			targets = append(targets, r)
		}
	}
	m.mu.Unlock()
	if len(targets) == 0 {
		return
	}

	loadCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	media, err := m.deps.Store.GetMedia(loadCtx, mediaID)
	if err != nil {
		m.deps.Logger.Warn("reload media", "media", mediaID, "error", err)
		return
	}
	for _, r := range targets {
		r.MediaUpdated(media)
	}
}

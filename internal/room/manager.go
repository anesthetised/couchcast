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
	// loading holds the rooms being read from the database, so concurrent
	// first requests wait for one load instead of starting their own.
	loading map[uuid.UUID]*roomLoad

	// IdleAfter is how long an empty room stays in memory.
	IdleAfter time.Duration
	// ReconcileEvery is how often Run re-reads media that loaded rooms
	// still wait for, in case a progress notification was lost.
	ReconcileEvery time.Duration
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
	if deps.PresenceEvery == 0 {
		deps.PresenceEvery = 250 * time.Millisecond
	}
	return &Manager{deps: deps, rooms: map[uuid.UUID]*Room{}, loading: map[uuid.UUID]*roomLoad{},
		IdleAfter: 10 * time.Minute, ReconcileEvery: 30 * time.Second}
}

// roomLoad is one load in flight; done closes when room or err is set.
type roomLoad struct {
	done chan struct{}
	room *Room
	err  error
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
// The load runs without the manager lock, so a slow database only delays
// the callers that wait for this room; concurrent first requests share
// one load and get the same instance.
func (m *Manager) Get(ctx context.Context, id uuid.UUID) (*Room, error) {
	m.mu.Lock()
	if r, ok := m.rooms[id]; ok {
		m.mu.Unlock()
		return r, nil
	}
	l, waiting := m.loading[id]
	if !waiting {
		l = &roomLoad{done: make(chan struct{})}
		m.loading[id] = l
	}
	m.mu.Unlock()

	if waiting {
		select {
		case <-l.done:
			return l.room, l.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Waiters share the result, so the first caller going away must not
	// cancel the load for them.
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	l.room, l.err = load(lctx, m.deps, id)
	cancel()
	m.mu.Lock()
	delete(m.loading, id)
	if l.err == nil {
		m.rooms[id] = l.room
	}
	m.mu.Unlock()
	close(l.done)
	return l.room, l.err
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

// Debug snapshots a loaded room for a bug report; false when the room is
// not in memory (nobody is watching).
func (m *Manager) Debug(roomID uuid.UUID) (DebugState, bool) {
	r, ok := m.Peek(roomID)
	if !ok {
		return DebugState{}, false
	}
	return r.Debug(), true
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
	for _, r := range m.holding(mediaID) {
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
	r.closeLocked()
	r.mu.Unlock()
}

// Run persists positions, evicts idle rooms and reconciles media until
// ctx is cancelled.
func (m *Manager) Run(ctx context.Context) error {
	t := time.NewTicker(m.deps.PersistEvery)
	defer t.Stop()
	reconcile := time.NewTicker(m.ReconcileEvery)
	defer reconcile.Stop()
	for {
		select {
		case <-ctx.Done():
			m.shutdownAll()
			return ctx.Err()
		case <-t.C:
			m.tick(ctx)
		case <-reconcile.C:
			m.reconcileMedia(ctx)
		}
	}
}

// loaded returns the rooms in memory.
func (m *Manager) loaded() []*Room {
	m.mu.Lock()
	defer m.mu.Unlock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	return rooms
}

// reconcileMedia re-reads, in one query, the media loaded rooms still wait
// for and applies what changed. Rooms normally hear of progress through
// media_progress notifications; one sent while the listener was
// reconnecting is lost, and a room must not wait for good on a video that
// is ready.
func (m *Manager) reconcileMedia(ctx context.Context) {
	rooms := m.loaded()
	seen := map[uuid.UUID]bool{}
	var ids []uuid.UUID
	for _, r := range rooms {
		for _, id := range r.unsettledMedia() {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	fresh, err := m.deps.Store.GetMediaBatch(qctx, ids)
	if err != nil {
		m.deps.Logger.Warn("reconcile media", "error", err)
		return
	}
	for _, r := range rooms {
		r.ReconcileMedia(fresh)
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

// ReasonShutdown closes viewers when the server stops; the hub sends it
// as "going away" (1001), so clients reconnect instead of ending the
// session as they do on a policy close (a kick).
const ReasonShutdown = "server restarting"

func (m *Manager) shutdownAll() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.rooms {
		// The HTTP server does not close hijacked WebSockets on shutdown;
		// without this they would outlive the server and its database.
		r.closeViewers(ReasonShutdown)
		r.shutdown(ctx)
		delete(m.rooms, id)
	}
}

// ListenProgress relays ingest progress notifications to loaded rooms and
// reconciles their media whenever listening (re)starts, since
// notifications sent meanwhile are lost.
func (m *Manager) ListenProgress(ctx context.Context, pool *pgxpool.Pool) error {
	return jobs.Listen(ctx, pool, ingest.ProgressChannel, m.deps.Logger, func(payload string) {
		id, err := uuid.Parse(payload)
		if err != nil {
			return
		}
		m.mediaChanged(ctx, id)
	}, func() { m.reconcileMedia(ctx) })
}

// holding returns the loaded rooms that hold the media. Room locks are
// taken one at a time and never under the manager lock, so one busy room
// cannot hold up the manager.
func (m *Manager) holding(mediaID uuid.UUID) []*Room {
	var targets []*Room
	for _, r := range m.loaded() {
		r.mu.Lock()
		_, has := r.media[mediaID]
		r.mu.Unlock()
		if has {
			targets = append(targets, r)
		}
	}
	return targets
}

// mediaChanged reloads the media row and pushes it to every loaded room
// that has it queued.
func (m *Manager) mediaChanged(ctx context.Context, mediaID uuid.UUID) {
	targets := m.holding(mediaID)
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

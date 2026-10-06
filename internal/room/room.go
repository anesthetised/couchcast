// Package room holds the live state of a room: the authoritative playback
// clock, the queue, connected viewers and votes. One Room value exists per
// room in memory (see Manager); every mutation happens under its mutex and
// ends with a broadcast to the viewers.
//
// The Room coordinates and owns the state; the rules it applies are pure
// and tested without a database:
//
//   - clock.go: the playback clock as a value (position, restart, time left);
//   - order.go: queue order rules (votes, fair turns, moves, shuffle);
//   - snapshot.go: snapshot preparation (presence, personalisation) and
//     delivery to the connections.
//
// The commands live by subject — playback.go, queue.go, presence.go,
// votes.go, chat.go — take the mutex themselves and reach the database
// only through Store and ChatStore (queue writes that belong together
// through Store.InTx). timers.go owns every timer and the rules that keep
// one from acting on a room that moved on or was unloaded.
package room

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"uuid"

	"golang.org/x/time/rate"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/ingest"
	"github.com/anesthetised/couchcast/internal/mediastore"
	"github.com/anesthetised/couchcast/internal/notify"
	"github.com/anesthetised/couchcast/internal/protocol"
	"github.com/anesthetised/couchcast/internal/ratelimit"
	"github.com/anesthetised/couchcast/internal/repository"
)

// Store is the persistence the room needs. Writes that must land together
// (a new media row and its queue item) go through InTx; the room never
// sees a connection pool.
type Store interface {
	InTx(ctx context.Context, fn func(q repository.Querier) error) error
	GetRoomByID(ctx context.Context, id uuid.UUID) (*entity.Room, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (*entity.User, error)
	GetMember(ctx context.Context, roomID, userID uuid.UUID) (*entity.RoomMember, error)
	UserIDsByUsername(ctx context.Context, names []string) (map[string]uuid.UUID, error)
	ListQueue(ctx context.Context, roomID uuid.UUID) ([]entity.QueueItem, error)
	GetMediaBatch(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*entity.Media, error)
	GetMedia(ctx context.Context, id uuid.UUID) (*entity.Media, error)
	ListPlayed(ctx context.Context, roomID uuid.UUID, limit int) ([]entity.QueueItem, error)
	AddQueueItem(ctx context.Context, q repository.Querier, roomID, mediaID uuid.UUID, addedBy *uuid.UUID) (*entity.QueueItem, error)
	DeleteQueueItem(ctx context.Context, roomID, itemID uuid.UUID) error
	MarkQueueItemPlayed(ctx context.Context, roomID, itemID uuid.UUID, at time.Time) error
	RequeuePlayed(ctx context.Context, roomID uuid.UUID) error
	ClearPlayed(ctx context.Context, roomID uuid.UUID) error
	ClearQueue(ctx context.Context, roomID uuid.UUID, keep *uuid.UUID) error
	SetQueueRanks(ctx context.Context, roomID uuid.UUID, ordered []uuid.UUID) error
	UpdateRoomPlayback(ctx context.Context, roomID uuid.UUID, p entity.PlaybackState) error
	SetRoomSchedule(ctx context.Context, roomID uuid.UUID, at *time.Time) error
	ToggleQueueVote(ctx context.Context, itemID, userID uuid.UUID) (bool, error)
	ListQueueVotes(ctx context.Context, roomID uuid.UUID) (map[uuid.UUID][]uuid.UUID, error)
	UpdateRoomSettings(ctx context.Context, id uuid.UUID, settings entity.Settings) error
	ListPlayingRoomIDs(ctx context.Context) ([]uuid.UUID, error)
}

// Admitter turns URLs into media rows (ingest.Service): Admit checks a
// link (and may resolve its host, so the room calls it without its lock),
// Create writes the row inside the room's transaction.
type Admitter interface {
	Admit(ctx context.Context, rawURL string) (ingest.Admission, error)
	Create(ctx context.Context, q repository.Querier, a ingest.Admission) (*entity.Media, error)
	Retry(ctx context.Context, media *entity.Media) error
}

// Conn is a viewer connection as seen by the room. Send must not block:
// the hub buffers and drops slow clients.
type Conn interface {
	Send(msg any)
	Close(reason string)
}

// Deps are shared by every room.
type Deps struct {
	Store  Store
	Chat   ChatStore // nil disables chat
	Admit  Admitter
	Signer *mediastore.Signer
	Logger *slog.Logger
	Now    func() time.Time
	// WaitScale shortens the buffering timers in tests (0 = real time).
	WaitScale float64
	// Persist bounds how often playback position is written while playing.
	PersistEvery time.Duration
	// RejoinGrace is how long after leaving a return still counts as the
	// same visit (no "left"/"joined" lines). Zero means the default.
	RejoinGrace time.Duration
	// PresenceEvery coalesces presence broadcasts (joins, leaves,
	// buffering, lag): at most one snapshot per interval, so a crowd that
	// buffers at once costs a few snapshots instead of one per viewer.
	// Zero sends each at once (tests); NewManager sets the default.
	PresenceEvery time.Duration
	// QueueAddLimiter budgets queue.add per user across rooms. Nil disables.
	QueueAddLimiter *ratelimit.Limiter
	// Notifier reaches users who are not in the room (Web Push). Nil
	// disables it.
	Notifier Notifier
}

// Notifier sends a push notification to a user's browsers (notify.Notifier).
type Notifier interface {
	Notify(user uuid.UUID, msg notify.Message)
}

// Error is a command rejection with a protocol error code.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

var (
	errForbidden = &Error{Code: protocol.CodeForbidden, Message: "insufficient permissions"}
	errNoCurrent = &Error{Code: protocol.CodeInvalid, Message: "nothing is playing"}
)

func notFound(what string) error {
	return &Error{Code: protocol.CodeNotFound, Message: what + " not found"}
}
func invalid(msg string) error { return &Error{Code: protocol.CodeInvalid, Message: msg} }

// viewer is one connection inside the room.
type viewer struct {
	conn      Conn
	user      *entity.User
	role      entity.RoomRole
	buffering bool
	// ignored: the room already waited for this viewer to the limit;
	// cleared when they play again, so they cannot stall the room twice.
	ignored bool
	// lagMs is the last reported distance from the room clock (see lagOf).
	lagMs int64
}

// Room is the in-memory state of one room.
type Room struct {
	deps Deps

	mu    sync.Mutex
	info  *entity.Room
	owner string

	queue  []*entity.QueueItem // rank order
	played []*entity.QueueItem // history, newest first, at most playedKept
	media  map[uuid.UUID]*entity.Media

	clock clock // the authoritative playback clock (clock.go)

	viewers   map[Conn]*viewer
	skipVotes map[uuid.UUID]struct{}
	// votes holds each user's queue votes (user → item), loaded with the
	// queue and kept current by QueueVote, so broadcasts never query.
	votes       map[uuid.UUID]map[uuid.UUID]bool
	chatLimits  map[uuid.UUID]*rate.Limiter
	reactLimits map[uuid.UUID]*rate.Limiter
	lastChatAt  map[uuid.UUID]time.Time // for slow mode
	lastMention map[uuid.UUID]time.Time // last mention push per user
	// leftAt remembers when a user's last connection closed, so a quick
	// reconnect (reload, network blip) is not logged as a new join; the
	// matching timer logs "left" once the grace period passes.
	leftAt map[uuid.UUID]time.Time

	timers timers // every timer the room owns (timers.go)

	lastPersist time.Time
	lastActive  time.Time

	// pinned is the message shown above the chat, nil when none.
	pinned *protocol.ChatMessage
	// waiting holds who the room paused for while they buffer
	// (Settings.WaitForBuffering).
	waiting []string

	// countdown is when a counted-down start begins (nil: none pending).
	countdown *time.Time

	// closed is set when the manager drops the room; a timer that fired
	// just before then finds it and does nothing.
	closed bool

	// unsaved is set when persisting the clock failed; Tick retries.
	unsaved bool
}

// load builds a Room from the database.
func load(ctx context.Context, deps Deps, id uuid.UUID) (*Room, error) {
	info, err := deps.Store.GetRoomByID(ctx, id)
	if err != nil {
		return nil, err
	}
	owner, err := deps.Store.GetUserByID(ctx, info.OwnerID)
	if err != nil {
		return nil, err
	}

	r := &Room{
		deps:        deps,
		info:        info,
		owner:       owner.Username,
		media:       map[uuid.UUID]*entity.Media{},
		viewers:     map[Conn]*viewer{},
		skipVotes:   map[uuid.UUID]struct{}{},
		chatLimits:  map[uuid.UUID]*rate.Limiter{},
		reactLimits: map[uuid.UUID]*rate.Limiter{},
		lastChatAt:  map[uuid.UUID]time.Time{},
		lastMention: map[uuid.UUID]time.Time{},
		leftAt:      map[uuid.UUID]time.Time{},
		timers:      timers{left: map[uuid.UUID]*time.Timer{}},
		clock:       clockFrom(info),
		lastActive:  deps.Now(),
	}

	if err := r.reloadQueue(ctx); err != nil {
		return nil, err
	}
	r.loadPinnedLocked(ctx)

	// A room restored mid-playback resumes from where it was; the clock
	// keeps running from the persisted timestamp.
	if r.clock.current != nil && r.itemByID(*r.clock.current) == nil {
		r.clock.current, r.clock.playing, r.clock.positionMs = nil, false, 0
	}
	if r.clock.playing {
		r.scheduleAdvanceLocked()
	}
	// Restored playing with nobody connected yet (after a restart).
	r.armEmptyPauseLocked()

	return r, nil
}

func unixMs(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.UnixMilli()
}

// ID returns the room id.
func (r *Room) ID() uuid.UUID { return r.info.ID }

// Slug returns the room slug.
func (r *Room) Slug() string { return r.info.Slug }

func (r *Room) now() time.Time { return r.deps.Now() }

// DebugState is the server half of a bug report for a loaded room.
type DebugState struct {
	Playback  protocol.Playback `json:"playback"`
	ServerMs  int64             `json:"serverMs"`
	Viewers   int               `json:"viewers"`
	Buffering []string          `json:"buffering"` // users reporting buffering right now
	Settings  entity.Settings   `json:"settings"`
	Queue     []DebugItem       `json:"queue"` // the first few items
	Played    int               `json:"played"`
	Unsaved   bool              `json:"unsaved,omitempty"` // the clock is waiting to be persisted
}

// DebugItem is a queue entry as seen by the room.
type DebugItem struct {
	ID      uuid.UUID          `json:"id"`
	MediaID uuid.UUID          `json:"mediaId"`
	Title   string             `json:"title"`
	Status  entity.MediaStatus `json:"status"`
	Current bool               `json:"current"`
}

// Debug snapshots the room for a bug report.
func (r *Room) Debug() DebugState {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := DebugState{
		Playback: r.clock.playback(), ServerMs: r.now().UnixMilli(), Viewers: len(r.viewers),
		Buffering: []string{}, Settings: r.info.Settings, Played: len(r.played), Unsaved: r.unsaved,
	}
	d.Playback.Type = ""
	for _, v := range r.viewers {
		if v.buffering && v.user != nil {
			d.Buffering = append(d.Buffering, v.user.Username)
		}
	}
	for i, it := range r.queue {
		if i == 5 {
			break
		}
		item := DebugItem{ID: it.ID, MediaID: it.MediaID, Current: r.clock.isCurrent(it.ID)}
		if m := r.media[it.MediaID]; m != nil {
			item.Title, item.Status = m.Title, m.Status
		}
		d.Queue = append(d.Queue, item)
	}
	return d
}

func (r *Room) requireLocked(actor access.Actor, action access.Action) error {
	if !access.Can(actor, action, r.info) {
		return errForbidden
	}
	return nil
}

// Refresh reloads room metadata after a REST change (name, settings...).
func (r *Room) Refresh(ctx context.Context) error {
	info, err := r.deps.Store.GetRoomByID(ctx, r.info.ID)
	if err != nil {
		return err
	}
	owner, err := r.deps.Store.GetUserByID(ctx, info.OwnerID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	info.CurrentItemID, info.Playing, info.PositionMs, info.PositionAt = r.clock.current, r.clock.playing, r.clock.positionMs, r.clock.positionAt
	r.info = info
	r.owner = owner.Username
	// Roles may have changed under connected viewers (ownership transfer).
	for _, v := range r.viewers {
		if v.user == nil {
			continue
		}
		m, err := r.deps.Store.GetMember(ctx, r.info.ID, v.user.ID)
		switch {
		case err == nil:
			v.role = m.Role
		case errors.Is(err, repository.ErrNotFound):
			v.role = ""
		}
	}
	r.broadcastLocked()
	return nil
}

// EndSession stops playback, moves the whole queue into the history and
// disconnects everyone; the room itself stays.
func (r *Room) EndSession(ctx context.Context, actor access.Actor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ManageSettings); err != nil {
		return err
	}
	ids := make([]uuid.UUID, 0, len(r.queue))
	for _, it := range r.queue {
		ids = append(ids, it.ID)
	}
	for _, id := range ids {
		if err := r.markPlayedLocked(ctx, id); err != nil {
			return err
		}
	}
	r.setCurrentLocked(nil)
	r.savePlaybackLocked(ctx)
	r.logLocked(ctx, actor.User.Username+" ended the session")
	for c := range r.viewers {
		delete(r.viewers, c)
		c.Send(protocol.Kicked{Type: protocol.TypeKicked, Reason: "session ended"})
		c.Close("session ended")
	}
	return nil
}

// Tick is called periodically by the manager: persists the running
// position and reports whether the room is idle enough to unload. A room
// that is playing stays loaded even with nobody watching, so the queue
// keeps advancing and the directory can show it as live.
func (r *Room) Tick(ctx context.Context, idleAfter time.Duration) (idle bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if r.unsaved || (r.clock.playing && now.Sub(r.lastPersist) >= r.deps.PersistEvery) {
		wasUnsaved := r.unsaved
		r.savePlaybackLocked(ctx)
		if wasUnsaved && !r.unsaved {
			r.deps.Logger.Info("persist playback recovered", "room", r.info.Slug)
		}
	}
	// An unsaved room stays loaded: dropping it would lose the state.
	return !r.unsaved && !r.clock.playing && len(r.viewers) == 0 && now.Sub(r.lastActive) > idleAfter
}

// shutdown stops timers and persists state before the room is unloaded.
func (r *Room) shutdown(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeLocked()
	if err := r.persistLocked(ctx); err != nil {
		r.deps.Logger.Warn("persist playback on unload", "room", r.info.Slug, "error", err)
	}
}

// closeViewers disconnects everyone with the reason.
func (r *Room) closeViewers(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for c := range r.viewers {
		c.Close(reason)
	}
	r.viewers = map[Conn]*viewer{}
}

// closeLocked stops every timer and marks the room closed, so nothing
// scheduled acts on it once the manager has let go of it.
func (r *Room) closeLocked() {
	r.closed = true
	r.timers.stopAll()
}

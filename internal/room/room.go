// Package room holds the live state of a room: the authoritative playback
// clock, the queue, connected viewers and votes. One Room value exists per
// room in memory (see Manager); every mutation happens under its mutex and
// ends with a broadcast to the viewers.
package room

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/mediastore"
	"github.com/anesthetised/couchcast/internal/notify"
	"github.com/anesthetised/couchcast/internal/protocol"
	"github.com/anesthetised/couchcast/internal/ratelimit"
	"github.com/anesthetised/couchcast/internal/repository"
)

// Store is the persistence the room needs.
type Store interface {
	Pool() *pgxpool.Pool
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
	ListQueueVotesByUser(ctx context.Context, roomID, userID uuid.UUID) ([]uuid.UUID, error)
	UpdateRoomSettings(ctx context.Context, id uuid.UUID, settings entity.Settings) error
	ListPlayingRoomIDs(ctx context.Context) ([]uuid.UUID, error)
}

// Admitter turns URLs into media rows (ingest.Service).
type Admitter interface {
	EnsureMedia(ctx context.Context, q repository.Querier, rawURL string) (*entity.Media, error)
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

// playedKept is how much history a room keeps in memory and sends out.
const playedKept = 20

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

	current    *uuid.UUID
	playing    bool
	positionMs int64
	positionAt time.Time
	rate       float64 // playback speed, 1 = normal
	seq        uint64

	viewers     map[Conn]*viewer
	skipVotes   map[uuid.UUID]struct{}
	chatLimits  map[uuid.UUID]*rate.Limiter
	reactLimits map[uuid.UUID]*rate.Limiter
	lastChatAt  map[uuid.UUID]time.Time // for slow mode
	lastMention map[uuid.UUID]time.Time // last mention push per user
	// leftAt remembers when a user's last connection closed, so a quick
	// reconnect (reload, network blip) is not logged as a new join; the
	// matching timer logs "left" once the grace period passes.
	leftAt     map[uuid.UUID]time.Time
	leftTimers map[uuid.UUID]*time.Timer

	advance     *time.Timer
	lastPersist time.Time
	lastActive  time.Time

	// pinned is the message shown above the chat, nil when none.
	pinned *protocol.ChatMessage
	// emptyTimer pauses the room once nobody has been here for the rejoin
	// grace period (Settings.PauseWhenEmpty).
	emptyTimer *time.Timer

	// Waiting for buffering viewers (Settings.WaitForBuffering):
	// bufferTimer runs while someone buffers and the room still plays;
	// waiting holds who the room paused for, waitTimer caps the pause.
	bufferTimer *time.Timer
	waiting     []string
	waitTimer   *time.Timer

	// countdown is when a counted-down start begins (nil: none pending).
	countdown      *time.Time
	countdownTimer *time.Timer

	// closed is set when the manager drops the room; a timer that fired
	// just before then finds it and does nothing.
	closed bool
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
		leftTimers:  map[uuid.UUID]*time.Timer{},
		current:     info.CurrentItemID,
		playing:     info.Playing,
		positionMs:  info.PositionMs,
		positionAt:  info.PositionAt,
		rate:        info.Rate,
		lastActive:  deps.Now(),
	}
	if r.rate <= 0 {
		r.rate = 1
	}

	if err := r.reloadQueue(ctx); err != nil {
		return nil, err
	}
	r.loadPinnedLocked(ctx)

	// A room restored mid-playback resumes from where it was; the clock
	// keeps running from the persisted timestamp.
	if r.current != nil && r.itemByID(*r.current) == nil {
		r.current, r.playing, r.positionMs = nil, false, 0
	}
	if r.playing {
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

// --- queue helpers -----------------------------------------------------------

func (r *Room) reloadQueue(ctx context.Context) error {
	items, err := r.deps.Store.ListQueue(ctx, r.info.ID)
	if err != nil {
		return err
	}
	played, err := r.deps.Store.ListPlayed(ctx, r.info.ID, playedKept)
	if err != nil {
		return err
	}
	ids := make([]uuid.UUID, 0, len(items)+len(played))
	for i := range items {
		ids = append(ids, items[i].MediaID)
	}
	for i := range played {
		ids = append(ids, played[i].MediaID)
	}
	media, err := r.deps.Store.GetMediaBatch(ctx, ids)
	if err != nil {
		return err
	}

	r.queue = r.queue[:0]
	for i := range items {
		r.queue = append(r.queue, &items[i])
	}
	r.played = r.played[:0]
	for i := range played {
		r.played = append(r.played, &played[i])
	}
	r.media = media
	return nil
}

// markPlayedLocked moves a queue item into the history.
func (r *Room) markPlayedLocked(ctx context.Context, itemID uuid.UUID) error {
	now := r.now()
	if err := r.deps.Store.MarkQueueItemPlayed(ctx, r.info.ID, itemID, now); err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	idx := r.indexOf(itemID)
	if idx < 0 {
		return nil
	}
	item := r.queue[idx]
	r.queue = append(r.queue[:idx], r.queue[idx+1:]...)
	item.PlayedAt = &now
	r.played = append([]*entity.QueueItem{item}, r.played...)
	if len(r.played) > playedKept {
		r.played = r.played[:playedKept]
	}
	return nil
}

func (r *Room) itemByID(id uuid.UUID) *entity.QueueItem {
	for _, it := range r.queue {
		if it.ID == id {
			return it
		}
	}
	return nil
}

func (r *Room) indexOf(id uuid.UUID) int {
	for i, it := range r.queue {
		if it.ID == id {
			return i
		}
	}
	return -1
}

func (r *Room) currentMedia() *entity.Media {
	if r.current == nil {
		return nil
	}
	it := r.itemByID(*r.current)
	if it == nil {
		return nil
	}
	return r.media[it.MediaID]
}

// --- playback math -----------------------------------------------------------

// positionLocked returns the position at time t.
func (r *Room) positionLocked(t time.Time) int64 {
	if !r.playing {
		return r.positionMs
	}
	pos := r.positionMs + int64(float64(t.Sub(r.positionAt).Milliseconds())*r.rate)
	if m := r.currentMedia(); m != nil && m.DurationMs > 0 && pos > m.DurationMs {
		return m.DurationMs
	}
	return pos
}

func (r *Room) playbackLocked() protocol.Playback {
	return protocol.Playback{
		Type: protocol.TypePlayback, ItemID: r.current, Playing: r.playing,
		PositionMs: r.positionMs, AtServerMs: r.positionAt.UnixMilli(), Rate: r.rate, Seq: r.seq,
	}
}

func (r *Room) stateLocked() entity.PlaybackState {
	return entity.PlaybackState{CurrentItemID: r.current, Playing: r.playing, PositionMs: r.positionMs, PositionAt: r.positionAt, Rate: r.rate}
}

// setPlayback updates the clock: freezes the current position and restarts
// it from now with the new playing flag.
func (r *Room) setPlaybackLocked(playing bool, positionMs int64) {
	r.playing = playing
	r.positionMs = positionMs
	r.positionAt = r.now()
	r.seq++
	r.scheduleAdvanceLocked()
	r.armEmptyPauseLocked()
}

// armEmptyPauseLocked starts the empty-room countdown when the room is
// playing with nobody in it; it is a no-op otherwise or when armed.
func (r *Room) armEmptyPauseLocked() {
	if !r.info.Settings.PauseWhenEmpty || !r.playing || len(r.viewers) > 0 || r.emptyTimer != nil {
		return
	}
	r.emptyTimer = time.AfterFunc(r.rejoinGrace(), r.pauseIfEmpty)
}

func (r *Room) disarmEmptyPauseLocked() {
	if r.emptyTimer != nil {
		r.emptyTimer.Stop()
		r.emptyTimer = nil
	}
}

// pauseIfEmpty fires after the grace period: still nobody here, still
// playing — pause where the clock is, once, and say so in the log.
func (r *Room) pauseIfEmpty() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.emptyTimer = nil
	if !r.info.Settings.PauseWhenEmpty || !r.playing || len(r.viewers) > 0 {
		return
	}
	r.setPlaybackLocked(false, r.positionLocked(r.now()))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.persistLocked(ctx); err != nil {
		r.deps.Logger.Warn("persist empty pause", "room", r.info.Slug, "error", err)
	}
	r.logLocked(ctx, "paused: everyone left")
}

// scheduleAdvanceLocked arms the timer that moves to the next item when
// the current one ends.
func (r *Room) scheduleAdvanceLocked() {
	if r.advance != nil {
		r.advance.Stop()
		r.advance = nil
	}
	m := r.currentMedia()
	if !r.playing || m == nil || m.DurationMs <= 0 || r.current == nil {
		return
	}
	remaining := time.Duration(float64(m.DurationMs-r.positionLocked(r.now()))/r.rate) * time.Millisecond
	if remaining < 0 {
		remaining = 0
	}
	itemID := *r.current
	seq := r.seq
	r.advance = time.AfterFunc(remaining+500*time.Millisecond, func() { r.onEnded(itemID, seq) })
}

// onEnded fires from the advance timer.
func (r *Room) onEnded(itemID uuid.UUID, seq uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if r.current == nil || *r.current != itemID || r.seq != seq || !r.playing {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.nextLocked(ctx); err != nil {
		r.deps.Logger.Error("auto advance", "room", r.info.Slug, "error", err)
	}
	r.broadcastLocked()
}

// setCurrentLocked switches to an item (or nothing) at position 0 and
// starts playing when its media is ready.
func (r *Room) setCurrentLocked(item *entity.QueueItem) {
	r.skipVotes = map[uuid.UUID]struct{}{}
	if item == nil {
		r.current = nil
		r.setPlaybackLocked(false, 0)
		return
	}
	r.endWaitLocked(false)
	r.cancelCountdownLocked()
	id := item.ID
	r.current = &id
	r.rate = 1 // a new video always starts at normal speed
	m := r.media[item.MediaID]
	r.setPlaybackLocked(m.IsReady(), 0)

	// Whoever queued it hears about it if they are not watching.
	if r.deps.Notifier != nil && item.AddedBy != nil && !r.connectedLocked(*item.AddedBy) {
		r.deps.Notifier.Notify(*item.AddedBy, notify.Message{
			Title: "Your video is starting",
			Body:  mediaTitle(m) + " — " + r.info.Name,
			URL:   "/r/" + r.info.Slug,
			Tag:   "start-" + item.ID.String(),
		})
	}
}

// connectedLocked reports whether the user has a live connection here.
func (r *Room) connectedLocked(user uuid.UUID) bool {
	for _, v := range r.viewers {
		if v.user != nil && v.user.ID == user {
			return true
		}
	}
	return false
}

func mediaTitle(m *entity.Media) string {
	if m == nil {
		return "A video"
	}
	if m.Title != "" {
		return m.Title
	}
	return m.SourceURL
}

// allowedRates are the speeds a moderator may pick.
var allowedRates = []float64{0.5, 0.75, 1, 1.25, 1.5, 2}

// SetRate changes the room's playback speed from now on.
func (r *Room) SetRate(ctx context.Context, actor access.Actor, rate float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ControlPlayback); err != nil {
		return err
	}
	if r.current == nil {
		return errNoCurrent
	}
	ok := false
	for _, a := range allowedRates {
		if a == rate {
			ok = true
			break
		}
	}
	if !ok {
		return invalid("rate must be one of 0.5, 0.75, 1, 1.25, 1.5, 2")
	}
	if rate == r.rate {
		return nil
	}
	// Fold the elapsed time in at the old speed, then switch.
	pos := r.positionLocked(r.now())
	r.rate = rate
	r.setPlaybackLocked(r.playing, pos)
	if err := r.persistLocked(ctx); err != nil {
		return err
	}
	r.logLocked(ctx, fmt.Sprintf("%s set speed to %g×", actor.User.Username, rate))
	r.broadcastPlaybackLocked()
	return nil
}

// nextLocked drops the current item and advances to the following one.
func (r *Room) nextLocked(ctx context.Context) error {
	if r.current == nil {
		return errNoCurrent
	}
	idx := r.indexOf(*r.current)
	if err := r.markPlayedLocked(ctx, *r.current); err != nil {
		return err
	}

	var next *entity.QueueItem
	if idx >= 0 && idx < len(r.queue) {
		next = r.queue[idx]
	} else if len(r.queue) > 0 {
		next = r.queue[0]
	}
	// Items whose ingest failed can never play: skip them into the history
	// instead of stalling the room on them.
	for next != nil {
		m := r.media[next.MediaID]
		if m == nil || m.Status != entity.MediaFailed {
			break
		}
		r.logLocked(ctx, "skipped "+mediaLabel(m)+" (not playable)")
		if err := r.markPlayedLocked(ctx, next.ID); err != nil {
			return err
		}
		next = nil
		if idx >= 0 && idx < len(r.queue) {
			next = r.queue[idx]
		} else if len(r.queue) > 0 {
			next = r.queue[0]
		}
	}
	// Loop: the queue ran out, so the history becomes the queue again in
	// the order it was played.
	if next == nil && r.info.Settings.Loop && len(r.played) > 0 {
		if err := r.deps.Store.RequeuePlayed(ctx, r.info.ID); err != nil {
			return err
		}
		if err := r.reloadQueue(ctx); err != nil {
			return err
		}
		if r.info.Settings.VoteMode {
			if err := r.reorderByVotesLocked(ctx); err != nil {
				return err
			}
		}
		if len(r.queue) > 0 {
			next = r.queue[0]
		}
		// Nobody watching: stop at the top instead of cycling on (and
		// logging every lap) for an empty room; play resumes it.
		if len(r.viewers) == 0 && next != nil {
			r.skipVotes = map[uuid.UUID]struct{}{}
			id := next.ID
			r.current, r.rate = &id, 1
			r.setPlaybackLocked(false, 0)
			r.logLocked(ctx, "queue restarted from the top, paused until someone is back")
			return r.persistLocked(ctx)
		}
		r.logLocked(ctx, "queue restarted from the top")
	}
	r.setCurrentLocked(next)
	return r.persistLocked(ctx)
}

func (r *Room) persistLocked(ctx context.Context) error {
	r.lastPersist = r.now()
	// The session has started: the announcement has served its purpose.
	if r.playing && r.info.ScheduledAt != nil {
		r.info.ScheduledAt = nil
		if err := r.deps.Store.SetRoomSchedule(ctx, r.info.ID, nil); err != nil {
			r.deps.Logger.Warn("clear schedule", "room", r.info.Slug, "error", err)
		}
	}
	return r.deps.Store.UpdateRoomPlayback(ctx, r.info.ID, r.stateLocked())
}

// --- snapshot & broadcast ----------------------------------------------------

func (r *Room) mediaInfoLocked(m *entity.Media) protocol.MediaInfo {
	if m == nil {
		return protocol.MediaInfo{}
	}
	info := protocol.MediaInfo{
		ID: m.ID, Title: m.Title, DurationMs: m.DurationMs, ThumbnailURL: m.ThumbnailURL,
		Status: m.Status, Progress: m.Progress, SpeedBps: m.SpeedBps, EtaMs: m.EtaMs, Error: m.Error, Renditions: m.Renditions, Subtitles: m.Subtitles, Chapters: m.Chapters, Storyboard: m.Storyboard, SourceURL: m.SourceURL,
	}
	if info.Renditions == nil {
		info.Renditions = []entity.Rendition{}
	}
	if m.IsReady() && r.deps.Signer != nil {
		info.Manifest = "/media/" + m.ID.String() + "/manifest.mpd"
		info.Token = r.deps.Signer.Sign(m.ID, r.now())
	}
	return info
}

// snapshotLocked builds the viewer-independent snapshot.
func (r *Room) snapshotLocked() protocol.Snapshot {
	snap := protocol.Snapshot{
		Type: protocol.TypeRoomState,
		Room: protocol.RoomInfo{
			ID: r.info.ID, Slug: r.info.Slug, Name: r.info.Name, Visibility: r.info.Visibility,
			Settings: r.info.Settings, Owner: r.owner, Description: r.info.Description, Pinned: r.pinned,
			ScheduledMs: unixMs(r.info.ScheduledAt),
		},
		Playback: r.playbackLocked(),
		Waiting:  r.waiting,
		CountdownMs: func() int64 {
			if r.countdown == nil {
				return 0
			}
			return r.countdown.UnixMilli()
		}(),
		Queue:   make([]protocol.QueueEntry, 0, len(r.queue)),
		Members: []protocol.Presence{},
	}
	snap.Playback.Type = ""

	for _, it := range r.queue {
		snap.Queue = append(snap.Queue, protocol.QueueEntry{
			ID: it.ID, Media: r.mediaInfoLocked(r.media[it.MediaID]), AddedBy: it.AddedByName, Votes: it.Votes,
			Current: r.current != nil && *r.current == it.ID,
		})
	}
	snap.Played = make([]protocol.QueueEntry, 0, len(r.played))
	for _, it := range r.played {
		var playedMs int64
		if it.PlayedAt != nil {
			playedMs = it.PlayedAt.UnixMilli()
		}
		snap.Played = append(snap.Played, protocol.QueueEntry{
			ID: it.ID, Media: r.mediaInfoLocked(r.media[it.MediaID]), AddedBy: it.AddedByName, PlayedMs: playedMs,
		})
	}

	seen := map[string]int{}
	for _, v := range r.viewers {
		if v.user == nil {
			snap.Guests++
			continue
		}
		if i, ok := seen[v.user.Username]; ok {
			snap.Members[i].Buffering = snap.Members[i].Buffering || v.buffering
			if abs(v.lagMs) > abs(snap.Members[i].LagMs) {
				snap.Members[i].LagMs = v.lagMs // the worst of their tabs
			}
			continue
		}
		seen[v.user.Username] = len(snap.Members)
		snap.Members = append(snap.Members, protocol.Presence{Username: v.user.Username, Color: v.user.AvatarColor, Role: v.role, Buffering: v.buffering, LagMs: v.lagMs})
	}
	sort.Slice(snap.Members, func(i, j int) bool { return snap.Members[i].Username < snap.Members[j].Username })

	snap.SkipVotes = len(r.skipVotes)
	snap.SkipNeeded = r.skipNeededLocked()

	return snap
}

// personalize adds the viewer-specific flags to a copy of the snapshot.
func (r *Room) personalizeLocked(base protocol.Snapshot, v *viewer, voted map[uuid.UUID]bool) protocol.Snapshot {
	snap := base
	if v.user == nil {
		return snap
	}
	_, snap.SkipVoted = r.skipVotes[v.user.ID]
	if len(voted) > 0 {
		snap.Queue = make([]protocol.QueueEntry, len(base.Queue))
		copy(snap.Queue, base.Queue)
		for i := range snap.Queue {
			snap.Queue[i].Voted = voted[snap.Queue[i].ID]
		}
	}
	return snap
}

// broadcastLocked sends the current snapshot to every viewer.
func (r *Room) broadcastLocked() {
	base := r.snapshotLocked()
	for _, v := range r.viewers {
		v.conn.Send(r.personalizeLocked(base, v, r.votesFor(v)))
	}
}

// broadcastPlaybackLocked sends only the clock: cheaper and jitter-free.
func (r *Room) broadcastPlaybackLocked() {
	pb := r.playbackLocked()
	for _, v := range r.viewers {
		v.conn.Send(pb)
	}
}

// votesFor returns the viewer's queue votes. Votes are read from the
// database lazily: they change rarely and only matter in vote mode.
func (r *Room) votesFor(v *viewer) map[uuid.UUID]bool {
	if v.user == nil || !r.info.Settings.VoteMode {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ids, err := r.deps.Store.ListQueueVotesByUser(ctx, r.info.ID, v.user.ID)
	if err != nil {
		return nil
	}
	out := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// --- viewers -----------------------------------------------------------------

// Join registers a connection and sends it the welcome message with the
// chat backlog.
func (r *Room) Join(ctx context.Context, conn Conn, actor access.Actor) {
	messages := r.recentMessages(ctx)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.disarmEmptyPauseLocked()
	first := actor.User != nil && !r.userOnlineLocked(actor.User.ID) &&
		r.now().Sub(r.leftAt[actor.User.ID]) > r.rejoinGrace()
	if actor.User != nil {
		if t, ok := r.leftTimers[actor.User.ID]; ok {
			t.Stop()
			delete(r.leftTimers, actor.User.ID)
		}
	}
	v := &viewer{conn: conn, user: actor.User, role: actor.Role()}
	r.viewers[conn] = v
	r.lastActive = r.now()

	base := r.snapshotLocked()
	welcome := protocol.Welcome{
		Type:     protocol.TypeWelcome,
		Role:     v.role,
		Snapshot: r.personalizeLocked(base, v, r.votesFor(v)),
		Messages: messages,
	}
	if v.user != nil {
		name := v.user.Username
		welcome.Me = &name
	}
	if welcome.Messages == nil {
		welcome.Messages = []protocol.ChatMessage{}
	}
	conn.Send(welcome)

	// Others learn about the new presence; everyone, including the
	// newcomer, gets the log line after the welcome.
	for c, other := range r.viewers {
		if c != conn {
			other.conn.Send(r.personalizeLocked(base, other, r.votesFor(other)))
		}
	}
	if first {
		r.logLocked(ctx, actor.User.Username+" joined")
	}
}

// defaultRejoinGrace is how long after leaving a return is still "the
// same visit".
const defaultRejoinGrace = 2 * time.Minute

func (r *Room) rejoinGrace() time.Duration {
	if r.deps.RejoinGrace > 0 {
		return r.deps.RejoinGrace
	}
	return defaultRejoinGrace
}

// onLeft fires after the grace period: if the user is still gone, log it.
func (r *Room) onLeft(userID uuid.UUID, username string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	delete(r.leftTimers, userID)
	if r.userOnlineLocked(userID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.logLocked(ctx, username+" left")
}

// userOnlineLocked reports whether the user has another connection.
func (r *Room) userOnlineLocked(userID uuid.UUID) bool {
	for _, v := range r.viewers {
		if v.user != nil && v.user.ID == userID {
			return true
		}
	}
	return false
}

// Leave unregisters a connection.
func (r *Room) Leave(conn Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.viewers[conn]
	if !ok {
		return
	}
	delete(r.viewers, conn)
	r.lastActive = r.now()
	r.armEmptyPauseLocked()
	r.checkBufferingLocked()
	if v.user != nil && !r.userOnlineLocked(v.user.ID) {
		r.leftAt[v.user.ID] = r.now()
		if t, ok := r.leftTimers[v.user.ID]; ok {
			t.Stop()
		}
		id, name := v.user.ID, v.user.Username
		r.leftTimers[id] = time.AfterFunc(r.rejoinGrace(), func() { r.onLeft(id, name) })
	}
	r.broadcastLocked()
}

// UpdateRole refreshes the role shown for a connection.
func (r *Room) UpdateRole(conn Conn, role entity.RoomRole) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.viewers[conn]; ok && v.role != role {
		v.role = role
		r.broadcastLocked()
	}
}

// Kick closes every connection of the user.
func (r *Room) Kick(userID uuid.UUID, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for c, v := range r.viewers {
		if v.user != nil && v.user.ID == userID {
			delete(r.viewers, c)
			c.Send(protocol.Kicked{Type: protocol.TypeKicked, Reason: reason})
			c.Close(reason)
		}
	}
	r.armEmptyPauseLocked()
	r.broadcastLocked()
}

// Playback returns the live authoritative clock.
func (r *Room) Playback() protocol.Playback {
	r.mu.Lock()
	defer r.mu.Unlock()
	pb := r.playbackLocked()
	pb.Type = ""
	return pb
}

// DebugState is the server half of a bug report for a loaded room.
type DebugState struct {
	Playback  protocol.Playback `json:"playback"`
	ServerMs  int64             `json:"serverMs"`
	Viewers   int               `json:"viewers"`
	Buffering []string          `json:"buffering"` // users reporting buffering right now
	Settings  entity.Settings   `json:"settings"`
	Queue     []DebugItem       `json:"queue"` // the first few items
	Played    int               `json:"played"`
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
		Playback: r.playbackLocked(), ServerMs: r.now().UnixMilli(), Viewers: len(r.viewers),
		Buffering: []string{}, Settings: r.info.Settings, Played: len(r.played),
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
		item := DebugItem{ID: it.ID, MediaID: it.MediaID, Current: r.current != nil && *r.current == it.ID}
		if m := r.media[it.MediaID]; m != nil {
			item.Title, item.Status = m.Title, m.Status
		}
		d.Queue = append(d.Queue, item)
	}
	return d
}

// Viewers returns the number of connections.
func (r *Room) Viewers() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.viewers)
}

// Report records client telemetry.
func (r *Room) Report(conn Conn, state string, positionMs int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.viewers[conn]
	if !ok {
		return
	}
	// How far the viewer's video is from the clock, coarsely: presence
	// only changes (and is broadcast) when that moves by half a second
	// or crosses the one-second "in sync" band.
	changed := false
	if r.current != nil && state != "ended" {
		if lag := lagOf(r.positionLocked(r.now()) - positionMs); lag != v.lagMs {
			v.lagMs = lag
			changed = true
		}
	}
	buffering := state == "buffering"
	if changed && v.buffering == buffering {
		r.broadcastLocked()
	}
	if v.buffering != buffering {
		v.buffering = buffering
		if !buffering {
			v.ignored = false
		}
		r.checkBufferingLocked()
		r.broadcastLocked()
	}
}

// lagOf rounds a distance from the clock (positive: behind) to half
// seconds, and to 0 inside the band where the synchroniser keeps up.
func lagOf(ms int64) int64 {
	if ms > -1000 && ms < 1000 {
		return 0
	}
	return (ms + sign(ms)*250) / 500 * 500
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func sign(n int64) int64 {
	if n < 0 {
		return -1
	}
	return 1
}

const (
	// bufferPatience is how long a viewer may buffer before the room waits.
	bufferPatience = 4 * time.Second
	// maxWait is how long the room waits before going on without them.
	maxWait = 30 * time.Second
)

// stuckLocked lists the viewers the room would wait for.
func (r *Room) stuckLocked() []string {
	var out []string
	for _, v := range r.viewers {
		if v.buffering && !v.ignored && v.user != nil && !slices.Contains(out, v.user.Username) {
			out = append(out, v.user.Username)
		}
	}
	slices.Sort(out)
	return out
}

// checkBufferingLocked reacts to a change in who is buffering: resume
// when the room waited and everyone is ready, start the patience timer
// when someone starts buffering while the room plays.
func (r *Room) checkBufferingLocked() {
	stuck := r.stuckLocked()
	if r.waiting != nil {
		if len(stuck) == 0 {
			r.endWaitLocked(true)
		} else {
			r.waiting = stuck
		}
		return
	}
	if !r.info.Settings.WaitForBuffering || !r.playing || len(stuck) == 0 {
		if r.bufferTimer != nil {
			r.bufferTimer.Stop()
			r.bufferTimer = nil
		}
		return
	}
	if r.bufferTimer == nil {
		r.bufferTimer = time.AfterFunc(r.patience(bufferPatience), r.startWaiting)
	}
}

// patience lets tests shorten the timers through Deps.RejoinGrace's
// sibling; production uses the constants.
func (r *Room) patience(d time.Duration) time.Duration {
	if r.deps.WaitScale > 0 {
		return time.Duration(float64(d) * r.deps.WaitScale)
	}
	return d
}

// startWaiting pauses the room for whoever is still buffering.
func (r *Room) startWaiting() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.bufferTimer = nil
	stuck := r.stuckLocked()
	if !r.info.Settings.WaitForBuffering || !r.playing || len(stuck) == 0 || r.waiting != nil {
		return
	}
	r.waiting = stuck
	r.setPlaybackLocked(false, r.positionLocked(r.now()))
	r.waitTimer = time.AfterFunc(r.patience(maxWait), r.stopWaiting)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.persistLocked(ctx); err != nil {
		r.deps.Logger.Warn("persist wait pause", "room", r.info.Slug, "error", err)
	}
	r.logLocked(ctx, "waiting for "+strings.Join(stuck, ", "))
	r.broadcastLocked()
}

// stopWaiting goes on without the viewers still buffering after maxWait.
func (r *Room) stopWaiting() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.waitTimer = nil
	if r.waiting == nil {
		return
	}
	for _, v := range r.viewers {
		if v.buffering {
			v.ignored = true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.logLocked(ctx, "continuing without "+strings.Join(r.waiting, ", "))
	r.endWaitLocked(true)
}

// endWaitLocked leaves the waiting state, resuming playback when asked
// (a moderator's own play/pause leaves it without touching the clock).
func (r *Room) endWaitLocked(resume bool) {
	if r.waitTimer != nil {
		r.waitTimer.Stop()
		r.waitTimer = nil
	}
	if r.waiting == nil {
		return
	}
	r.waiting = nil
	if resume && !r.playing && r.current != nil {
		r.setPlaybackLocked(true, r.positionMs)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.persistLocked(ctx); err != nil {
			r.deps.Logger.Warn("persist wait resume", "room", r.info.Slug, "error", err)
		}
	}
	r.broadcastLocked()
}

// --- commands ----------------------------------------------------------------

func (r *Room) requireLocked(actor access.Actor, action access.Action) error {
	if !access.Can(actor, action, r.info) {
		return errForbidden
	}
	return nil
}

// Play resumes playback.
func (r *Room) Play(ctx context.Context, actor access.Actor) error {
	return r.play(ctx, actor, false)
}

// PlayCountdown starts playback after a 3-2-1 every viewer sees.
func (r *Room) PlayCountdown(ctx context.Context, actor access.Actor) error {
	return r.play(ctx, actor, true)
}

// countdownFor is how long the 3-2-1 before a counted start lasts.
const countdownFor = 3 * time.Second

func (r *Room) play(ctx context.Context, actor access.Actor, countdown bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ControlPlayback); err != nil {
		return err
	}
	m := r.currentMedia()
	if m == nil {
		return errNoCurrent
	}
	if !m.IsReady() {
		return invalid("media is not ready yet")
	}
	r.endWaitLocked(false)
	if r.playing {
		return nil
	}
	pos := r.positionMs
	if m.DurationMs > 0 && pos >= m.DurationMs {
		pos = 0
	}
	// The announced session starts with a countdown, as does any start a
	// moderator asks to count down; a second play during it starts now.
	if (countdown || r.info.ScheduledAt != nil) && r.countdown == nil {
		at := r.now().Add(countdownFor)
		r.countdown = &at
		r.countdownTimer = time.AfterFunc(r.patience(countdownFor), r.endCountdown)
		r.broadcastLocked()
		return nil
	}
	r.cancelCountdownLocked()
	r.setPlaybackLocked(true, pos)
	if err := r.persistLocked(ctx); err != nil {
		return err
	}
	r.broadcastPlaybackLocked()
	return nil
}

// endCountdown starts playback when the countdown runs out.
func (r *Room) endCountdown() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if r.countdown == nil {
		return
	}
	r.countdown, r.countdownTimer = nil, nil
	m := r.currentMedia()
	if m == nil || !m.IsReady() || r.playing {
		r.broadcastLocked()
		return
	}
	pos := r.positionMs
	if m.DurationMs > 0 && pos >= m.DurationMs {
		pos = 0
	}
	r.setPlaybackLocked(true, pos)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.persistLocked(ctx); err != nil {
		r.deps.Logger.Warn("persist countdown start", "room", r.info.Slug, "error", err)
	}
	r.broadcastLocked()
}

// cancelCountdownLocked drops a pending countdown (pause, seek to another
// item, a direct play).
func (r *Room) cancelCountdownLocked() {
	if r.countdownTimer != nil {
		r.countdownTimer.Stop()
	}
	if r.countdown != nil {
		r.countdown, r.countdownTimer = nil, nil
		r.broadcastLocked()
	}
}

// Pause freezes playback.
func (r *Room) Pause(ctx context.Context, actor access.Actor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ControlPlayback); err != nil {
		return err
	}
	if r.current == nil {
		return errNoCurrent
	}
	r.endWaitLocked(false)
	r.cancelCountdownLocked()
	if !r.playing {
		return nil
	}
	r.setPlaybackLocked(false, r.positionLocked(r.now()))
	if err := r.persistLocked(ctx); err != nil {
		return err
	}
	r.broadcastPlaybackLocked()
	return nil
}

// Seek jumps to an absolute position, keeping the playing flag.
func (r *Room) Seek(ctx context.Context, actor access.Actor, positionMs int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ControlPlayback); err != nil {
		return err
	}
	m := r.currentMedia()
	if m == nil {
		return errNoCurrent
	}
	if positionMs < 0 {
		positionMs = 0
	}
	if m.DurationMs > 0 && positionMs > m.DurationMs {
		positionMs = m.DurationMs
	}
	r.setPlaybackLocked(r.playing && m.IsReady(), positionMs)
	if err := r.persistLocked(ctx); err != nil {
		return err
	}
	r.broadcastPlaybackLocked()
	return nil
}

// Next skips to the following queue item.
func (r *Room) Next(ctx context.Context, actor access.Actor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ControlPlayback); err != nil {
		return err
	}
	if r.current == nil {
		return errNoCurrent
	}
	// Logged first so a loop restart reads after the skip in the chat.
	r.logLocked(ctx, actor.User.Username+" skipped "+mediaLabel(r.currentMedia()))
	if err := r.nextLocked(ctx); err != nil {
		return err
	}
	r.broadcastLocked()
	return nil
}

// Jump makes the given item current, dropping the one that was playing.
func (r *Room) Jump(ctx context.Context, actor access.Actor, itemID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ControlPlayback); err != nil {
		return err
	}
	target := r.itemByID(itemID)
	if target == nil {
		return notFound("queue item")
	}
	if r.current != nil && *r.current != itemID {
		if err := r.markPlayedLocked(ctx, *r.current); err != nil {
			return err
		}
	}
	r.setCurrentLocked(target)
	if err := r.persistLocked(ctx); err != nil {
		return err
	}
	r.logLocked(ctx, actor.User.Username+" jumped to "+mediaLabel(r.media[target.MediaID]))
	r.broadcastLocked()
	return nil
}

// QueueAdd admits a URL and appends it to the queue, or places it right
// after the current item when next is set (manual mode only).
func (r *Room) QueueAdd(ctx context.Context, actor access.Actor, rawURL string, next, force bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.AddToQueue); err != nil {
		return err
	}
	if len(r.queue) >= 200 {
		return invalid("queue is full")
	}
	if r.deps.QueueAddLimiter != nil && actor.User != nil && !r.deps.QueueAddLimiter.Allow(actor.User.ID.String()) {
		return &Error{Code: protocol.CodeRateLimit, Message: "you are adding videos too quickly"}
	}

	tx, err := r.deps.Store.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	media, err := r.deps.Admit.EnsureMedia(ctx, tx, rawURL)
	if err != nil {
		if msg := admitMessage(err); msg != "" {
			return &Error{Code: protocol.CodeInvalid, Message: msg}
		}
		return err // internal: logged by the hub, "internal error" to the client
	}
	if !force {
		if err := r.duplicateLocked(media.ID); err != nil {
			return err
		}
	}
	var addedBy *uuid.UUID
	if actor.User != nil {
		addedBy = &actor.User.ID
	}
	item, err := r.deps.Store.AddQueueItem(ctx, tx, r.info.ID, media.ID, addedBy)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	if actor.User != nil {
		item.AddedByName = actor.User.Username
	}
	r.queue = append(r.queue, item)
	r.media[media.ID] = media
	if next && r.orderedByRule() == "" && r.current != nil && len(r.queue) > 1 {
		if err := r.placeAfterLocked(ctx, len(r.queue)-1, r.current); err != nil {
			return err
		}
	}
	if err := r.fairReorderLocked(ctx); err != nil {
		return err
	}
	if actor.User != nil {
		r.logLocked(ctx, actor.User.Username+" added "+mediaLabel(media))
	}

	if r.current == nil {
		r.setCurrentLocked(item)
		if err := r.persistLocked(ctx); err != nil {
			return err
		}
	}
	r.broadcastLocked()
	return nil
}

// maxAddMany bounds one bulk add (ingest.PlaylistLimit on the import side).
const maxAddMany = 50

// QueueAddMany queues several links at once, in order — a playlist
// import. Links the room already has (queued or played), unsupported or
// blocked links are skipped, and so is anything past the queue limit. The
// add budget is charged once. With next (manual mode, something playing)
// the batch lands right after the current item.
func (r *Room) QueueAddMany(ctx context.Context, actor access.Actor, urls []string, next bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.AddToQueue); err != nil {
		return err
	}
	if len(urls) == 0 || len(urls) > maxAddMany {
		return invalid(fmt.Sprintf("send between 1 and %d links", maxAddMany))
	}
	if r.deps.QueueAddLimiter != nil && actor.User != nil && !r.deps.QueueAddLimiter.Allow(actor.User.ID.String()) {
		return &Error{Code: protocol.CodeRateLimit, Message: "you are adding videos too quickly"}
	}

	seen := map[uuid.UUID]bool{}
	for _, it := range r.queue {
		seen[it.MediaID] = true
	}
	for _, it := range r.played {
		seen[it.MediaID] = true
	}

	tx, err := r.deps.Store.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var addedBy *uuid.UUID
	if actor.User != nil {
		addedBy = &actor.User.ID
	}
	var (
		added   []*entity.QueueItem
		medias  []*entity.Media
		skipped int
	)
	for _, raw := range urls {
		if len(r.queue)+len(added) >= 200 {
			skipped++
			continue
		}
		media, err := r.deps.Admit.EnsureMedia(ctx, tx, raw)
		if errors.Is(err, errUnsupported) || errors.Is(err, errBlocked) {
			skipped++
			continue
		}
		if err != nil {
			return err
		}
		if seen[media.ID] {
			skipped++
			continue
		}
		seen[media.ID] = true
		item, err := r.deps.Store.AddQueueItem(ctx, tx, r.info.ID, media.ID, addedBy)
		if err != nil {
			return err
		}
		if actor.User != nil {
			item.AddedByName = actor.User.Username
		}
		added = append(added, item)
		medias = append(medias, media)
	}
	if len(added) == 0 {
		return &Error{Code: protocol.CodeDuplicate, Message: "nothing to add: these videos are already here or cannot be played"}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	for i, item := range added {
		r.media[item.MediaID] = medias[i]
	}
	if next && r.orderedByRule() == "" && r.current != nil {
		// One re-rank for the whole batch: current, the batch, the rest.
		ordered := make([]*entity.QueueItem, 0, len(r.queue)+len(added))
		rest := make([]*entity.QueueItem, 0, len(r.queue))
		for _, it := range r.queue {
			if it.ID == *r.current {
				ordered = append(ordered, it)
			} else {
				rest = append(rest, it)
			}
		}
		ordered = append(append(ordered, added...), rest...)
		ids := make([]uuid.UUID, len(ordered))
		for i, it := range ordered {
			ids[i] = it.ID
			it.Rank = fmt.Sprintf("%08d", i+1)
		}
		if err := r.deps.Store.SetQueueRanks(ctx, r.info.ID, ids); err != nil {
			return err
		}
		r.queue = ordered
	} else {
		r.queue = append(r.queue, added...)
	}
	// In vote mode the batch has no votes yet and is the newest: the end
	// of the queue is already where the vote order puts it.
	if err := r.fairReorderLocked(ctx); err != nil {
		return err
	}

	if actor.User != nil {
		line := fmt.Sprintf("%s added %d %s from a playlist", actor.User.Username, len(added), plural(len(added), "video", "videos"))
		if skipped > 0 {
			line += fmt.Sprintf(" (%d skipped)", skipped)
		}
		r.logLocked(ctx, line)
	}
	if r.current == nil {
		r.setCurrentLocked(added[0])
		if err := r.persistLocked(ctx); err != nil {
			return err
		}
	}
	r.broadcastLocked()
	return nil
}

// duplicateLocked answers CodeDuplicate when the media is already queued
// or was played in this session, so the client can ask before doubling.
func (r *Room) duplicateLocked(mediaID uuid.UUID) error {
	for _, it := range r.queue {
		if it.MediaID == mediaID {
			return &Error{Code: protocol.CodeDuplicate, Message: "this video is already in the queue"}
		}
	}
	for _, it := range r.played {
		if it.MediaID == mediaID {
			return &Error{Code: protocol.CodeDuplicate, Message: "this video has already been played"}
		}
	}
	return nil
}

// QueueClear drops every waiting item; the current one keeps playing.
func (r *Room) QueueClear(ctx context.Context, actor access.Actor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ManageQueue); err != nil {
		return err
	}
	if err := r.deps.Store.ClearQueue(ctx, r.info.ID, r.current); err != nil {
		return err
	}
	kept := r.queue[:0]
	for _, it := range r.queue {
		if r.current != nil && it.ID == *r.current {
			kept = append(kept, it)
		}
	}
	dropped := len(r.queue) - len(kept)
	r.queue = kept
	if actor.User != nil && dropped > 0 {
		r.logLocked(ctx, fmt.Sprintf("%s cleared the queue (%d %s)", actor.User.Username, dropped, plural(dropped, "video", "videos")))
	}
	r.broadcastLocked()
	return nil
}

// QueueShuffle reorders the waiting items at random; the current one
// stays first. Vote mode owns the order and refuses.
func (r *Room) QueueShuffle(ctx context.Context, actor access.Actor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ManageQueue); err != nil {
		return err
	}
	if why := r.orderedByRule(); why != "" {
		return invalid(why)
	}
	rest := r.queue
	if r.current != nil && len(r.queue) > 0 && r.queue[0].ID == *r.current {
		rest = r.queue[1:]
	}
	if len(rest) < 2 {
		return nil
	}
	rand.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] }) //nolint:gosec // play order, not a secret
	ids := make([]uuid.UUID, len(r.queue))
	for i, it := range r.queue {
		ids[i] = it.ID
	}
	if err := r.deps.Store.SetQueueRanks(ctx, r.info.ID, ids); err != nil {
		return err
	}
	for i, it := range r.queue {
		it.Rank = fmt.Sprintf("%08d", i+1)
	}
	if actor.User != nil {
		r.logLocked(ctx, actor.User.Username+" shuffled the queue")
	}
	r.broadcastLocked()
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// QueueReplay re-queues an item from the history as a fresh entry.
func (r *Room) QueueReplay(ctx context.Context, actor access.Actor, itemID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.AddToQueue); err != nil {
		return err
	}
	var src *entity.QueueItem
	for _, it := range r.played {
		if it.ID == itemID {
			src = it
			break
		}
	}
	if src == nil {
		return notFound("played item")
	}
	if len(r.queue) >= 200 {
		return invalid("queue is full")
	}
	var addedBy *uuid.UUID
	if actor.User != nil {
		addedBy = &actor.User.ID
	}
	item, err := r.deps.Store.AddQueueItem(ctx, r.deps.Store.Pool(), r.info.ID, src.MediaID, addedBy)
	if err != nil {
		return err
	}
	if actor.User != nil {
		item.AddedByName = actor.User.Username
		r.logLocked(ctx, actor.User.Username+" re-added "+mediaLabel(r.media[src.MediaID]))
	}
	r.queue = append(r.queue, item)
	if err := r.fairReorderLocked(ctx); err != nil {
		return err
	}
	if r.current == nil {
		r.setCurrentLocked(item)
		if err := r.persistLocked(ctx); err != nil {
			return err
		}
	}
	r.broadcastLocked()
	return nil
}

// QueueClearPlayed empties the history.
func (r *Room) QueueClearPlayed(ctx context.Context, actor access.Actor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ManageQueue); err != nil {
		return err
	}
	if err := r.deps.Store.ClearPlayed(ctx, r.info.ID); err != nil {
		return err
	}
	r.played = r.played[:0]
	r.broadcastLocked()
	return nil
}

// admitMessage explains an admission refusal to the viewer; "" for
// anything else, which is an internal failure.
func admitMessage(err error) string {
	switch {
	case errors.Is(err, errUnsupported):
		return "this link is not supported"
	case errors.Is(err, errBlocked):
		return "this video has been blocked by an administrator"
	default:
		return ""
	}
}

// QueueRemove deletes an item; removing the current one advances.
func (r *Room) QueueRemove(ctx context.Context, actor access.Actor, itemID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.itemByID(itemID)
	if item == nil {
		return notFound("queue item")
	}
	// Members may remove what they added themselves.
	own := actor.User != nil && item.AddedBy != nil && *item.AddedBy == actor.User.ID
	if !own || item.ID == ptrVal(r.current) {
		if err := r.requireLocked(actor, access.ManageQueue); err != nil {
			return err
		}
	}
	if r.current != nil && *r.current == itemID {
		if err := r.nextLocked(ctx); err != nil {
			return err
		}
		r.broadcastLocked()
		return nil
	}
	if err := r.deps.Store.DeleteQueueItem(ctx, r.info.ID, itemID); err != nil {
		return err
	}
	if idx := r.indexOf(itemID); idx >= 0 {
		r.queue = append(r.queue[:idx], r.queue[idx+1:]...)
	}
	r.broadcastLocked()
	return nil
}

// QueueMove places an item after another (nil = head). The current item
// always stays first.
func (r *Room) QueueMove(ctx context.Context, actor access.Actor, itemID uuid.UUID, afterID *uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ManageQueue); err != nil {
		return err
	}
	if why := r.orderedByRule(); why != "" {
		return invalid(why)
	}
	idx := r.indexOf(itemID)
	if idx < 0 {
		return notFound("queue item")
	}
	if r.current != nil && *r.current == itemID {
		return invalid("the current item cannot be moved")
	}

	if err := r.placeAfterLocked(ctx, idx, afterID); err != nil {
		return err
	}
	r.broadcastLocked()
	return nil
}

// placeAfterLocked moves the item at idx right after afterID (nil = head)
// and persists the new ranks.
func (r *Room) placeAfterLocked(ctx context.Context, idx int, afterID *uuid.UUID) error {
	item := r.queue[idx]
	rest := append(append([]*entity.QueueItem{}, r.queue[:idx]...), r.queue[idx+1:]...)

	insertAt := 0
	if afterID != nil {
		found := false
		for i, it := range rest {
			if it.ID == *afterID {
				insertAt, found = i+1, true
				break
			}
		}
		if !found {
			return notFound("anchor item")
		}
	}
	// Never place anything ahead of the current item.
	if r.current != nil && insertAt == 0 && len(rest) > 0 && rest[0].ID == *r.current {
		insertAt = 1
	}

	ordered := append(append(append([]*entity.QueueItem{}, rest[:insertAt]...), item), rest[insertAt:]...)
	ids := make([]uuid.UUID, len(ordered))
	for i, it := range ordered {
		ids[i] = it.ID
	}
	if err := r.deps.Store.SetQueueRanks(ctx, r.info.ID, ids); err != nil {
		return err
	}
	for i, it := range ordered {
		it.Rank = fmt.Sprintf("%08d", i+1)
	}
	r.queue = ordered
	return nil
}

// QueueRetry re-queues a failed ingest.
func (r *Room) QueueRetry(ctx context.Context, actor access.Actor, itemID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.ManageQueue); err != nil {
		return err
	}
	item := r.itemByID(itemID)
	if item == nil {
		return notFound("queue item")
	}
	m := r.media[item.MediaID]
	if m == nil || m.Status != entity.MediaFailed {
		return invalid("media is not in a failed state")
	}
	if err := r.deps.Admit.Retry(ctx, m); err != nil {
		return err
	}
	m.Status, m.Error, m.Progress = entity.MediaQueued, "", 0
	r.broadcastLocked()
	return nil
}

// MediaUpdated replaces a media row after an ingest progress event.
func (r *Room) MediaUpdated(m *entity.Media) {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.media[m.ID]
	if !ok {
		return
	}
	r.media[m.ID] = m

	// The current item just became playable: start it.
	if cur := r.currentMedia(); cur != nil && cur.ID == m.ID && m.IsReady() && !old.IsReady() && !r.playing && r.positionMs == 0 {
		r.setPlaybackLocked(true, 0)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.persistLocked(ctx); err != nil {
			r.deps.Logger.Error("persist playback", "room", r.info.Slug, "error", err)
		}
	}
	r.broadcastLocked()
}

// ReloadQueue re-reads the queue after rows changed outside the room
// (an administrator deleted a media item).
func (r *Room) ReloadQueue(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.reloadQueue(ctx); err != nil {
		return err
	}
	if r.current != nil && r.itemByID(*r.current) == nil {
		var next *entity.QueueItem
		if len(r.queue) > 0 {
			next = r.queue[0]
		}
		r.setCurrentLocked(next)
		if err := r.persistLocked(ctx); err != nil {
			return err
		}
	}
	r.broadcastLocked()
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
	info.CurrentItemID, info.Playing, info.PositionMs, info.PositionAt = r.current, r.playing, r.positionMs, r.positionAt
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
	if err := r.persistLocked(ctx); err != nil {
		return err
	}
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
	if r.playing && now.Sub(r.lastPersist) >= r.deps.PersistEvery {
		if err := r.persistLocked(ctx); err != nil {
			r.deps.Logger.Warn("persist playback", "room", r.info.Slug, "error", err)
		}
	}
	return !r.playing && len(r.viewers) == 0 && now.Sub(r.lastActive) > idleAfter
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

// closeLocked stops every timer and marks the room closed, so nothing
// scheduled acts on it once the manager has let go of it.
func (r *Room) closeLocked() {
	r.closed = true
	for _, t := range []*time.Timer{r.advance, r.emptyTimer, r.bufferTimer, r.waitTimer, r.countdownTimer} {
		if t != nil {
			t.Stop()
		}
	}
	for _, t := range r.leftTimers {
		t.Stop()
	}
}

func ptrVal(p *uuid.UUID) uuid.UUID {
	if p == nil {
		return uuid.Nil()
	}
	return *p
}

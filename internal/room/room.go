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
	"sort"
	"sync"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/mediastore"
	"github.com/anesthetised/couchcast/internal/protocol"
	"github.com/anesthetised/couchcast/internal/repository"
)

// Store is the persistence the room needs.
type Store interface {
	Pool() *pgxpool.Pool
	GetRoomByID(ctx context.Context, id uuid.UUID) (*entity.Room, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (*entity.User, error)
	ListQueue(ctx context.Context, roomID uuid.UUID) ([]entity.QueueItem, error)
	GetMediaBatch(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*entity.Media, error)
	GetMedia(ctx context.Context, id uuid.UUID) (*entity.Media, error)
	AddQueueItem(ctx context.Context, q repository.Querier, roomID, mediaID uuid.UUID, addedBy *uuid.UUID) (*entity.QueueItem, error)
	DeleteQueueItem(ctx context.Context, roomID, itemID uuid.UUID) error
	SetQueueRanks(ctx context.Context, roomID uuid.UUID, ordered []uuid.UUID) error
	UpdateRoomPlayback(ctx context.Context, roomID uuid.UUID, p entity.PlaybackState) error
	ToggleQueueVote(ctx context.Context, itemID, userID uuid.UUID) (bool, error)
	ListQueueVotesByUser(ctx context.Context, roomID, userID uuid.UUID) ([]uuid.UUID, error)
	UpdateRoomSettings(ctx context.Context, id uuid.UUID, settings entity.Settings) error
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
	// Persist bounds how often playback position is written while playing.
	PersistEvery time.Duration
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
}

// Room is the in-memory state of one room.
type Room struct {
	deps Deps

	mu    sync.Mutex
	info  *entity.Room
	owner string

	queue []*entity.QueueItem // rank order
	media map[uuid.UUID]*entity.Media

	current    *uuid.UUID
	playing    bool
	positionMs int64
	positionAt time.Time
	seq        uint64

	viewers    map[Conn]*viewer
	skipVotes  map[uuid.UUID]struct{}
	chatLimits map[uuid.UUID]*rate.Limiter

	advance     *time.Timer
	lastPersist time.Time
	lastActive  time.Time
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
		deps:       deps,
		info:       info,
		owner:      owner.Username,
		media:      map[uuid.UUID]*entity.Media{},
		viewers:    map[Conn]*viewer{},
		skipVotes:  map[uuid.UUID]struct{}{},
		chatLimits: map[uuid.UUID]*rate.Limiter{},
		current:    info.CurrentItemID,
		playing:    info.Playing,
		positionMs: info.PositionMs,
		positionAt: info.PositionAt,
		lastActive: deps.Now(),
	}

	if err := r.reloadQueue(ctx); err != nil {
		return nil, err
	}

	// A room restored mid-playback resumes from where it was; the clock
	// keeps running from the persisted timestamp.
	if r.current != nil && r.itemByID(*r.current) == nil {
		r.current, r.playing, r.positionMs = nil, false, 0
	}
	if r.playing {
		r.scheduleAdvanceLocked()
	}

	return r, nil
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
	ids := make([]uuid.UUID, 0, len(items))
	for i := range items {
		ids = append(ids, items[i].MediaID)
	}
	media, err := r.deps.Store.GetMediaBatch(ctx, ids)
	if err != nil {
		return err
	}

	r.queue = r.queue[:0]
	for i := range items {
		r.queue = append(r.queue, &items[i])
	}
	r.media = media
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
	pos := r.positionMs + t.Sub(r.positionAt).Milliseconds()
	if m := r.currentMedia(); m != nil && m.DurationMs > 0 && pos > m.DurationMs {
		return m.DurationMs
	}
	return pos
}

func (r *Room) playbackLocked() protocol.Playback {
	return protocol.Playback{
		Type: protocol.TypePlayback, ItemID: r.current, Playing: r.playing,
		PositionMs: r.positionMs, AtServerMs: r.positionAt.UnixMilli(), Rate: 1, Seq: r.seq,
	}
}

func (r *Room) stateLocked() entity.PlaybackState {
	return entity.PlaybackState{CurrentItemID: r.current, Playing: r.playing, PositionMs: r.positionMs, PositionAt: r.positionAt}
}

// setPlayback updates the clock: freezes the current position and restarts
// it from now with the new playing flag.
func (r *Room) setPlaybackLocked(playing bool, positionMs int64) {
	r.playing = playing
	r.positionMs = positionMs
	r.positionAt = r.now()
	r.seq++
	r.scheduleAdvanceLocked()
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
	remaining := time.Duration(m.DurationMs-r.positionLocked(r.now())) * time.Millisecond
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
	id := item.ID
	r.current = &id
	m := r.media[item.MediaID]
	r.setPlaybackLocked(m.IsReady(), 0)
}

// nextLocked drops the current item and advances to the following one.
func (r *Room) nextLocked(ctx context.Context) error {
	if r.current == nil {
		return errNoCurrent
	}
	idx := r.indexOf(*r.current)
	oldID := *r.current

	if err := r.deps.Store.DeleteQueueItem(ctx, r.info.ID, oldID); err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if idx >= 0 {
		r.queue = append(r.queue[:idx], r.queue[idx+1:]...)
	}

	var next *entity.QueueItem
	if idx >= 0 && idx < len(r.queue) {
		next = r.queue[idx]
	} else if len(r.queue) > 0 {
		next = r.queue[0]
	}
	r.setCurrentLocked(next)
	return r.persistLocked(ctx)
}

func (r *Room) persistLocked(ctx context.Context) error {
	r.lastPersist = r.now()
	return r.deps.Store.UpdateRoomPlayback(ctx, r.info.ID, r.stateLocked())
}

// --- snapshot & broadcast ----------------------------------------------------

func (r *Room) mediaInfoLocked(m *entity.Media) protocol.MediaInfo {
	if m == nil {
		return protocol.MediaInfo{}
	}
	info := protocol.MediaInfo{
		ID: m.ID, Title: m.Title, DurationMs: m.DurationMs, ThumbnailURL: m.ThumbnailURL,
		Status: m.Status, Progress: m.Progress, Error: m.Error, Renditions: m.Renditions, SourceURL: m.SourceURL,
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
			Settings: r.info.Settings, Owner: r.owner,
		},
		Playback: r.playbackLocked(),
		Queue:    make([]protocol.QueueEntry, 0, len(r.queue)),
		Members:  []protocol.Presence{},
	}
	snap.Playback.Type = ""

	for _, it := range r.queue {
		snap.Queue = append(snap.Queue, protocol.QueueEntry{
			ID: it.ID, Media: r.mediaInfoLocked(r.media[it.MediaID]), AddedBy: it.AddedByName, Votes: it.Votes,
			Current: r.current != nil && *r.current == it.ID,
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
			continue
		}
		seen[v.user.Username] = len(snap.Members)
		snap.Members = append(snap.Members, protocol.Presence{Username: v.user.Username, Role: v.role, Buffering: v.buffering})
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

	// Others learn about the new presence.
	for c, other := range r.viewers {
		if c != conn {
			other.conn.Send(r.personalizeLocked(base, other, r.votesFor(other)))
		}
	}
}

// Leave unregisters a connection.
func (r *Room) Leave(conn Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.viewers[conn]; !ok {
		return
	}
	delete(r.viewers, conn)
	r.lastActive = r.now()
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
	r.broadcastLocked()
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
	buffering := state == "buffering"
	if v.buffering != buffering {
		v.buffering = buffering
		r.broadcastLocked()
	}
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
	if r.playing {
		return nil
	}
	pos := r.positionMs
	if m.DurationMs > 0 && pos >= m.DurationMs {
		pos = 0
	}
	r.setPlaybackLocked(true, pos)
	if err := r.persistLocked(ctx); err != nil {
		return err
	}
	r.broadcastPlaybackLocked()
	return nil
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
		oldID := *r.current
		if err := r.deps.Store.DeleteQueueItem(ctx, r.info.ID, oldID); err != nil && !errors.Is(err, repository.ErrNotFound) {
			return err
		}
		if idx := r.indexOf(oldID); idx >= 0 {
			r.queue = append(r.queue[:idx], r.queue[idx+1:]...)
		}
	}
	r.setCurrentLocked(target)
	if err := r.persistLocked(ctx); err != nil {
		return err
	}
	r.broadcastLocked()
	return nil
}

// QueueAdd admits a URL and appends it to the queue.
func (r *Room) QueueAdd(ctx context.Context, actor access.Actor, rawURL string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.AddToQueue); err != nil {
		return err
	}
	if len(r.queue) >= 200 {
		return invalid("queue is full")
	}

	tx, err := r.deps.Store.Pool().Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	media, err := r.deps.Admit.EnsureMedia(ctx, tx, rawURL)
	if err != nil {
		return &Error{Code: protocol.CodeInvalid, Message: admitMessage(err)}
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

	if r.current == nil {
		r.setCurrentLocked(item)
		if err := r.persistLocked(ctx); err != nil {
			return err
		}
	}
	r.broadcastLocked()
	return nil
}

func admitMessage(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errUnsupported):
		return "this link is not supported"
	case errors.Is(err, errBlocked):
		return "this video has been blocked by an administrator"
	default:
		return "could not add this link"
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
	idx := r.indexOf(itemID)
	if idx < 0 {
		return notFound("queue item")
	}
	if r.current != nil && *r.current == itemID {
		return invalid("the current item cannot be moved")
	}

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
	r.broadcastLocked()
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

// Refresh reloads room metadata after a REST change (name, settings...).
func (r *Room) Refresh(ctx context.Context) error {
	info, err := r.deps.Store.GetRoomByID(ctx, r.info.ID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	info.CurrentItemID, info.Playing, info.PositionMs, info.PositionAt = r.current, r.playing, r.positionMs, r.positionAt
	r.info = info
	r.broadcastLocked()
	return nil
}

// Tick is called periodically by the manager: persists the running
// position and reports whether the room is idle enough to unload.
func (r *Room) Tick(ctx context.Context, idleAfter time.Duration) (idle bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if r.playing && now.Sub(r.lastPersist) >= r.deps.PersistEvery {
		if err := r.persistLocked(ctx); err != nil {
			r.deps.Logger.Warn("persist playback", "room", r.info.Slug, "error", err)
		}
	}
	return len(r.viewers) == 0 && now.Sub(r.lastActive) > idleAfter
}

// shutdown stops timers and persists state before the room is unloaded.
func (r *Room) shutdown(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.advance != nil {
		r.advance.Stop()
	}
	if err := r.persistLocked(ctx); err != nil {
		r.deps.Logger.Warn("persist playback on unload", "room", r.info.Slug, "error", err)
	}
}

func ptrVal(p *uuid.UUID) uuid.UUID {
	if p == nil {
		return uuid.Nil()
	}
	return *p
}

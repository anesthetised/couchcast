package room

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/mediastore"
	"github.com/anesthetised/couchcast/internal/protocol"
	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

// fakeConn records messages sent to a viewer.
type fakeConn struct {
	mu     sync.Mutex
	msgs   []any
	closed string
}

func (c *fakeConn) Send(msg any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, msg)
}

func (c *fakeConn) Close(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = reason
}

func (c *fakeConn) last() any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.msgs) == 0 {
		return nil
	}
	return c.msgs[len(c.msgs)-1]
}

func (c *fakeConn) lastSnapshot() protocol.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.msgs) - 1; i >= 0; i-- {
		switch m := c.msgs[i].(type) {
		case protocol.Snapshot:
			return m
		case protocol.Welcome:
			return m.Snapshot
		}
	}
	return protocol.Snapshot{}
}

// fakeAdmit creates media rows directly, standing in for ingest.
type fakeAdmit struct {
	repo    *repository.Repo
	retried []uuid.UUID
}

func (f *fakeAdmit) EnsureMedia(ctx context.Context, q repository.Querier, rawURL string) (*entity.Media, error) {
	if rawURL == "unsupported" {
		return nil, errUnsupported
	}
	m, _, err := f.repo.CreateMedia(ctx, q, "url:"+rawURL, rawURL)
	return m, err
}

func (f *fakeAdmit) Retry(_ context.Context, m *entity.Media) error {
	f.retried = append(f.retried, m.ID)
	return nil
}

type fixture struct {
	t     *testing.T
	repo  *repository.Repo
	admit *fakeAdmit
	now   time.Time
	deps  Deps
	owner access.Actor
	guest access.Actor
	room  *Room
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	repo := repository.New(repotest.Pool(t))
	f := &fixture{t: t, repo: repo, admit: &fakeAdmit{repo: repo}, now: time.Unix(1_800_000_000, 0)}

	owner, err := repo.CreateUser(ctx, "owner", "h")
	require.NoError(t, err)
	guest, err := repo.CreateUser(ctx, "guest", "h")
	require.NoError(t, err)
	rm, err := repo.CreateRoom(ctx, "sync-room", "Sync", owner.ID, entity.VisibilityPublic, entity.DefaultSettings())
	require.NoError(t, err)

	f.deps = Deps{
		Store: repo, Chat: repo, Admit: f.admit, Signer: mediastore.NewSigner("0123456789abcdef0123456789abcdef", time.Hour),
		Logger: slog.New(slog.DiscardHandler), Now: func() time.Time { return f.now }, PersistEvery: 5 * time.Second,
	}
	f.owner = access.Actor{User: owner, Member: &entity.RoomMember{Role: entity.RoomRoleOwner}}
	f.guest = access.Actor{User: guest}

	f.room, err = load(ctx, f.deps, rm.ID)
	require.NoError(t, err)
	return f
}

// ready marks the media of a URL playable with the given duration.
func (f *fixture) ready(url string, durationMs int64) *entity.Media {
	f.t.Helper()
	ctx := context.Background()
	m, _, err := f.repo.CreateMedia(ctx, f.repo.Pool(), "url:"+url, url)
	require.NoError(f.t, err)
	require.NoError(f.t, f.repo.SetMediaProbed(ctx, m.ID, "T "+url, durationMs, "", nil))
	require.NoError(f.t, f.repo.SetMediaReady(ctx, m.ID, []entity.Rendition{{ID: "0", Height: 720}}, 1, "p"))
	m, err = f.repo.GetMedia(ctx, m.ID)
	require.NoError(f.t, err)
	return m
}

func TestQueueAddStartsPlayback(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	conn := &fakeConn{}
	f.room.Join(ctx, conn, f.owner)

	// Media not ready: current is set but paused.
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	snap := conn.lastSnapshot()
	require.Len(t, snap.Queue, 1)
	assert.True(t, snap.Queue[0].Current)
	assert.False(t, snap.Playback.Playing)
	assert.Equal(t, "queued", string(snap.Queue[0].Media.Status))

	// Guests may add to public rooms with viewersCanAdd; anonymous may not.
	require.NoError(t, f.room.QueueAdd(ctx, f.guest, "https://b", false, false))
	err := f.room.QueueAdd(ctx, access.Actor{}, "https://c", false, false)
	require.ErrorAs(t, err, new(*Error))

	// Ingest finishes the first item: playback starts automatically.
	m := f.ready("https://a", 60_000)
	f.room.MediaUpdated(m)
	snap = conn.lastSnapshot()
	assert.True(t, snap.Playback.Playing)
	assert.NotEmpty(t, snap.Queue[0].Media.Manifest)
	assert.NotEmpty(t, snap.Queue[0].Media.Token)

	// Persisted.
	saved, err := f.repo.GetRoomByID(ctx, f.room.ID())
	require.NoError(t, err)
	assert.True(t, saved.Playing)
	assert.Equal(t, snap.Queue[0].ID, *saved.CurrentItemID)
}

func TestPlaybackClock(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	conn := &fakeConn{}
	f.room.Join(ctx, conn, f.owner)
	f.ready("https://a", 100_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://b", false, false))

	// Playing from 0 at t0.
	pb := conn.last().(protocol.Snapshot).Playback
	assert.True(t, pb.Playing)
	assert.EqualValues(t, 0, pb.PositionMs)
	assert.Equal(t, f.now.UnixMilli(), pb.AtServerMs)

	// Pause 10 s later freezes at 10 000.
	f.now = f.now.Add(10 * time.Second)
	require.NoError(t, f.room.Pause(ctx, f.owner))
	pb = conn.last().(protocol.Playback)
	assert.False(t, pb.Playing)
	assert.EqualValues(t, 10_000, pb.PositionMs)

	// Guests cannot control playback.
	assert.Error(t, f.room.Play(ctx, f.guest))
	assert.Error(t, f.room.Seek(ctx, f.guest, 5))

	// Seek while paused stays paused; seek clamps to duration.
	require.NoError(t, f.room.Seek(ctx, f.owner, 500_000))
	pb = conn.last().(protocol.Playback)
	assert.False(t, pb.Playing)
	assert.EqualValues(t, 100_000, pb.PositionMs)

	// Play at the end restarts from 0; seq increases monotonically.
	prevSeq := pb.Seq
	require.NoError(t, f.room.Play(ctx, f.owner))
	pb = conn.last().(protocol.Playback)
	assert.True(t, pb.Playing)
	assert.EqualValues(t, 0, pb.PositionMs)
	assert.Greater(t, pb.Seq, prevSeq)

	// Next drops the current item and moves on (b is not ready → paused).
	require.NoError(t, f.room.Next(ctx, f.owner))
	snap := conn.lastSnapshot()
	require.Len(t, snap.Queue, 1)
	assert.True(t, snap.Queue[0].Current)
	assert.False(t, snap.Playback.Playing)
	assert.Error(t, f.room.Play(ctx, f.owner), "play is rejected while media is not ready")
}

func TestPlayNotReady(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://x", false, false))
	err := f.room.Play(ctx, f.owner)
	var re *Error
	require.ErrorAs(t, err, &re)
	assert.Equal(t, protocol.CodeInvalid, re.Code)
}

func TestQueueRemoveMoveJumpRetry(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	conn := &fakeConn{}
	f.room.Join(ctx, conn, f.owner)
	for _, u := range []string{"https://a", "https://b", "https://c", "https://d"} {
		f.ready(u, 10_000)
		require.NoError(t, f.room.QueueAdd(ctx, f.owner, u, false, false))
	}
	ids := func() []string {
		snap := conn.lastSnapshot()
		out := make([]string, 0, len(snap.Queue))
		for _, q := range snap.Queue {
			out = append(out, q.Media.Title)
		}
		return out
	}
	assert.Equal(t, []string{"T https://a", "T https://b", "T https://c", "T https://d"}, ids())
	snap := conn.lastSnapshot()
	a, b, c, d := snap.Queue[0].ID, snap.Queue[1].ID, snap.Queue[2].ID, snap.Queue[3].ID

	// Move d after b; moving to the head lands after the current item.
	require.NoError(t, f.room.QueueMove(ctx, f.owner, d, &b))
	assert.Equal(t, []string{"T https://a", "T https://b", "T https://d", "T https://c"}, ids())
	require.NoError(t, f.room.QueueMove(ctx, f.owner, c, nil))
	assert.Equal(t, []string{"T https://a", "T https://c", "T https://b", "T https://d"}, ids())
	assert.Error(t, f.room.QueueMove(ctx, f.owner, a, &b), "current cannot move")
	assert.Error(t, f.room.QueueMove(ctx, f.guest, b, nil), "guests cannot reorder")

	// Persisted order survives a reload.
	reloaded, err := load(ctx, f.deps, f.room.ID())
	require.NoError(t, err)
	assert.Equal(t, c, reloaded.queue[1].ID)

	// Guests can remove only their own items.
	require.NoError(t, f.room.QueueAdd(ctx, f.guest, "https://e", false, false))
	e := conn.lastSnapshot().Queue[4].ID
	assert.Error(t, f.room.QueueRemove(ctx, f.guest, b))
	require.NoError(t, f.room.QueueRemove(ctx, f.guest, e))
	assert.Len(t, conn.lastSnapshot().Queue, 4)

	// Removing the current item advances to the next.
	require.NoError(t, f.room.QueueRemove(ctx, f.owner, a))
	snap = conn.lastSnapshot()
	assert.Equal(t, c, snap.Queue[0].ID)
	assert.True(t, snap.Queue[0].Current)

	// Jump to d drops c.
	require.NoError(t, f.room.Jump(ctx, f.owner, d))
	assert.Equal(t, []string{"T https://b", "T https://d"}, ids())
	assert.Equal(t, d, *conn.lastSnapshot().Playback.ItemID)

	// Retry only applies to failed media.
	assert.Error(t, f.room.QueueRetry(ctx, f.owner, b))
	require.NoError(t, f.repo.SetMediaFailed(ctx, f.room.media[f.room.itemByID(b).MediaID].ID, "boom"))
	f.room.media[f.room.itemByID(b).MediaID].Status = entity.MediaFailed
	require.NoError(t, f.room.QueueRetry(ctx, f.owner, b))
	assert.Len(t, f.admit.retried, 1)
	assert.Equal(t, "queued", string(conn.lastSnapshot().Queue[0].Media.Status))
}

func TestAutoAdvance(t *testing.T) {
	f := newFixture(t)
	f.deps.Now = time.Now // real clock for the timer
	ctx := context.Background()
	f.ready("https://short", 300)
	f.ready("https://next", 60_000)
	room, err := load(ctx, f.deps, f.room.ID())
	require.NoError(t, err)
	conn := &fakeConn{}
	room.Join(ctx, conn, f.owner)
	require.NoError(t, room.QueueAdd(ctx, f.owner, "https://short", false, false))
	require.NoError(t, room.QueueAdd(ctx, f.owner, "https://next", false, false))

	require.Eventually(t, func() bool {
		snap := conn.lastSnapshot()
		return len(snap.Queue) == 1 && snap.Queue[0].Media.Title == "T https://next" && snap.Playback.Playing
	}, 3*time.Second, 50*time.Millisecond, "advances to the next item when the first ends")
}

func TestPresenceAndKick(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	c1, c2, anon := &fakeConn{}, &fakeConn{}, &fakeConn{}
	f.room.Join(ctx, c1, f.owner)
	f.room.Join(ctx, c2, f.guest)
	f.room.Join(ctx, anon, access.Actor{})

	w := c1.msgs[0].(protocol.Welcome)
	assert.Equal(t, "owner", *w.Me)
	assert.Equal(t, entity.RoomRoleOwner, w.Role)

	snap := anon.lastSnapshot()
	assert.Len(t, snap.Members, 2)
	assert.Equal(t, 1, snap.Guests)

	f.room.Report(c2, "buffering", 0)
	snap = c1.lastSnapshot()
	assert.True(t, snap.Members[0].Buffering, "guest sorts before owner")

	f.room.Kick(f.guest.User.ID, "banned")
	assert.Equal(t, "banned", c2.closed)
	assert.IsType(t, protocol.Kicked{}, c2.msgs[len(c2.msgs)-1])
	assert.Len(t, c1.lastSnapshot().Members, 1)

	f.room.Leave(anon)
	assert.Equal(t, 0, c1.lastSnapshot().Guests)
	assert.Equal(t, 1, f.room.Viewers())
}

func TestPlayingRoomStaysLoaded(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.ready("https://a", 600_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))

	f.now = f.now.Add(time.Hour)
	assert.False(t, f.room.Tick(ctx, time.Minute), "playing with nobody watching is not idle")

	require.NoError(t, f.room.Pause(ctx, f.owner))
	f.now = f.now.Add(time.Hour)
	assert.True(t, f.room.Tick(ctx, time.Minute), "paused and empty is idle")
}

func TestRestoreFromDatabase(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.ready("https://a", 100_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	f.now = f.now.Add(7 * time.Second)
	require.NoError(t, f.room.Pause(ctx, f.owner))

	restored, err := load(ctx, f.deps, f.room.ID())
	require.NoError(t, err)
	pb := restored.playbackLocked()
	assert.False(t, pb.Playing)
	assert.EqualValues(t, 7_000, pb.PositionMs)
	assert.NotNil(t, pb.ItemID)
}

func TestQueueHistoryAndLoop(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	conn := &fakeConn{}
	f.room.Join(ctx, conn, f.owner)
	f.ready("https://a", 10_000)
	f.ready("https://b", 10_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://b", false, false))

	// Skipping moves the item into the history instead of dropping it.
	require.NoError(t, f.room.Next(ctx, f.owner))
	snap := conn.lastSnapshot()
	require.Len(t, snap.Queue, 1)
	require.Len(t, snap.Played, 1)
	assert.Equal(t, "T https://a", snap.Played[0].Media.Title)
	assert.Equal(t, f.now.UnixMilli(), snap.Played[0].PlayedMs)
	played, err := f.repo.ListPlayed(ctx, f.room.ID(), 10)
	require.NoError(t, err)
	assert.Len(t, played, 1)

	// Play next lands right after the current item.
	f.ready("https://c", 10_000)
	f.ready("https://d", 10_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://c", false, false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://d", true, false))
	titles := func() []string {
		queue := conn.lastSnapshot().Queue
		out := make([]string, 0, len(queue))
		for _, q := range queue {
			out = append(out, q.Media.Title)
		}
		return out
	}
	assert.Equal(t, []string{"T https://b", "T https://d", "T https://c"}, titles())

	// Replay re-queues a copy from the history; the history keeps it.
	require.NoError(t, f.room.QueueReplay(ctx, f.guest, snap.Played[0].ID))
	assert.Equal(t, []string{"T https://b", "T https://d", "T https://c", "T https://a"}, titles())
	assert.Len(t, conn.lastSnapshot().Played, 1)
	assert.Error(t, f.room.QueueReplay(ctx, f.owner, uuid.New()))

	// Without loop the queue runs dry; with loop it restarts in play order.
	for range 4 {
		f.now = f.now.Add(time.Second) // distinct played_at per item
		require.NoError(t, f.room.Next(ctx, f.owner))
	}
	snap = conn.lastSnapshot()
	assert.Empty(t, snap.Queue)
	assert.Nil(t, snap.Playback.ItemID)
	assert.Len(t, snap.Played, 5)

	on := true
	require.NoError(t, f.room.SettingsSet(ctx, f.owner, protocol.SettingsSet{Loop: &on}))
	require.NoError(t, f.room.QueueReplay(ctx, f.owner, snap.Played[0].ID)) // "a" again, starts playing
	f.now = f.now.Add(time.Second)
	require.NoError(t, f.room.Next(ctx, f.owner))
	snap = conn.lastSnapshot()
	assert.Equal(t, []string{"T https://a", "T https://b", "T https://d", "T https://c", "T https://a", "T https://a"}, titles(), "history back in play order")
	assert.Empty(t, snap.Played)
	require.NotNil(t, snap.Playback.ItemID)
	assert.True(t, snap.Queue[0].Current)

	// Clearing the history is a moderator action.
	require.NoError(t, f.room.Next(ctx, f.owner))
	assert.Error(t, f.room.QueueClearPlayed(ctx, f.guest))
	require.NoError(t, f.room.QueueClearPlayed(ctx, f.owner))
	assert.Empty(t, conn.lastSnapshot().Played)
	played, err = f.repo.ListPlayed(ctx, f.room.ID(), 10)
	require.NoError(t, err)
	assert.Empty(t, played)
}

func TestEndSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	c1, c2 := &fakeConn{}, &fakeConn{}
	f.room.Join(ctx, c1, f.owner)
	f.room.Join(ctx, c2, f.guest)
	f.ready("https://a", 10_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://b", false, false))

	assert.Error(t, f.room.EndSession(ctx, f.guest))
	require.NoError(t, f.room.EndSession(ctx, f.owner))
	assert.Equal(t, "session ended", c1.closed)
	assert.Equal(t, "session ended", c2.closed)
	assert.Equal(t, 0, f.room.Viewers())

	// Everything went to the history and nothing plays; the room remains.
	c3 := &fakeConn{}
	f.room.Join(ctx, c3, f.owner)
	snap := c3.lastSnapshot()
	assert.Empty(t, snap.Queue)
	assert.Len(t, snap.Played, 2)
	assert.Nil(t, snap.Playback.ItemID)
	assert.False(t, snap.Playback.Playing)
	saved, err := f.repo.GetRoomByID(ctx, f.room.ID())
	require.NoError(t, err)
	assert.Nil(t, saved.CurrentItemID)
}

func TestPlaybackRate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	conn := &fakeConn{}
	f.room.Join(ctx, conn, f.owner)
	f.ready("https://a", 100_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	assert.Error(t, f.room.SetRate(ctx, f.guest, 2))
	assert.Error(t, f.room.SetRate(ctx, f.owner, 3))

	// 10 s at 1×, then 10 s at 2× → 30 s.
	f.now = f.now.Add(10 * time.Second)
	require.NoError(t, f.room.SetRate(ctx, f.owner, 2))
	pb := conn.last().(protocol.Playback)
	assert.EqualValues(t, 10_000, pb.PositionMs)
	assert.EqualValues(t, 2, pb.Rate)
	f.now = f.now.Add(10 * time.Second)
	require.NoError(t, f.room.Pause(ctx, f.owner))
	pb = conn.last().(protocol.Playback)
	assert.EqualValues(t, 30_000, pb.PositionMs)

	// Persisted, and reset by the next item.
	saved, err := f.repo.GetRoomByID(ctx, f.room.ID())
	require.NoError(t, err)
	assert.EqualValues(t, 2, saved.Rate)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://b", false, false))
	require.NoError(t, f.room.Next(ctx, f.owner))
	assert.EqualValues(t, 1, conn.lastSnapshot().Playback.Rate)
}

func TestAdvanceSkipsFailedItems(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	conn := &fakeConn{}
	f.room.Join(ctx, conn, f.owner)
	f.ready("https://a", 10_000)
	f.ready("https://d", 10_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://b", false, false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://c", false, false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://d", false, false))
	for _, u := range []string{"https://b", "https://c"} {
		m, _, err := f.repo.CreateMedia(ctx, f.repo.Pool(), "url:"+u, u)
		require.NoError(t, err)
		require.NoError(t, f.repo.SetMediaFailed(ctx, m.ID, "boom"))
		m, err = f.repo.GetMedia(ctx, m.ID)
		require.NoError(t, err)
		f.room.MediaUpdated(m)
	}

	// Skipping "a" lands on "d": the two failed items go straight to the history.
	require.NoError(t, f.room.Next(ctx, f.owner))
	snap := conn.lastSnapshot()
	require.Len(t, snap.Queue, 1)
	assert.True(t, snap.Queue[0].Current)
	assert.Equal(t, "T https://d", snap.Queue[0].Media.Title)
	assert.True(t, snap.Playback.Playing)
	assert.Len(t, snap.Played, 3)
	msgs, err := f.repo.ListRecentMessages(ctx, f.room.ID(), 10)
	require.NoError(t, err)
	var skipped int
	for _, m := range msgs {
		if strings.Contains(m.Body, "(not playable)") {
			skipped++
		}
	}
	assert.Equal(t, 2, skipped)
}

func TestQueueDuplicatesClearShuffle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	conn := &fakeConn{}
	f.room.Join(ctx, conn, f.owner)
	for _, u := range []string{"https://a", "https://b", "https://c", "https://d"} {
		f.ready(u, 10_000)
		require.NoError(t, f.room.QueueAdd(ctx, f.owner, u, false, false))
	}

	// The same video again is refused with CodeDuplicate unless forced.
	err := f.room.QueueAdd(ctx, f.owner, "https://b", false, false)
	var re *Error
	require.ErrorAs(t, err, &re)
	assert.Equal(t, protocol.CodeDuplicate, re.Code)
	require.Len(t, conn.lastSnapshot().Queue, 4)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://b", false, true))
	require.Len(t, conn.lastSnapshot().Queue, 5)

	// Played videos count as duplicates too.
	require.NoError(t, f.room.Next(ctx, f.owner)) // a → history
	err = f.room.QueueAdd(ctx, f.owner, "https://a", false, false)
	require.ErrorAs(t, err, &re)
	assert.Equal(t, protocol.CodeDuplicate, re.Code)

	// Shuffle keeps the current item first and persists the order.
	require.NoError(t, f.room.QueueShuffle(ctx, f.owner))
	snap := conn.lastSnapshot()
	assert.True(t, snap.Queue[0].Current)
	assert.Equal(t, "T https://b", snap.Queue[0].Media.Title)
	saved, err := f.repo.ListQueue(ctx, f.room.ID())
	require.NoError(t, err)
	for i, it := range saved {
		assert.Equal(t, snap.Queue[i].ID, it.ID)
	}
	assert.Error(t, f.room.QueueShuffle(ctx, f.guest))

	// Clear drops the waiting items; the current one keeps playing.
	require.NoError(t, f.room.QueueClear(ctx, f.owner))
	snap = conn.lastSnapshot()
	require.Len(t, snap.Queue, 1)
	assert.True(t, snap.Queue[0].Current)
	assert.True(t, snap.Playback.Playing)
	saved, err = f.repo.ListQueue(ctx, f.room.ID())
	require.NoError(t, err)
	assert.Len(t, saved, 1)
	assert.Len(t, snap.Played, 1, "history untouched")
}

func TestQueueAddMany(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	conn := &fakeConn{}
	f.room.Join(ctx, conn, f.owner)
	for _, u := range []string{"https://a", "https://b", "https://p1", "https://p2", "https://p3"} {
		f.ready(u, 10_000)
	}
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://b", false, false))
	titles := func() []string {
		queue := conn.lastSnapshot().Queue
		out := make([]string, 0, len(queue))
		for _, q := range queue {
			out = append(out, q.Media.Title)
		}
		return out
	}

	// Next: the batch lands after the current item, in order; the
	// duplicate and the unsupported link are skipped and the room says so.
	require.NoError(t, f.room.QueueAddMany(ctx, f.owner, []string{"https://p1", "https://b", "unsupported", "https://p2", "https://p1"}, true))
	assert.Equal(t, []string{"T https://a", "T https://p1", "T https://p2", "T https://b"}, titles())
	saved, err := f.repo.ListQueue(ctx, f.room.ID())
	require.NoError(t, err)
	require.Len(t, saved, 4)
	assert.Equal(t, conn.lastSnapshot().Queue[1].ID, saved[1].ID, "ranks persisted")
	msgs, err := f.repo.ListRecentMessages(ctx, f.room.ID(), 10)
	require.NoError(t, err)
	assert.Contains(t, msgs[len(msgs)-1].Body, "added 2 videos from a playlist (3 skipped)")

	// Without next the batch goes to the end; nothing new is an error.
	require.NoError(t, f.room.QueueAddMany(ctx, f.owner, []string{"https://p3"}, false))
	assert.Equal(t, "T https://p3", titles()[4])
	var re *Error
	require.ErrorAs(t, f.room.QueueAddMany(ctx, f.owner, []string{"https://p3", "unsupported"}, false), &re)
	assert.Equal(t, protocol.CodeDuplicate, re.Code)

	// Limits and permissions.
	assert.Error(t, f.room.QueueAddMany(ctx, f.owner, nil, false))
	assert.Error(t, f.room.QueueAddMany(ctx, f.owner, make([]string, maxAddMany+1), false))
	assert.Error(t, f.room.QueueAddMany(ctx, access.Actor{}, []string{"https://x"}, false))
}

func TestLoopPausesWhenNobodyWatches(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	conn := &fakeConn{}
	f.room.Join(ctx, conn, f.owner)
	f.ready("https://a", 10_000)
	f.ready("https://b", 10_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://b", false, false))
	on := true
	require.NoError(t, f.room.SettingsSet(ctx, f.owner, protocol.SettingsSet{Loop: &on}))

	// Everyone leaves; the queue runs out: it restarts at the top but
	// waits instead of cycling on.
	f.room.Leave(conn)
	require.NoError(t, f.room.Next(ctx, f.owner))
	f.now = f.now.Add(time.Second)
	require.NoError(t, f.room.Next(ctx, f.owner))
	pb := f.room.Playback()
	assert.False(t, pb.Playing)
	require.NotNil(t, pb.ItemID)
	f.room.mu.Lock()
	assert.Equal(t, "T https://a", f.room.media[f.room.queue[0].MediaID].Title)
	assert.Equal(t, f.room.queue[0].ID, *f.room.current)
	f.room.mu.Unlock()
	msgs, err := f.repo.ListRecentMessages(ctx, f.room.ID(), 5)
	require.NoError(t, err)
	assert.Equal(t, "queue restarted from the top, paused until someone is back", msgs[len(msgs)-1].Body)

	// Someone back: play resumes the loop as usual.
	back := &fakeConn{}
	f.room.Join(ctx, back, f.owner)
	require.NoError(t, f.room.Play(ctx, f.owner))
	assert.True(t, f.room.Playback().Playing)
}

func TestPauseWhenEveryoneLeaves(t *testing.T) {
	f := newFixture(t)
	f.room.deps.RejoinGrace = 30 * time.Millisecond
	ctx := context.Background()
	conn := &fakeConn{}
	f.room.Join(ctx, conn, f.owner)
	f.ready("https://a", 600_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	require.True(t, f.room.Playback().Playing)

	// Leaving and coming back inside the grace period changes nothing.
	f.room.Leave(conn)
	back := &fakeConn{}
	f.room.Join(ctx, back, f.owner)
	time.Sleep(80 * time.Millisecond)
	assert.True(t, f.room.Playback().Playing)

	// Gone for good: paused where the clock was, logged once.
	f.room.Leave(back)
	f.now = f.now.Add(5 * time.Second)
	require.Eventually(t, func() bool { return !f.room.Playback().Playing }, time.Second, 10*time.Millisecond)
	assert.EqualValues(t, 5000, f.room.Playback().PositionMs)
	msgs, err := f.repo.ListRecentMessages(ctx, f.room.ID(), 5)
	require.NoError(t, err)
	bodies := make([]string, 0, len(msgs))
	for _, m := range msgs {
		bodies = append(bodies, m.Body)
	}
	assert.Contains(t, bodies, "paused: everyone left") // next to "owner left", same grace
	saved, err := f.repo.GetRoomByID(ctx, f.room.ID())
	require.NoError(t, err)
	assert.False(t, saved.Playing)

	// With the setting off the room plays on.
	off := false
	again := &fakeConn{}
	f.room.Join(ctx, again, f.owner)
	require.NoError(t, f.room.SettingsSet(ctx, f.owner, protocol.SettingsSet{PauseWhenEmpty: &off}))
	require.NoError(t, f.room.Play(ctx, f.owner))
	f.room.Leave(again)
	time.Sleep(80 * time.Millisecond)
	assert.True(t, f.room.Playback().Playing)
}

func TestWaitForBuffering(t *testing.T) {
	f := newFixture(t)
	f.room.deps.WaitScale = 0.01 // 4 s → 40 ms, 30 s → 300 ms
	ctx := context.Background()
	owner, guest := &fakeConn{}, &fakeConn{}
	f.room.Join(ctx, owner, f.owner)
	f.room.Join(ctx, guest, f.guest)
	f.ready("https://a", 600_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	require.True(t, f.room.Playback().Playing)

	// A short stall does not pause the room.
	f.room.Report(guest, "buffering", 1000)
	f.room.Report(guest, "playing", 1000)
	time.Sleep(80 * time.Millisecond)
	assert.True(t, f.room.Playback().Playing)

	// A long one does; everyone sees who the room waits for; it resumes
	// by itself when they are ready.
	f.room.Report(guest, "buffering", 2000)
	require.Eventually(t, func() bool { return !f.room.Playback().Playing }, time.Second, 5*time.Millisecond)
	assert.Equal(t, []string{"guest"}, owner.lastSnapshot().Waiting)
	f.room.Report(guest, "playing", 2000)
	assert.True(t, f.room.Playback().Playing)
	assert.Empty(t, owner.lastSnapshot().Waiting)

	// Stuck for good: after the cap the room goes on without them and
	// does not wait for the same stall again.
	f.room.Report(guest, "buffering", 3000)
	require.Eventually(t, func() bool { return !f.room.Playback().Playing }, time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool { return f.room.Playback().Playing }, 2*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	assert.True(t, f.room.Playback().Playing, "an ignored viewer does not stall the room again")
	msgs, err := f.repo.ListRecentMessages(ctx, f.room.ID(), 10)
	require.NoError(t, err)
	bodies := make([]string, 0, len(msgs))
	for _, m := range msgs {
		bodies = append(bodies, m.Body)
	}
	assert.Contains(t, bodies, "waiting for guest")
	assert.Contains(t, bodies, "continuing without guest")

	// A moderator's pause ends the wait without resuming.
	f.room.Report(guest, "playing", 4000)
	f.room.Report(guest, "buffering", 4000)
	require.Eventually(t, func() bool { return !f.room.Playback().Playing }, time.Second, 5*time.Millisecond)
	require.NoError(t, f.room.Pause(ctx, f.owner))
	f.room.Report(guest, "playing", 4000)
	assert.False(t, f.room.Playback().Playing)
	assert.Empty(t, owner.lastSnapshot().Waiting)
}

func TestViewerLag(t *testing.T) {
	assert.EqualValues(t, 0, lagOf(900))
	assert.EqualValues(t, 0, lagOf(-999))
	assert.EqualValues(t, 1000, lagOf(1100))
	assert.EqualValues(t, 2500, lagOf(2400))
	assert.EqualValues(t, -1500, lagOf(-1600))

	f := newFixture(t)
	ctx := context.Background()
	owner, guest := &fakeConn{}, &fakeConn{}
	f.room.Join(ctx, owner, f.owner)
	f.room.Join(ctx, guest, f.guest)
	f.ready("https://a", 600_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	f.now = f.now.Add(10 * time.Second)

	lag := func() int64 {
		for _, m := range owner.lastSnapshot().Members {
			if m.Username == "guest" {
				return m.LagMs
			}
		}
		return -1
	}
	f.room.Report(guest, "playing", 9_500) // half a second behind: in sync
	assert.EqualValues(t, 0, lag())
	n := len(owner.msgs)
	f.room.Report(guest, "playing", 7_400) // 2.6 s behind
	assert.EqualValues(t, 2500, lag())
	assert.Greater(t, len(owner.msgs), n, "a change is broadcast")
	n = len(owner.msgs)
	f.room.Report(guest, "playing", 7_450) // same bucket: no broadcast
	assert.Len(t, owner.msgs, n)
}

func TestFairQueue(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	conn := &fakeConn{}
	f.room.Join(ctx, conn, f.owner)
	for _, u := range []string{"https://a1", "https://a2", "https://a3", "https://g1", "https://g2"} {
		f.ready(u, 60_000)
	}
	for _, u := range []string{"https://a1", "https://a2", "https://a3"} {
		require.NoError(t, f.room.QueueAdd(ctx, f.owner, u, false, false))
	}
	require.NoError(t, f.room.QueueAdd(ctx, f.guest, "https://g1", false, false))
	titles := func() []string {
		queue := conn.lastSnapshot().Queue
		out := make([]string, 0, len(queue))
		for _, q := range queue {
			out = append(out, strings.TrimPrefix(q.Media.Title, "T https://"))
		}
		return out
	}
	assert.Equal(t, []string{"a1", "a2", "a3", "g1"}, titles(), "arrival order while off")

	// On: the guest gets the next turn, since the owner's video is playing.
	on := true
	require.NoError(t, f.room.SettingsSet(ctx, f.owner, protocol.SettingsSet{FairQueue: &on}))
	assert.Equal(t, []string{"a1", "g1", "a2", "a3"}, titles())
	saved, err := f.repo.ListQueue(ctx, f.room.ID())
	require.NoError(t, err)
	assert.Equal(t, conn.lastSnapshot().Queue[1].ID, saved[1].ID, "ranks persisted")

	// New videos take their place in the rotation; "play next" does not jump it.
	require.NoError(t, f.room.QueueAdd(ctx, f.guest, "https://g2", true, false))
	assert.Equal(t, []string{"a1", "g1", "a2", "g2", "a3"}, titles())

	// Manual ordering is refused while turns decide.
	var re *Error
	require.ErrorAs(t, f.room.QueueMove(ctx, f.owner, conn.lastSnapshot().Queue[4].ID, nil), &re)
	assert.Contains(t, re.Message, "fair queue")
	assert.Error(t, f.room.QueueShuffle(ctx, f.owner))

	// Advancing keeps the turns stable.
	require.NoError(t, f.room.Next(ctx, f.owner))
	assert.Equal(t, []string{"g1", "a2", "g2", "a3"}, titles())
}

func TestCountdown(t *testing.T) {
	f := newFixture(t)
	f.room.deps.WaitScale = 0.01 // 3 s → 30 ms
	ctx := context.Background()
	conn := &fakeConn{}
	f.room.Join(ctx, conn, f.owner)
	f.ready("https://a", 600_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	require.NoError(t, f.room.Pause(ctx, f.owner))

	// Everyone learns when the counted start begins; then it plays.
	require.NoError(t, f.room.PlayCountdown(ctx, f.owner))
	assert.False(t, f.room.Playback().Playing)
	assert.Equal(t, f.now.Add(3*time.Second).UnixMilli(), conn.lastSnapshot().CountdownMs)
	require.Eventually(t, func() bool { return f.room.Playback().Playing }, time.Second, 5*time.Millisecond)
	assert.Zero(t, conn.lastSnapshot().CountdownMs)

	// A pause during the countdown cancels it.
	require.NoError(t, f.room.Pause(ctx, f.owner))
	require.NoError(t, f.room.PlayCountdown(ctx, f.owner))
	require.NoError(t, f.room.Pause(ctx, f.owner))
	time.Sleep(60 * time.Millisecond)
	assert.False(t, f.room.Playback().Playing)
	assert.Zero(t, conn.lastSnapshot().CountdownMs)

	// A room with an announced session always counts down.
	at := f.now.Add(time.Hour)
	f.room.mu.Lock()
	f.room.info.ScheduledAt = &at
	f.room.mu.Unlock()
	require.NoError(t, f.room.Play(ctx, f.owner))
	assert.NotZero(t, conn.lastSnapshot().CountdownMs)
	require.Eventually(t, func() bool { return f.room.Playback().Playing }, time.Second, 5*time.Millisecond)
}

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
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false))
	snap := conn.lastSnapshot()
	require.Len(t, snap.Queue, 1)
	assert.True(t, snap.Queue[0].Current)
	assert.False(t, snap.Playback.Playing)
	assert.Equal(t, "queued", string(snap.Queue[0].Media.Status))

	// Guests may add to public rooms with viewersCanAdd; anonymous may not.
	require.NoError(t, f.room.QueueAdd(ctx, f.guest, "https://b", false))
	err := f.room.QueueAdd(ctx, access.Actor{}, "https://c", false)
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
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://b", false))

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
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://x", false))
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
		require.NoError(t, f.room.QueueAdd(ctx, f.owner, u, false))
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
	require.NoError(t, f.room.QueueAdd(ctx, f.guest, "https://e", false))
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
	require.NoError(t, room.QueueAdd(ctx, f.owner, "https://short", false))
	require.NoError(t, room.QueueAdd(ctx, f.owner, "https://next", false))

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
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false))

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
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false))
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
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://b", false))

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
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://c", false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://d", true))
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
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://b", false))

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
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false))
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
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://b", false))
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
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://b", false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://c", false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://d", false))
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

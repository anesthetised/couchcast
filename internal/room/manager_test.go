package room

import (
	"context"
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/ingest"
	"github.com/anesthetised/couchcast/internal/protocol"
)

// newManager builds a manager over the fixture's database and clock.
func (f *fixture) newManager() *Manager { return NewManager(f.deps) }

func TestManagerLoadsOnDemand(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	m := f.newManager()
	id := f.room.ID()

	_, ok := m.Peek(id)
	assert.False(t, ok)
	_, ok = m.Playback(id)
	assert.False(t, ok)
	_, ok = m.Debug(id)
	assert.False(t, ok)

	r, err := m.Get(ctx, id)
	require.NoError(t, err)
	again, err := m.Get(ctx, id)
	require.NoError(t, err)
	assert.Same(t, r, again, "one live instance per room")
	assert.Equal(t, 1, m.Loaded())

	_, err = m.Get(ctx, uuid.New())
	require.Error(t, err)
	assert.Equal(t, 1, m.Loaded())

	// Live counts only list rooms with viewers.
	assert.Empty(t, m.LiveCounts())
	c := &fakeConn{}
	r.Join(ctx, c, f.guest)
	assert.Equal(t, map[uuid.UUID]int{id: 1}, m.LiveCounts())

	pb, ok := m.Playback(id)
	require.True(t, ok)
	assert.False(t, pb.Playing)
	dbg, ok := m.Debug(id)
	require.True(t, ok)
	assert.Equal(t, 1, dbg.Viewers)
}

func TestManagerKickAndLog(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	m := f.newManager()
	r, err := m.Get(ctx, f.room.ID())
	require.NoError(t, err)
	guest, owner := &fakeConn{}, &fakeConn{}
	r.Join(ctx, guest, f.guest)
	r.Join(ctx, owner, f.owner)

	m.Log(ctx, f.room.ID(), "owner renamed the room")
	line, ok := owner.last().(protocol.ChatMessage)
	require.True(t, ok)
	assert.True(t, line.System)
	assert.Equal(t, "owner renamed the room", line.Body)

	m.Kick(f.room.ID(), f.guest.User.ID, "removed")
	assert.Equal(t, "removed", guest.closed)
	assert.Empty(t, owner.closed)

	// Unloaded rooms are left alone.
	m.Kick(uuid.New(), f.owner.User.ID, "x")
	m.Log(ctx, uuid.New(), "x")

	m.KickEverywhere(f.owner.User.ID, "banned")
	assert.Equal(t, "banned", owner.closed)
	assert.Equal(t, 0, r.Viewers())
}

func TestManagerWarmResumesPlayingRooms(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.ready("https://a", 60_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	require.True(t, f.room.Playback().Playing)

	m := f.newManager()
	require.NoError(t, m.Warm(ctx))
	r, ok := m.Peek(f.room.ID())
	require.True(t, ok, "a room that was playing is loaded at start")
	assert.True(t, r.Playback().Playing)
}

func TestManagerMediaChanges(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	m := f.newManager()
	r, err := m.Get(ctx, f.room.ID())
	require.NoError(t, err)
	c := &fakeConn{}
	r.Join(ctx, c, f.owner)
	require.NoError(t, r.QueueAdd(ctx, f.owner, "https://a", false, false))
	require.NoError(t, r.QueueAdd(ctx, f.owner, "https://b", false, false))
	snap := c.lastSnapshot()
	require.Len(t, snap.Queue, 2)
	mediaA := snap.Queue[0].Media.ID

	// Progress for media nobody has queued is ignored; for queued media
	// the room gets the new row and starts playing once it is ready.
	m.mediaChanged(ctx, uuid.New())
	f.ready("https://a", 60_000)
	m.mediaChanged(ctx, mediaA)
	assert.True(t, c.lastSnapshot().Playback.Playing)

	// A deleted media drops out of the loaded room's queue.
	require.NoError(t, f.repo.DeleteMedia(ctx, mediaA))
	m.MediaDeleted(ctx, mediaA)
	snap = c.lastSnapshot()
	require.Len(t, snap.Queue, 1)
	assert.Equal(t, "https://b", snap.Queue[0].Media.SourceURL)
}

func TestManagerListenProgress(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := f.newManager()
	r, err := m.Get(ctx, f.room.ID())
	require.NoError(t, err)
	c := &fakeConn{}
	r.Join(ctx, c, f.owner)
	require.NoError(t, r.QueueAdd(ctx, f.owner, "https://a", false, false))
	mediaID := c.lastSnapshot().Queue[0].Media.ID.String()

	done := make(chan error, 1)
	go func() { done <- m.ListenProgress(ctx, f.repo.Pool()) }()
	f.ready("https://a", 60_000)
	// LISTEN may not be registered yet: notify until the room reacts.
	require.Eventually(t, func() bool {
		_, err := f.repo.Pool().Exec(ctx, `SELECT pg_notify($1, $2)`, ingest.ProgressChannel, mediaID)
		require.NoError(t, err)
		return r.Playback().Playing
	}, 5*time.Second, 50*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ListenProgress did not stop")
	}
}

func TestManagerRunUnloadsIdleRooms(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.deps.PersistEvery = 10 * time.Millisecond
	m := f.newManager()
	m.IdleAfter = time.Minute
	r, err := m.Get(ctx, f.room.ID())
	require.NoError(t, err)
	c := &fakeConn{}
	r.Join(ctx, c, f.owner)
	r.Leave(c)

	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, m.Loaded(), "not idle long enough")
	f.advance(2 * time.Minute)
	require.Eventually(t, func() bool { return m.Loaded() == 0 }, 2*time.Second, 10*time.Millisecond)

	// Shutdown persists and drops whatever is still loaded.
	_, err = m.Get(ctx, f.room.ID())
	require.NoError(t, err)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	assert.Equal(t, 0, m.Loaded())
}

func TestManagerUnloadStopsTheRoom(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.deps.WaitScale = 0.01 // the 3 s countdown takes 30 ms
	m := f.newManager()
	r, err := m.Get(ctx, f.room.ID())
	require.NoError(t, err)
	c := &fakeConn{}
	r.Join(ctx, c, f.owner)
	f.ready("https://a", 60_000)
	require.NoError(t, r.QueueAdd(ctx, f.owner, "https://a", false, false))
	require.NoError(t, r.Pause(ctx, f.owner))
	require.NoError(t, r.PlayCountdown(ctx, f.owner))

	m.Unload(f.room.ID(), "room deleted")
	assert.Equal(t, "room deleted", c.closed)
	assert.Equal(t, 0, m.Loaded())
	m.Unload(f.room.ID(), "again") // no-op

	// The pending countdown must not start an unloaded room.
	time.Sleep(100 * time.Millisecond)
	saved, err := f.repo.GetRoomByID(ctx, f.room.ID())
	require.NoError(t, err)
	assert.False(t, saved.Playing)
}

func TestManagerRefresh(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	m := f.newManager()
	r, err := m.Get(ctx, f.room.ID())
	require.NoError(t, err)
	c := &fakeConn{}
	r.Join(ctx, c, f.owner)

	_, err = f.repo.UpdateRoom(ctx, f.room.ID(), "sync-room", "Renamed", entity.VisibilityPublic, "", nil)
	require.NoError(t, err)
	m.Refresh(ctx, f.room.ID())
	assert.Equal(t, "Renamed", c.lastSnapshot().Room.Name)
	m.Refresh(ctx, uuid.New()) // not loaded: nothing to do
}

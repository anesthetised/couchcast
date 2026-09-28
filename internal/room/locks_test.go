package room

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/protocol"
	"github.com/anesthetised/couchcast/internal/repository"
)

// countingStore counts the vote reads and can hold a room's load.
type countingStore struct {
	*repository.Repo
	voteReads atomic.Int32
	roomLoads atomic.Int32
	holdRoom  uuid.UUID     // GetRoomByID of this room waits for release
	release   chan struct{} // closed to let held loads through
}

func (s *countingStore) ListQueueVotes(ctx context.Context, roomID uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	s.voteReads.Add(1)
	return s.Repo.ListQueueVotes(ctx, roomID)
}

func (s *countingStore) GetRoomByID(ctx context.Context, id uuid.UUID) (*entity.Room, error) {
	if id == s.holdRoom {
		s.roomLoads.Add(1)
		<-s.release
	}
	return s.Repo.GetRoomByID(ctx, id)
}

// In vote mode every broadcast is personalised with the viewer's votes;
// they come from memory, not from a query per viewer (#121).
func TestVotesAreNotQueriedPerViewer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	store := &countingStore{Repo: f.repo}
	f.deps.Store = store
	m := f.newManager()
	r, err := m.Get(ctx, f.room.ID())
	require.NoError(t, err)
	require.EqualValues(t, 1, store.voteReads.Load(), "read once with the queue")

	on := true
	require.NoError(t, r.SettingsSet(ctx, f.owner, protocol.SettingsSet{VoteMode: &on}))
	for _, u := range []string{"https://a", "https://b"} {
		f.ready(u, 10_000)
		require.NoError(t, r.QueueAdd(ctx, f.owner, u, false, false))
	}

	conns := make([]*fakeConn, 20)
	for i := range conns {
		u, err := f.repo.CreateUser(ctx, fmt.Sprintf("viewer%d", i), "h")
		require.NoError(t, err)
		conns[i] = &fakeConn{}
		r.Join(ctx, conns[i], access.Actor{User: u})
	}
	voter := &fakeConn{}
	r.Join(ctx, voter, f.guest)
	b := voter.lastSnapshot().Queue[1].ID
	require.NoError(t, r.QueueVote(ctx, f.guest, b))
	require.NoError(t, r.Pause(ctx, f.owner))
	require.NoError(t, r.Play(ctx, f.owner))

	assert.EqualValues(t, 1, store.voteReads.Load(), "no vote query for any of the broadcasts")
	assert.True(t, voter.lastSnapshot().Queue[1].Voted)
	for _, c := range conns {
		assert.False(t, c.lastSnapshot().Queue[1].Voted, "votes stay personal")
	}

	// A reconnect sees the vote from memory; unvoting clears it.
	r.Leave(voter)
	again := &fakeConn{}
	r.Join(ctx, again, f.guest)
	assert.True(t, again.lastSnapshot().Queue[1].Voted)
	require.NoError(t, r.QueueVote(ctx, f.guest, b))
	assert.False(t, again.lastSnapshot().Queue[1].Voted)
	require.NoError(t, r.QueueVote(ctx, f.guest, b))

	// A fresh load reads the votes back from the database.
	fresh, err := load(ctx, f.deps, f.room.ID())
	require.NoError(t, err)
	late := &fakeConn{}
	fresh.Join(ctx, late, f.guest)
	assert.True(t, late.lastSnapshot().Queue[1].Voted)
}

// URL admission can wait on DNS; the room keeps serving commands meanwhile.
func TestSlowAdmissionDoesNotBlockTheRoom(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.ready("https://a", 60_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	c := &fakeConn{}
	f.room.Join(ctx, c, f.owner)

	f.admit.slow = make(chan struct{})
	f.admit.admitting = make(chan struct{}, 1)
	added := make(chan error, 1)
	go func() { added <- f.room.QueueAdd(ctx, f.owner, "https://slow/", false, false) }()
	<-f.admit.admitting

	done := make(chan error, 1)
	go func() {
		if err := f.room.Pause(ctx, f.owner); err != nil {
			done <- err
			return
		}
		done <- f.room.Play(ctx, f.owner)
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("playback waited for the admission of another link")
	}

	close(f.admit.slow)
	require.NoError(t, <-added)
	assert.Len(t, c.lastSnapshot().Queue, 2)
}

// A room whose load hangs on the database holds up nobody else, and
// concurrent first requests share one load and one instance.
func TestSlowRoomLoadDoesNotBlockTheManager(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	other, err := f.repo.CreateRoom(ctx, "other-room", "Other", f.owner.User.ID, entity.VisibilityPublic, entity.DefaultSettings())
	require.NoError(t, err)
	store := &countingStore{Repo: f.repo, holdRoom: f.room.ID(), release: make(chan struct{})}
	f.deps.Store = store
	m := f.newManager()

	var wg sync.WaitGroup
	got := make([]*Room, 10)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := m.Get(ctx, f.room.ID())
			assert.NoError(t, err)
			got[i] = r
		}()
	}
	require.Eventually(t, func() bool { return store.roomLoads.Load() == 1 }, 2*time.Second, 5*time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := m.Get(ctx, other.ID)
		assert.NoError(t, err)
		_, _ = m.Peek(f.room.ID())
		_ = m.LiveCounts()
		m.mediaChanged(ctx, uuid.New())
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the manager waited for another room's load")
	}

	close(store.release)
	wg.Wait()
	assert.EqualValues(t, 1, store.roomLoads.Load(), "one load for all first requests")
	for _, r := range got {
		assert.Same(t, got[0], r, "one live instance")
	}
	assert.Equal(t, 2, m.Loaded())
}

// A waiter gives up with its own context; the load goes on for the others.
func TestRoomLoadWaiterCancels(t *testing.T) {
	f := newFixture(t)
	store := &countingStore{Repo: f.repo, holdRoom: f.room.ID(), release: make(chan struct{})}
	f.deps.Store = store
	m := f.newManager()

	first := make(chan *Room, 1)
	go func() {
		r, err := m.Get(context.Background(), f.room.ID())
		assert.NoError(t, err)
		first <- r
	}()
	require.Eventually(t, func() bool { return store.roomLoads.Load() == 1 }, 2*time.Second, 5*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := m.Get(ctx, f.room.ID())
	assert.ErrorIs(t, err, context.DeadlineExceeded)

	close(store.release)
	r := <-first
	again, err := m.Get(context.Background(), f.room.ID())
	require.NoError(t, err)
	assert.Same(t, r, again)
}

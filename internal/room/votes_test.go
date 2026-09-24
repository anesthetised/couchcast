package room

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/protocol"
)

func TestVoteMode(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	third, err := f.repo.CreateUser(ctx, "third", "h")
	require.NoError(t, err)
	thirdActor := access.Actor{User: third}

	oc, gc, tc := &fakeConn{}, &fakeConn{}, &fakeConn{}
	f.room.Join(ctx, oc, f.owner)
	f.room.Join(ctx, gc, f.guest)
	f.room.Join(ctx, tc, thirdActor)

	for _, u := range []string{"https://a", "https://b", "https://c"} {
		f.ready(u, 10_000)
		require.NoError(t, f.room.QueueAdd(ctx, f.owner, u, false, false))
	}
	titles := func(c *fakeConn) []string {
		snap := c.lastSnapshot()
		out := make([]string, 0, len(snap.Queue))
		for _, q := range snap.Queue {
			out = append(out, q.Media.Title)
		}
		return out
	}
	snap := oc.lastSnapshot()
	b, c := snap.Queue[1].ID, snap.Queue[2].ID

	// Votes are rejected while vote mode is off; only moderators toggle it.
	assert.Error(t, f.room.QueueVote(ctx, f.guest, c))
	on := true
	assert.Error(t, f.room.SettingsSet(ctx, f.guest, protocol.SettingsSet{VoteMode: &on}))
	bad := 1.5
	assert.Error(t, f.room.SettingsSet(ctx, f.owner, protocol.SettingsSet{SkipThreshold: &bad}))
	half := 0.5
	require.NoError(t, f.room.SettingsSet(ctx, f.owner, protocol.SettingsSet{VoteMode: &on, SkipThreshold: &half}))
	assert.True(t, gc.lastSnapshot().Room.Settings.VoteMode)
	assert.Equal(t, 2, gc.lastSnapshot().SkipNeeded, "ceil(3 users × 0.5)")

	// Upvoting c moves it ahead of b; the current item stays first.
	require.NoError(t, f.room.QueueVote(ctx, f.guest, c))
	assert.Equal(t, []string{"T https://a", "T https://c", "T https://b"}, titles(oc))
	gs := gc.lastSnapshot()
	assert.True(t, gs.Queue[1].Voted, "voter sees own vote")
	assert.Equal(t, 1, gs.Queue[1].Votes)
	assert.False(t, oc.lastSnapshot().Queue[1].Voted)

	// Toggle off restores insertion order; manual moves are blocked.
	require.NoError(t, f.room.QueueVote(ctx, f.guest, c))
	assert.Equal(t, []string{"T https://a", "T https://b", "T https://c"}, titles(oc))
	assert.Error(t, f.room.QueueMove(ctx, f.owner, c, &b))
	assert.Error(t, f.room.QueueVote(ctx, f.guest, snap.Queue[0].ID), "current item")
	assert.Error(t, f.room.QueueVote(ctx, access.Actor{}, c), "anonymous")

	// Skip: one vote is not enough for three users at 0.5; the second is.
	require.NoError(t, f.room.SkipVote(ctx, f.guest))
	s := gc.lastSnapshot()
	assert.Equal(t, 1, s.SkipVotes)
	assert.True(t, s.SkipVoted)
	assert.False(t, oc.lastSnapshot().SkipVoted)
	assert.Equal(t, "T https://a", titles(oc)[0])

	require.NoError(t, f.room.SkipVote(ctx, f.guest), "toggle off")
	assert.Equal(t, 0, gc.lastSnapshot().SkipVotes)
	require.NoError(t, f.room.SkipVote(ctx, f.guest))
	require.NoError(t, f.room.SkipVote(ctx, thirdActor))
	s = oc.lastSnapshot()
	assert.Equal(t, []string{"T https://b", "T https://c"}, titles(oc))
	assert.Equal(t, 0, s.SkipVotes, "reset on advance")

	// Leaving lowers the threshold: a lone viewer can skip alone.
	f.room.Leave(tc)
	f.room.Leave(gc)
	assert.Equal(t, 1, oc.lastSnapshot().SkipNeeded)

	// Disabling vote mode clears skip votes.
	require.NoError(t, f.room.SkipVote(ctx, f.owner))
	off := false
	require.NoError(t, f.room.SettingsSet(ctx, f.owner, protocol.SettingsSet{VoteMode: &off}))
	assert.Equal(t, 0, oc.lastSnapshot().SkipNeeded)

	// viewersCanAdd gates guests.
	no := false
	require.NoError(t, f.room.SettingsSet(ctx, f.owner, protocol.SettingsSet{ViewersCanAdd: &no}))
	assert.Error(t, f.room.QueueAdd(ctx, f.guest, "https://z", false, false))
	saved, err := f.repo.GetRoomByID(ctx, f.room.ID())
	require.NoError(t, err)
	assert.Equal(t, entity.Settings{VoteMode: false, SkipThreshold: 0.5, ViewersCanAdd: false, PauseWhenEmpty: true, WaitForBuffering: true}, saved.Settings)
}

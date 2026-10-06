package room

import (
	"testing"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/protocol"
)

// Snapshot preparation is pure: these tests need no database.

func TestPresenceOf(t *testing.T) {
	zoe := &entity.User{ID: uuid.New(), Username: "zoe", AvatarColor: "teal"}
	amy := &entity.User{ID: uuid.New(), Username: "amy"}
	viewers := map[Conn]*viewer{
		&fakeConn{}: {user: zoe, role: entity.RoomRoleModerator, lagMs: 1_500},
		&fakeConn{}: {user: zoe, role: entity.RoomRoleModerator, buffering: true, lagMs: -3_000},
		&fakeConn{}: {user: amy},
		&fakeConn{}: {},
		&fakeConn{}: {},
	}

	members, guests := presenceOf(viewers)
	assert.Equal(t, 2, guests)
	require.Len(t, members, 2, "one entry per user, whatever the tabs")
	assert.Equal(t, protocol.Presence{Username: "amy"}, members[0], "sorted by name")
	assert.Equal(t, protocol.Presence{Username: "zoe", Color: "teal", Role: entity.RoomRoleModerator, Buffering: true, LagMs: -3_000}, members[1],
		"buffering if any tab is, the worst lag of them")

	members, guests = presenceOf(nil)
	assert.NotNil(t, members, "an empty list on the wire, not null")
	assert.Zero(t, guests)
}

func TestPersonalize(t *testing.T) {
	me := &entity.User{ID: uuid.New(), Username: "me"}
	a, b := uuid.New(), uuid.New()
	base := protocol.Snapshot{Queue: []protocol.QueueEntry{{ID: a}, {ID: b}}}
	skips := map[uuid.UUID]struct{}{me.ID: {}}

	snap := personalize(base, &viewer{user: me}, skips, map[uuid.UUID]bool{b: true})
	assert.True(t, snap.SkipVoted)
	assert.False(t, snap.Queue[0].Voted)
	assert.True(t, snap.Queue[1].Voted)
	assert.False(t, base.Queue[1].Voted, "the shared base is not modified")

	// Anonymous viewers get the base as is.
	snap = personalize(base, &viewer{}, skips, nil)
	assert.False(t, snap.SkipVoted)
	assert.Equal(t, base, snap)
}

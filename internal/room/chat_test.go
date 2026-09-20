package room

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/protocol"
)

func TestChat(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner, guest, anon := &fakeConn{}, &fakeConn{}, &fakeConn{}
	f.room.Join(ctx, owner, f.owner)
	f.room.Join(ctx, guest, f.guest)
	f.room.Join(ctx, anon, access.Actor{})

	// Anonymous viewers read but cannot write.
	assert.Error(t, f.room.ChatSend(ctx, access.Actor{}, "hi"))
	assert.Error(t, f.room.ChatSend(ctx, f.guest, "   "))
	assert.Error(t, f.room.ChatSend(ctx, f.guest, strings.Repeat("x", 2001)))

	require.NoError(t, f.room.ChatSend(ctx, f.guest, "hello everyone"))
	msg, ok := anon.last().(protocol.ChatMessage)
	require.True(t, ok, "anonymous viewers receive chat")
	assert.Equal(t, "guest", msg.Username)
	assert.Equal(t, "hello everyone", msg.Body)

	// Flood control: burst of 5, then rate limited.
	for range 4 {
		require.NoError(t, f.room.ChatSend(ctx, f.guest, "spam"))
	}
	err := f.room.ChatSend(ctx, f.guest, "spam")
	var re *Error
	require.ErrorAs(t, err, &re)
	assert.Equal(t, protocol.CodeRateLimit, re.Code)

	// Only moderators delete; everyone learns about it.
	assert.Error(t, f.room.ChatDelete(ctx, f.guest, msg.ID))
	require.NoError(t, f.room.ChatDelete(ctx, f.owner, msg.ID))
	del, ok := guest.last().(protocol.ChatDeleted)
	require.True(t, ok)
	assert.Equal(t, msg.ID, del.ID)
	assert.Error(t, f.room.ChatDelete(ctx, f.owner, msg.ID), "already deleted")

	// Late joiners get the backlog without deleted messages.
	late := &fakeConn{}
	f.room.Join(ctx, late, access.Actor{})
	w := late.msgs[0].(protocol.Welcome)
	var user []protocol.ChatMessage
	for _, m := range w.Messages {
		if !m.System {
			user = append(user, m)
		}
	}
	assert.Len(t, user, 4, "deleted message is gone, system lines are separate")
	assert.Equal(t, "spam", user[0].Body)
}

func TestRoomLog(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner, guest := &fakeConn{}, &fakeConn{}
	f.room.Join(ctx, owner, f.owner)

	systemLines := func(c *fakeConn) []string {
		c.mu.Lock()
		defer c.mu.Unlock()
		var out []string
		for _, m := range c.msgs {
			if cm, ok := m.(protocol.ChatMessage); ok && cm.System {
				out = append(out, cm.Body)
			}
		}
		return out
	}

	// A second connection of the same user is not a new join.
	f.room.Join(ctx, guest, f.guest)
	again := &fakeConn{}
	f.room.Join(ctx, again, f.guest)
	assert.Equal(t, []string{"owner joined", "guest joined"}, systemLines(owner))

	// Leaving and coming straight back is one visit: no "left", no new
	// "joined". Staying away past the grace period logs both.
	f.room.deps.RejoinGrace = 150 * time.Millisecond
	f.room.Leave(guest)
	f.room.Leave(again)
	f.now = f.now.Add(50 * time.Millisecond)
	f.room.Join(ctx, guest, f.guest)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, []string{"owner joined", "guest joined"}, systemLines(owner))
	f.room.Leave(guest)
	require.Eventually(t, func() bool {
		lines := systemLines(owner)
		return len(lines) == 3 && lines[2] == "guest left"
	}, time.Second, 20*time.Millisecond)
	f.now = f.now.Add(time.Second)
	f.room.Join(ctx, guest, f.guest)
	assert.Equal(t, []string{"owner joined", "guest joined", "guest left", "guest joined"}, systemLines(owner))

	f.ready("https://a", 10_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.guest, "https://a"))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://b"))
	require.NoError(t, f.room.Next(ctx, f.owner))
	lines := systemLines(owner)
	assert.Equal(t, []string{"guest added “T https://a”", "owner added a video from b", "owner skipped “T https://a”"}, lines[4:])

	// The backlog carries system lines without an author.
	late := &fakeConn{}
	f.room.Join(ctx, late, access.Actor{})
	w := late.msgs[0].(protocol.Welcome)
	require.NotEmpty(t, w.Messages)
	assert.True(t, w.Messages[0].System)
	assert.Empty(t, w.Messages[0].Username)

	// Skip by vote is logged too.
	on := true
	require.NoError(t, f.room.SettingsSet(ctx, f.owner, protocol.SettingsSet{VoteMode: &on}))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://c"))
	require.NoError(t, f.room.SkipVote(ctx, f.owner))
	require.NoError(t, f.room.SkipVote(ctx, f.guest))
	lines = systemLines(owner)
	assert.Contains(t, lines[len(lines)-1], "by vote")
}

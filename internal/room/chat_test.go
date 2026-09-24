package room

import (
	"context"
	"strings"
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/notify"
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
	assert.Error(t, f.room.ChatSend(ctx, access.Actor{}, "hi", nil))
	assert.Error(t, f.room.ChatSend(ctx, f.guest, "   ", nil))
	assert.Error(t, f.room.ChatSend(ctx, f.guest, strings.Repeat("x", 2001), nil))

	require.NoError(t, f.room.ChatSend(ctx, f.guest, "hello everyone", nil))
	msg, ok := anon.last().(protocol.ChatMessage)
	require.True(t, ok, "anonymous viewers receive chat")
	assert.Equal(t, "guest", msg.Username)
	assert.Equal(t, "hello everyone", msg.Body)

	// Flood control: burst of 5, then rate limited.
	for range 4 {
		require.NoError(t, f.room.ChatSend(ctx, f.guest, "spam", nil))
	}
	err := f.room.ChatSend(ctx, f.guest, "spam", nil)
	var re *Error
	require.ErrorAs(t, err, &re)
	assert.Equal(t, protocol.CodeRateLimit, re.Code)

	// Authors and moderators delete; everyone learns about it.
	assert.Error(t, f.room.ChatDelete(ctx, access.Actor{}, msg.ID))
	require.NoError(t, f.room.ChatDelete(ctx, f.guest, msg.ID))
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
	require.NoError(t, f.room.QueueAdd(ctx, f.guest, "https://a", false, false))
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://b", false, false))
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
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://c", false, false))
	require.NoError(t, f.room.SkipVote(ctx, f.owner))
	require.NoError(t, f.room.SkipVote(ctx, f.guest))
	lines = systemLines(owner)
	assert.Contains(t, lines[len(lines)-1], "by vote")
}

func TestTypingAndReactions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	c1, c2 := &fakeConn{}, &fakeConn{}
	f.room.Join(ctx, c1, f.owner)
	f.room.Join(ctx, c2, f.guest)
	n1, n2 := len(c1.msgs), len(c2.msgs)

	// Typing reaches everyone but the sender and is not stored.
	require.NoError(t, f.room.ChatTyping(f.owner))
	assert.Len(t, c1.msgs, n1)
	require.Len(t, c2.msgs, n2+1)
	assert.Equal(t, protocol.Typing{Type: protocol.TypeTyping, Username: "owner"}, c2.msgs[n2])
	assert.Error(t, f.room.ChatTyping(access.Actor{}), "anonymous cannot type")

	// Reactions go to all, from a fixed set, rate limited.
	assert.Error(t, f.room.React(f.guest, "🦄"))
	require.NoError(t, f.room.React(f.guest, "🔥"))
	assert.Equal(t, protocol.Reaction{Type: protocol.TypeReaction, Username: "guest", Emoji: "🔥"}, c1.msgs[len(c1.msgs)-1])
	assert.Equal(t, protocol.Reaction{Type: protocol.TypeReaction, Username: "guest", Emoji: "🔥"}, c2.msgs[len(c2.msgs)-1])
	var limited bool
	for range 5 {
		if err := f.room.React(f.guest, "🔥"); err != nil {
			limited = true
		}
	}
	assert.True(t, limited)
	msgs, err := f.repo.ListRecentMessages(ctx, f.room.ID(), 50)
	require.NoError(t, err)
	for _, m := range msgs {
		assert.NotContains(t, m.Body, "🔥")
	}
}

func TestMuteSlowModeAndClear(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	c1, c2 := &fakeConn{}, &fakeConn{}
	f.room.Join(ctx, c1, f.owner)
	f.room.Join(ctx, c2, f.guest)

	// A muted actor cannot speak, vote or add; the message says until when.
	until := f.now.Add(30 * time.Minute)
	muted := f.guest
	muted.MutedUntil = &until
	err := f.room.ChatSend(ctx, muted, "hi", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "muted until")
	assert.Error(t, f.room.QueueAdd(ctx, muted, "https://x", false, false))
	assert.Error(t, f.room.React(muted, "🔥"))

	// Slow mode throttles members but not moderators.
	slow := 30
	require.NoError(t, f.room.SettingsSet(ctx, f.owner, protocol.SettingsSet{SlowModeSec: &slow}))
	bad := 7
	assert.Error(t, f.room.SettingsSet(ctx, f.owner, protocol.SettingsSet{SlowModeSec: &bad}))
	require.NoError(t, f.room.ChatSend(ctx, f.guest, "one", nil))
	err = f.room.ChatSend(ctx, f.guest, "two", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "slow mode")
	require.NoError(t, f.room.ChatSend(ctx, f.owner, "mods are exempt", nil))
	require.NoError(t, f.room.ChatSend(ctx, f.owner, "still", nil))
	f.now = f.now.Add(31 * time.Second)
	require.NoError(t, f.room.ChatSend(ctx, f.guest, "two", nil))

	// Clear: everyone gets chat.cleared, the backlog is empty, a line logs it.
	assert.Error(t, f.room.ChatClear(ctx, f.guest))
	require.NoError(t, f.room.ChatClear(ctx, f.owner))
	var cleared bool
	for _, m := range c2.msgs {
		if _, ok := m.(protocol.ChatCleared); ok {
			cleared = true
		}
	}
	assert.True(t, cleared)
	msgs, err := f.repo.ListRecentMessages(ctx, f.room.ID(), 50)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "owner cleared the chat", msgs[0].Body)
}

func TestRepliesOwnDeleteAndPin(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	c1, c2 := &fakeConn{}, &fakeConn{}
	f.room.Join(ctx, c1, f.owner)
	f.room.Join(ctx, c2, f.guest)

	require.NoError(t, f.room.ChatSend(ctx, f.guest, "which one?", nil))
	first := c1.msgs[len(c1.msgs)-1].(protocol.ChatMessage)

	// A reply quotes the original; replying to a missing message fails.
	require.NoError(t, f.room.ChatSend(ctx, f.owner, "the second", &first.ID))
	reply := c2.msgs[len(c2.msgs)-1].(protocol.ChatMessage)
	require.NotNil(t, reply.ReplyTo)
	assert.Equal(t, first.ID, reply.ReplyTo.ID)
	assert.Equal(t, "guest", reply.ReplyTo.Username)
	assert.Equal(t, "which one?", reply.ReplyTo.Body)
	missing := int64(999_999)
	assert.Error(t, f.room.ChatSend(ctx, f.owner, "to nobody", &missing))

	// Pinning needs ModerateChat; the pin travels in the snapshot and is
	// announced to everyone.
	assert.Error(t, f.room.ChatPin(ctx, f.guest, first.ID))
	require.NoError(t, f.room.ChatPin(ctx, f.owner, first.ID))
	var pinned *protocol.ChatPinned
	for _, m := range c2.msgs {
		if p, ok := m.(protocol.ChatPinned); ok {
			pinned = &p
		}
	}
	require.NotNil(t, pinned)
	require.NotNil(t, pinned.Message)
	assert.Equal(t, first.ID, pinned.Message.ID)
	c3 := &fakeConn{}
	f.room.Join(ctx, c3, f.guest)
	require.NotNil(t, c3.lastSnapshot().Room.Pinned)
	assert.Equal(t, first.ID, c3.lastSnapshot().Room.Pinned.ID)

	// Members delete their own lines only; moderators anything. Deleting
	// the pinned message unpins it.
	assert.Error(t, f.room.ChatDelete(ctx, f.guest, reply.ID), "not the guest's line")
	require.NoError(t, f.room.ChatDelete(ctx, f.guest, first.ID))
	last := c1.msgs[len(c1.msgs)-1]
	assert.Equal(t, protocol.ChatPinned{Type: protocol.TypeChatPinned, Message: nil}, last)
	require.NoError(t, f.room.ChatDelete(ctx, f.owner, reply.ID))

	// The persisted pin survives a reload; a pin on a deleted line is
	// dropped on load.
	require.NoError(t, f.room.ChatSend(ctx, f.owner, "rules: be nice", nil))
	rules := c1.msgs[len(c1.msgs)-1].(protocol.ChatMessage)
	require.NoError(t, f.room.ChatPin(ctx, f.owner, rules.ID))
	reloaded, err := load(ctx, f.room.deps, f.room.ID())
	require.NoError(t, err)
	require.NotNil(t, reloaded.pinned)
	assert.Equal(t, rules.ID, reloaded.pinned.ID)
	require.NoError(t, f.room.ChatUnpin(ctx, f.owner))
	reloaded, err = load(ctx, f.room.deps, f.room.ID())
	require.NoError(t, err)
	assert.Nil(t, reloaded.pinned)
}

type pushed struct {
	user uuid.UUID
	msg  notify.Message
}

type fakeNotifier struct{ out []pushed }

func (f *fakeNotifier) Notify(user uuid.UUID, msg notify.Message) {
	f.out = append(f.out, pushed{user, msg})
}

func TestPushMentionsAndStart(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	n := &fakeNotifier{}
	f.room.deps.Notifier = n
	owner := &fakeConn{}
	f.room.Join(ctx, owner, f.owner)
	away, err := f.repo.CreateUser(ctx, "away", "h")
	require.NoError(t, err)

	// The guest is connected: no push; "away" is not: one push, and the
	// sender's own name is ignored.
	guestConn := &fakeConn{}
	f.room.Join(ctx, guestConn, f.guest)
	require.NoError(t, f.room.ChatSend(ctx, f.owner, "@guest @away @owner look at 1:23", nil))
	require.Len(t, n.out, 1)
	assert.Equal(t, away.ID, n.out[0].user)
	assert.Equal(t, "owner mentioned you in "+f.room.info.Name, n.out[0].msg.Title)
	assert.Equal(t, "/r/"+f.room.info.Slug, n.out[0].msg.URL)

	// Throttled per user.
	require.NoError(t, f.room.ChatSend(ctx, f.owner, "@away again", nil))
	assert.Len(t, n.out, 1)
	f.now = f.now.Add(2 * time.Minute)
	require.NoError(t, f.room.ChatSend(ctx, f.owner, "@away again", nil))
	assert.Len(t, n.out, 2)

	// The guest queues a video and leaves; when it comes up they hear
	// about it, tagged like the page's own notification.
	f.ready("https://a", 10_000)
	f.ready("https://b", 10_000)
	require.NoError(t, f.room.QueueAdd(ctx, f.owner, "https://a", false, false))
	require.NoError(t, f.room.QueueAdd(ctx, f.guest, "https://b", false, false))
	f.room.Leave(guestConn)
	n.out = nil
	require.NoError(t, f.room.Next(ctx, f.owner))
	require.Len(t, n.out, 1)
	assert.Equal(t, f.guest.User.ID, n.out[0].user)
	assert.Equal(t, "Your video is starting", n.out[0].msg.Title)
	cur := owner.lastSnapshot().Queue[0]
	assert.Equal(t, "start-"+cur.ID.String(), n.out[0].msg.Tag)
}

package room

import (
	"context"
	"strings"
	"testing"

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
	assert.Len(t, w.Messages, 4)
	assert.Equal(t, "spam", w.Messages[0].Body)
}

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
)

func TestMessages(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	owner, _ := repo.CreateUser(ctx, "owner", "h")
	room, err := repo.CreateRoom(ctx, "chat-room", "Chat", owner.ID, entity.VisibilityPublic, entity.DefaultSettings())
	require.NoError(t, err)

	m1, err := repo.CreateMessage(ctx, room.ID, owner.ID, "hello")
	require.NoError(t, err)
	assert.Equal(t, "owner", m1.Username)
	m2, err := repo.CreateMessage(ctx, room.ID, owner.ID, "world")
	require.NoError(t, err)

	recent, err := repo.ListRecentMessages(ctx, room.ID, 1)
	require.NoError(t, err)
	require.Len(t, recent, 1)
	assert.Equal(t, m2.ID, recent[0].ID)

	require.NoError(t, repo.DeleteMessage(ctx, room.ID, m1.ID, owner.ID))
	assert.ErrorIs(t, repo.DeleteMessage(ctx, room.ID, m1.ID, owner.ID), ErrNotFound)
	recent, _ = repo.ListRecentMessages(ctx, room.ID, 10)
	assert.Len(t, recent, 1)

	n, err := repo.PurgeMessagesBefore(ctx, time.Now().Add(time.Minute))
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)
}

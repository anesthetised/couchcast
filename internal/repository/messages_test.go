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

	m1, err := repo.CreateMessage(ctx, room.ID, owner.ID, "hello", nil)
	require.NoError(t, err)
	assert.Equal(t, "owner", m1.Username)
	m2, err := repo.CreateMessage(ctx, room.ID, owner.ID, "world", nil)
	require.NoError(t, err)

	recent, err := repo.ListRecentMessages(ctx, room.ID, 1)
	require.NoError(t, err)
	require.Len(t, recent, 1)
	assert.Equal(t, m2.ID, recent[0].ID)

	require.NoError(t, repo.DeleteMessage(ctx, room.ID, m1.ID, owner.ID, false))
	assert.ErrorIs(t, repo.DeleteMessage(ctx, room.ID, m1.ID, owner.ID, false), ErrNotFound)
	recent, _ = repo.ListRecentMessages(ctx, room.ID, 10)
	assert.Len(t, recent, 1)

	n, err := repo.PurgeMessagesBefore(ctx, time.Now().Add(time.Minute))
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)
}

func TestEditMessage(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	owner, _ := repo.CreateUser(ctx, "owner", "h")
	other, _ := repo.CreateUser(ctx, "other", "h")
	room, err := repo.CreateRoom(ctx, "edit-room", "Edit", owner.ID, entity.VisibilityPublic, entity.DefaultSettings())
	require.NoError(t, err)
	m, err := repo.CreateMessage(ctx, room.ID, owner.ID, "helo", nil)
	require.NoError(t, err)
	assert.Nil(t, m.EditedAt)

	edited, err := repo.EditMessage(ctx, room.ID, m.ID, owner.ID, "hello", 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "hello", edited.Body)
	require.NotNil(t, edited.EditedAt)

	// Only the author, only visible user lines, only within the window.
	_, err = repo.EditMessage(ctx, room.ID, m.ID, other.ID, "mine now", 5*time.Minute)
	require.ErrorIs(t, err, ErrNotFound)
	sys, err := repo.CreateSystemMessage(ctx, room.ID, "owner joined")
	require.NoError(t, err)
	_, err = repo.EditMessage(ctx, room.ID, sys.ID, owner.ID, "x", 5*time.Minute)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = repo.Pool().Exec(ctx, `UPDATE messages SET created_at = now() - interval '6 minutes' WHERE id = $1`, m.ID)
	require.NoError(t, err)
	_, err = repo.EditMessage(ctx, room.ID, m.ID, owner.ID, "too late", 5*time.Minute)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, repo.DeleteMessage(ctx, room.ID, m.ID, owner.ID, true))
	_, err = repo.EditMessage(ctx, room.ID, m.ID, owner.ID, "gone", time.Hour)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestTouchMediaAccessOnlyMovesForward(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	m, _, err := repo.CreateMedia(ctx, repo.Pool(), "url:https://a", "https://a")
	require.NoError(t, err)
	later := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	require.NoError(t, repo.TouchMediaAccess(ctx, m.ID, later))
	require.NoError(t, repo.TouchMediaAccess(ctx, m.ID, later.Add(-30*time.Minute)))
	got, err := repo.GetMedia(ctx, m.ID)
	require.NoError(t, err)
	assert.True(t, got.LastAccessedAt.Equal(later))
}

func TestPurgeBugReports(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	u, err := repo.CreateUser(ctx, "reporter", "h")
	require.NoError(t, err)
	b, err := repo.CreateBugReport(ctx, &entity.BugReport{UserID: &u.ID, Category: entity.BugCategory("other"), Description: "x", Client: []byte(`{}`), Server: []byte(`{}`)}, nil)
	require.NoError(t, err)
	n, err := repo.PurgeBugReports(ctx, b.CreatedAt)
	require.NoError(t, err)
	assert.Zero(t, n, "only older reports go")
	n, err = repo.PurgeBugReports(ctx, time.Now().Add(time.Minute))
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
}

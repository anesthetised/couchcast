package repository

import (
	"context"
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
)

func TestQueueAndPlayback(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	owner, _ := repo.CreateUser(ctx, "owner", "h")
	room, err := repo.CreateRoom(ctx, "queue-room", "Q", owner.ID, entity.VisibilityPublic, entity.DefaultSettings())
	require.NoError(t, err)

	m1, created, err := repo.CreateMedia(ctx, repo.Pool(), "youtube:aaa", "https://youtu.be/aaa")
	require.NoError(t, err)
	assert.True(t, created)
	m1b, created, err := repo.CreateMedia(ctx, repo.Pool(), "youtube:aaa", "https://youtu.be/aaa?x")
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, m1.ID, m1b.ID)
	m2, _, err := repo.CreateMedia(ctx, repo.Pool(), "youtube:bbb", "https://youtu.be/bbb")
	require.NoError(t, err)

	i1, err := repo.AddQueueItem(ctx, repo.Pool(), room.ID, m1.ID, &owner.ID)
	require.NoError(t, err)
	i2, err := repo.AddQueueItem(ctx, repo.Pool(), room.ID, m2.ID, nil)
	require.NoError(t, err)
	assert.Less(t, i1.Rank, i2.Rank)

	items, err := repo.ListQueue(ctx, room.ID)
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, i1.ID, items[0].ID)
	assert.Equal(t, "owner", items[0].AddedByName)
	assert.Equal(t, 0, items[0].Votes)

	// Votes toggle.
	on, err := repo.ToggleQueueVote(ctx, i2.ID, owner.ID)
	require.NoError(t, err)
	assert.True(t, on)
	voted, err := repo.ListQueueVotesByUser(ctx, room.ID, owner.ID)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{i2.ID}, voted)
	items, _ = repo.ListQueue(ctx, room.ID)
	assert.Equal(t, 1, items[1].Votes)
	on, err = repo.ToggleQueueVote(ctx, i2.ID, owner.ID)
	require.NoError(t, err)
	assert.False(t, on)

	// Reorder.
	require.NoError(t, repo.SetQueueRanks(ctx, room.ID, []uuid.UUID{i2.ID, i1.ID}))
	items, _ = repo.ListQueue(ctx, room.ID)
	assert.Equal(t, i2.ID, items[0].ID)

	// Playback persistence.
	at := time.Now().Truncate(time.Microsecond)
	require.NoError(t, repo.UpdateRoomPlayback(ctx, room.ID, entity.PlaybackState{CurrentItemID: &i2.ID, Playing: true, PositionMs: 4200, PositionAt: at}))
	got, err := repo.GetRoomByID(ctx, room.ID)
	require.NoError(t, err)
	assert.Equal(t, i2.ID, *got.CurrentItemID)
	assert.True(t, got.Playing)
	assert.EqualValues(t, 4200, got.PositionMs)
	assert.True(t, got.PositionAt.Equal(at))

	rooms, err := repo.ListRoomIDsWithMedia(ctx, m2.ID)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{room.ID}, rooms)

	// Deleting the current item nulls the pointer.
	require.NoError(t, repo.DeleteQueueItem(ctx, room.ID, i2.ID))
	got, _ = repo.GetRoomByID(ctx, room.ID)
	assert.Nil(t, got.CurrentItemID)
	assert.ErrorIs(t, repo.DeleteQueueItem(ctx, room.ID, i2.ID), ErrNotFound)

	// Media status transitions.
	require.NoError(t, repo.SetMediaStatus(ctx, m1.ID, entity.MediaDownloading))
	require.NoError(t, repo.SetMediaProgress(ctx, m1.ID, 0.5))
	require.NoError(t, repo.SetMediaProbed(ctx, m1.ID, "Title", 60000, "http://thumb"))
	require.NoError(t, repo.SetMediaReady(ctx, m1.ID, []entity.Rendition{{ID: "0", Height: 720}}, 123, "media/x/"))
	mm, err := repo.GetMedia(ctx, m1.ID)
	require.NoError(t, err)
	assert.True(t, mm.IsReady())
	assert.Equal(t, "Title", mm.Title)
	assert.Len(t, mm.Renditions, 1)
	require.NoError(t, repo.SetMediaFailed(ctx, m1.ID, "boom"))
	mm, _ = repo.GetMedia(ctx, m1.ID)
	assert.Equal(t, entity.MediaFailed, mm.Status)
	assert.Equal(t, "boom", mm.Error)

	batch, err := repo.GetMediaBatch(ctx, []uuid.UUID{m1.ID, m2.ID, uuid.New()})
	require.NoError(t, err)
	assert.Len(t, batch, 2)

	blocked, err := repo.IsSourceBlocked(ctx, "youtube:aaa")
	require.NoError(t, err)
	assert.False(t, blocked)
	require.NoError(t, repo.BlockSource(ctx, "youtube:aaa", "dmca", &owner.ID))
	blocked, _ = repo.IsSourceBlocked(ctx, "youtube:aaa")
	assert.True(t, blocked)
}

func TestPlayedHistory(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	owner, _ := repo.CreateUser(ctx, "owner", "h")
	room, err := repo.CreateRoom(ctx, "history-room", "H", owner.ID, entity.VisibilityPublic, entity.DefaultSettings())
	require.NoError(t, err)
	items := make([]*entity.QueueItem, 0, 3)
	for _, key := range []string{"a", "b", "c"} {
		m, _, err := repo.CreateMedia(ctx, repo.Pool(), "url:"+key, "https://"+key)
		require.NoError(t, err)
		it, err := repo.AddQueueItem(ctx, repo.Pool(), room.ID, m.ID, &owner.ID)
		require.NoError(t, err)
		items = append(items, it)
	}

	t0 := time.Now().Add(-time.Hour)
	require.NoError(t, repo.MarkQueueItemPlayed(ctx, room.ID, items[0].ID, t0))
	require.NoError(t, repo.MarkQueueItemPlayed(ctx, room.ID, items[1].ID, t0.Add(time.Minute)))
	assert.ErrorIs(t, repo.MarkQueueItemPlayed(ctx, room.ID, items[1].ID, t0), ErrNotFound, "already played")

	queue, err := repo.ListQueue(ctx, room.ID)
	require.NoError(t, err)
	require.Len(t, queue, 1)
	assert.Equal(t, items[2].ID, queue[0].ID)
	played, err := repo.ListPlayed(ctx, room.ID, 10)
	require.NoError(t, err)
	require.Len(t, played, 2)
	assert.Equal(t, items[1].ID, played[0].ID, "newest first")
	assert.NotNil(t, played[0].PlayedAt)

	// A new item ranks after the unplayed ones only.
	m, _, _ := repo.CreateMedia(ctx, repo.Pool(), "url:d", "https://d")
	d, err := repo.AddQueueItem(ctx, repo.Pool(), room.ID, m.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, "00000004", d.Rank)

	// Requeue appends the history in play order after what is queued.
	require.NoError(t, repo.RequeuePlayed(ctx, room.ID))
	queue, _ = repo.ListQueue(ctx, room.ID)
	ids := make([]uuid.UUID, 0, len(queue))
	for _, q := range queue {
		ids = append(ids, q.ID)
	}
	assert.Equal(t, []uuid.UUID{items[2].ID, d.ID, items[0].ID, items[1].ID}, ids)
	played, _ = repo.ListPlayed(ctx, room.ID, 10)
	assert.Empty(t, played)

	// Purge keeps the newest N per room and drops old ones.
	for i, it := range queue {
		require.NoError(t, repo.MarkQueueItemPlayed(ctx, room.ID, it.ID, t0.Add(time.Duration(i)*time.Minute)))
	}
	n, err := repo.PurgePlayed(ctx, t0.Add(30*time.Second), 2)
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)
	played, _ = repo.ListPlayed(ctx, room.ID, 10)
	assert.Len(t, played, 2)
	require.NoError(t, repo.ClearPlayed(ctx, room.ID))
	played, _ = repo.ListPlayed(ctx, room.ID, 10)
	assert.Empty(t, played)
}

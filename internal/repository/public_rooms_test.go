package repository

import (
	"context"
	"testing"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
)

func TestListPublicRooms(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	owner, _ := repo.CreateUser(ctx, "owner", "h")

	mk := func(slug, name string, vis entity.Visibility) *entity.Room {
		rm, err := repo.CreateRoom(ctx, slug, name, owner.ID, vis, entity.DefaultSettings())
		require.NoError(t, err)
		return rm
	}
	idle := mk("idle-room", "Idle", entity.VisibilityPublic)
	playing := mk("movie-night", "Movie night", entity.VisibilityPublic)
	weird := mk("percent", "100% fun_time", entity.VisibilityPublic)
	mk("secret", "Secret", entity.VisibilityPrivate)

	media, _, _ := repo.CreateMedia(ctx, repo.Pool(), "youtube:abc", "https://youtu.be/abc")
	require.NoError(t, repo.SetMediaProbed(ctx, media.ID, "Big Movie", 1000, "http://thumb"))
	item, err := repo.AddQueueItem(ctx, repo.Pool(), playing.ID, media.ID, &owner.ID)
	require.NoError(t, err)
	require.NoError(t, repo.UpdateRoomPlayback(ctx, playing.ID, entity.PlaybackState{CurrentItemID: &item.ID, Playing: true, PositionMs: 5, PositionAt: playing.CreatedAt}))

	slugs := func(list []PublicRoom) []string {
		out := make([]string, 0, len(list))
		for _, pr := range list {
			out = append(out, pr.Room.Slug)
		}
		return out
	}

	// Default order: live first, then playing, then recency; private hidden.
	list, total, err := repo.ListPublicRooms(ctx, PublicRoomsQuery{Limit: 10, LiveIDs: []uuid.UUID{idle.ID}, LiveCounts: []int{3}})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Equal(t, []string{"idle-room", "movie-night", "percent"}, slugs(list))
	assert.Equal(t, 3, list[0].Viewers)
	assert.Nil(t, list[0].Media)
	require.NotNil(t, list[1].Media)
	assert.Equal(t, "Big Movie", list[1].Media.Title)
	assert.Equal(t, "owner", list[1].Owner)
	assert.Equal(t, 1, list[1].MemberCount)

	// Live only.
	list, total, err = repo.ListPublicRooms(ctx, PublicRoomsQuery{Limit: 10, OnlyLive: true, LiveIDs: []uuid.UUID{idle.ID}, LiveCounts: []int{3}})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, []string{"idle-room"}, slugs(list))
	list, total, err = repo.ListPublicRooms(ctx, PublicRoomsQuery{Limit: 10, OnlyLive: true})
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, list)

	// Search by name or slug, case-insensitive; % and _ are literal.
	list, _, err = repo.ListPublicRooms(ctx, PublicRoomsQuery{Limit: 10, Search: "MOVIE"})
	require.NoError(t, err)
	assert.Equal(t, []string{"movie-night"}, slugs(list))
	list, _, err = repo.ListPublicRooms(ctx, PublicRoomsQuery{Limit: 10, Search: "100%"})
	require.NoError(t, err)
	assert.Equal(t, []string{"percent"}, slugs(list))
	list, _, err = repo.ListPublicRooms(ctx, PublicRoomsQuery{Limit: 10, Search: "n_t"})
	require.NoError(t, err)
	assert.Equal(t, []string{"percent"}, slugs(list), "underscore is literal: fun_time")
	_ = weird

	// Pagination keeps total.
	list, total, err = repo.ListPublicRooms(ctx, PublicRoomsQuery{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, list, 1)
	list, total, err = repo.ListPublicRooms(ctx, PublicRoomsQuery{Limit: 2, Offset: 10})
	require.NoError(t, err)
	assert.Zero(t, total, "no rows means no window count")
	assert.Empty(t, list)
}

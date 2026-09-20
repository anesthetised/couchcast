package repository

import (
	"context"
	"testing"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
)

func TestListDirectory(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	owner, _ := repo.CreateUser(ctx, "owner", "h")
	alice, _ := repo.CreateUser(ctx, "alice", "h")
	bob, _ := repo.CreateUser(ctx, "bob", "h")

	mk := func(slug, name string, vis entity.Visibility) *entity.Room {
		rm, err := repo.CreateRoom(ctx, slug, name, owner.ID, vis, entity.DefaultSettings())
		require.NoError(t, err)
		return rm
	}
	idle := mk("idle-room", "Idle", entity.VisibilityPublic)
	playing := mk("movie-night", "Movie night", entity.VisibilityPublic)
	mk("percent", "100% fun_time", entity.VisibilityPublic)
	secret := mk("secret", "Secret", entity.VisibilityPrivate)
	other := mk("other-secret", "Other secret", entity.VisibilityPrivate)
	banned := mk("banned-here", "Banned here", entity.VisibilityPublic)

	require.NoError(t, repo.UpsertMember(ctx, secret.ID, alice.ID, entity.RoomRoleModerator))
	require.NoError(t, repo.UpsertMember(ctx, idle.ID, alice.ID, entity.RoomRoleMember))
	require.NoError(t, repo.CreateBan(ctx, banned.ID, alice.ID, owner.ID, ""))
	_ = other

	media, _, _ := repo.CreateMedia(ctx, repo.Pool(), "youtube:abc", "https://youtu.be/abc")
	require.NoError(t, repo.SetMediaProbed(ctx, media.ID, "Big Movie", 1000, "http://thumb"))
	require.NoError(t, repo.SetMediaReady(ctx, media.ID, nil, 1, "p"))
	item, err := repo.AddQueueItem(ctx, repo.Pool(), playing.ID, media.ID, &owner.ID)
	require.NoError(t, err)
	require.NoError(t, repo.UpdateRoomPlayback(ctx, playing.ID, entity.PlaybackState{CurrentItemID: &item.ID, Playing: true, PositionMs: 5, PositionAt: playing.CreatedAt}))

	slugs := func(list []DirectoryRoom) []string {
		out := make([]string, 0, len(list))
		for _, dr := range list {
			out = append(out, dr.Room.Slug)
		}
		return out
	}

	// Anonymous: public rooms only, live first, then viewers, then recency.
	list, total, err := repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, LiveIDs: []uuid.UUID{idle.ID}, LiveCounts: []int{3}})
	require.NoError(t, err)
	assert.Equal(t, 4, total)
	assert.Equal(t, []string{"movie-night", "idle-room", "banned-here", "percent"}, slugs(list))
	assert.True(t, list[0].Live)
	assert.Equal(t, 0, list[0].Viewers)

	// Other sorts: by viewers, newest first, alphabetical.
	sorted, _, err := repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, LiveIDs: []uuid.UUID{idle.ID}, LiveCounts: []int{3}, Sort: SortViewers})
	require.NoError(t, err)
	assert.Equal(t, "idle-room", slugs(sorted)[0])
	sorted, _, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, Sort: SortNewest})
	require.NoError(t, err)
	assert.Equal(t, []string{"banned-here", "percent", "movie-night", "idle-room"}, slugs(sorted))
	sorted, _, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, Sort: SortName})
	require.NoError(t, err)
	assert.Equal(t, []string{"percent", "banned-here", "idle-room", "movie-night"}, slugs(sorted))
	assert.Equal(t, SortActive, ParseDirectorySort("bogus"))
	require.NotNil(t, list[0].Media)
	assert.Equal(t, "Big Movie", list[0].Media.Title)
	assert.Equal(t, "owner", list[0].Owner)
	assert.Equal(t, entity.RoomRole(""), list[0].MyRole)
	assert.False(t, list[1].Live)
	assert.Equal(t, 3, list[1].Viewers)
	assert.Nil(t, list[1].Media)

	// Anonymous cannot use private/mine; they are ignored.
	list, total, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, OnlyPrivate: true, OnlyMine: true})
	require.NoError(t, err)
	assert.Equal(t, 4, total)
	assert.Len(t, list, 4)

	// Alice: her private room appears, the one she is banned from does not,
	// and her role is reported.
	list, total, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, ViewerID: &alice.ID})
	require.NoError(t, err)
	assert.Equal(t, 4, total)
	assert.ElementsMatch(t, []string{"movie-night", "idle-room", "percent", "secret"}, slugs(list))
	for _, dr := range list {
		switch dr.Room.Slug {
		case "secret":
			assert.Equal(t, entity.RoomRoleModerator, dr.MyRole)
			assert.Equal(t, entity.VisibilityPrivate, dr.Room.Visibility)
		case "idle-room":
			assert.Equal(t, entity.RoomRoleMember, dr.MyRole)
		default:
			assert.Empty(t, dr.MyRole)
		}
	}

	// Private only, mine only, and both combined.
	list, _, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, ViewerID: &alice.ID, OnlyPrivate: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"secret"}, slugs(list))
	list, _, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, ViewerID: &alice.ID, OnlyMine: true})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"idle-room", "secret"}, slugs(list))
	list, _, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, ViewerID: &alice.ID, OnlyMine: true, OnlyPrivate: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"secret"}, slugs(list))

	// Bob is a member of nothing: public rooms only, mine is empty.
	list, _, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, ViewerID: &bob.ID, OnlyMine: true})
	require.NoError(t, err)
	assert.Empty(t, list)
	// The owner sees both private rooms as owner.
	list, _, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, ViewerID: &owner.ID, OnlyPrivate: true})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"secret", "other-secret"}, slugs(list))
	assert.Equal(t, entity.RoomRoleOwner, list[0].MyRole)

	// Live only: viewers alone do not count; paused is not live.
	list, total, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, OnlyLive: true, LiveIDs: []uuid.UUID{idle.ID}, LiveCounts: []int{3}})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, []string{"movie-night"}, slugs(list))
	require.NoError(t, repo.UpdateRoomPlayback(ctx, playing.ID, entity.PlaybackState{CurrentItemID: &item.ID, Playing: false, PositionAt: playing.CreatedAt}))
	list, total, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, OnlyLive: true})
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, list)
	require.NoError(t, repo.UpdateRoomPlayback(ctx, playing.ID, entity.PlaybackState{CurrentItemID: &item.ID, Playing: true, PositionAt: playing.CreatedAt}))
	ids, err := repo.ListPlayingRoomIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{playing.ID}, ids)

	// Search by name or slug, case-insensitive; % and _ are literal.
	list, _, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, Search: "MOVIE"})
	require.NoError(t, err)
	assert.Equal(t, []string{"movie-night"}, slugs(list))
	list, _, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, Search: "100%"})
	require.NoError(t, err)
	assert.Equal(t, []string{"percent"}, slugs(list))
	list, _, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 10, Search: "n_t"})
	require.NoError(t, err)
	assert.Equal(t, []string{"percent"}, slugs(list), "underscore is literal: fun_time")

	// Pagination keeps total.
	list, total, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Equal(t, 4, total)
	assert.Len(t, list, 2)
	list, total, err = repo.ListDirectory(ctx, DirectoryQuery{Limit: 2, Offset: 10})
	require.NoError(t, err)
	assert.Zero(t, total, "no rows means no window count")
	assert.Empty(t, list)
}

package repository

import (
	"context"
	"testing"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
)

func TestReportsBlocklistStats(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	admin, _ := repo.CreateUser(ctx, "admin", "h")
	alice, _ := repo.CreateUser(ctx, "alice", "h")
	require.NoError(t, repo.SetUserRole(ctx, admin.ID, entity.RoleAdmin))
	room, _ := repo.CreateRoom(ctx, "stats-room", "S", admin.ID, entity.VisibilityPrivate, entity.DefaultSettings())
	m, _, _ := repo.CreateMedia(ctx, repo.Pool(), "youtube:xyz", "https://youtu.be/xyz")
	m2, _, _ := repo.CreateMedia(ctx, repo.Pool(), "youtube:two", "https://youtu.be/two")
	require.NoError(t, repo.SetMediaReady(ctx, m.ID, nil, 1000, "p"))

	_, err := repo.AddQueueItem(ctx, repo.Pool(), room.ID, m.ID, &admin.ID)
	require.NoError(t, err)
	adder, queued, err := repo.QueueAdderInRoom(ctx, room.ID, m.ID)
	require.NoError(t, err)
	assert.True(t, queued)
	assert.Equal(t, admin.ID, *adder)
	_, queued, err = repo.QueueAdderInRoom(ctx, room.ID, m2.ID)
	require.NoError(t, err)
	assert.False(t, queued)

	require.NoError(t, repo.CreateReport(ctx, m.ID, alice.ID, &room.ID, adder, entity.ReportNSFW, "nope"))
	assert.ErrorIs(t, repo.CreateReport(ctx, m.ID, alice.ID, nil, nil, entity.ReportOther, ""), ErrConflict)
	require.NoError(t, repo.CreateReport(ctx, m.ID, admin.ID, nil, nil, entity.ReportCopyright, ""))
	require.NoError(t, repo.CreateReport(ctx, m2.ID, alice.ID, nil, nil, entity.ReportOther, ""))

	reported, err := repo.ListReportedMedia(ctx, 10)
	require.NoError(t, err)
	require.Len(t, reported, 2)
	assert.Equal(t, m.ID, reported[0].Media.ID, "most reported first")
	assert.Equal(t, 2, reported[0].Count)
	assert.Equal(t, "alice", reported[0].Reports[0].Reporter)
	assert.Equal(t, "stats-room", reported[0].Reports[0].RoomSlug)
	assert.Equal(t, "admin", reported[0].Reports[0].AddedBy)
	assert.Empty(t, reported[0].Reports[1].RoomSlug)
	require.Len(t, reported[0].Placements, 1)
	assert.Equal(t, "stats-room", reported[0].Placements[0].RoomSlug)
	assert.Equal(t, "admin", reported[0].Placements[0].AddedBy)
	assert.Empty(t, reported[1].Placements)

	n, err := repo.ResolveReports(ctx, m.ID, admin.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)
	reported, _ = repo.ListReportedMedia(ctx, 10)
	assert.Len(t, reported, 1)

	require.NoError(t, repo.BlockSource(ctx, "youtube:xyz", "dmca", &admin.ID))
	list, err := repo.ListBlocklist(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "admin", list[0].CreatedBy)
	require.NoError(t, repo.UnblockSource(ctx, "youtube:xyz"))
	assert.ErrorIs(t, repo.UnblockSource(ctx, "youtube:xyz"), ErrNotFound)

	stats, err := repo.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.Users)
	assert.Equal(t, 1, stats.Rooms)
	assert.Equal(t, 1, stats.PrivateRooms)
	assert.Equal(t, 1, stats.MediaByStatus["ready"])
	assert.Equal(t, 1, stats.MediaByStatus["queued"])
	assert.EqualValues(t, 1000, stats.MediaBytes)
	assert.Equal(t, 1, stats.OpenReports)

	users, err := repo.ListUsers(ctx, "al", 10)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "alice", users[0].User.Username)
	users, _ = repo.ListUsers(ctx, "", 10)
	assert.Len(t, users, 2)

	rooms, err := repo.ListRooms(ctx, "stats", 10)
	require.NoError(t, err)
	assert.Len(t, rooms, 1)

	require.NoError(t, repo.RecordAudit(ctx, entity.AuditEntry{ActorID: &admin.ID, Action: "x", TargetType: "room", TargetID: "1", RoomID: &room.ID}))
	audit, err := repo.ListAudit(ctx, &room.ID, 10)
	require.NoError(t, err)
	assert.Len(t, audit, 1)
	audit, _ = repo.ListAudit(ctx, nil, 10)
	assert.Len(t, audit, 1)
	other := uuid.New()
	audit, _ = repo.ListAudit(ctx, &other, 10)
	assert.Empty(t, audit)

	names, err := repo.UsernamesByID(ctx, []uuid.UUID{admin.ID, alice.ID})
	require.NoError(t, err)
	assert.Equal(t, "alice", names[alice.ID])
}

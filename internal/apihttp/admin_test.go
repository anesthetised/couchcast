package apihttp

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"testing/fstest"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/metrics"
	"github.com/anesthetised/couchcast/internal/ratelimit"
	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

type fakeDeleter struct{ prefixes []string }

func (f *fakeDeleter) DeletePrefix(_ context.Context, p string) error {
	f.prefixes = append(f.prefixes, p)
	return nil
}

func TestAdminAPI(t *testing.T) {
	repo := repository.New(repotest.Pool(t))
	deleter := &fakeDeleter{}
	var kicked, deletedMedia []uuid.UUID
	srv := New(Deps{
		Logger: slog.New(slog.DiscardHandler), DB: repo, Metrics: metrics.New("test"), Static: fstest.MapFS{},
		Users: repo, Rooms: repo, Admin: repo, Sessions: auth.NewSessions(repo, time.Hour, false),
		AuthLimiter: ratelimit.New(6000, 1000), LoginLimiter: ratelimit.New(6000, 1000),
		MediaObjects: deleter, RoomsLoaded: func() int { return 3 },
		OnUserBanned:   func(id uuid.UUID) { kicked = append(kicked, id) },
		OnMediaDeleted: func(id uuid.UUID) { deletedMedia = append(deletedMedia, id) },
	})
	admin := &dbEnv{&testEnv{t: t, handler: srv.Handler()}}
	user := &dbEnv{&testEnv{t: t, handler: srv.Handler()}}
	admin.register("boss")
	user.register("alice")

	ctx := context.Background()
	boss, err := repo.GetUserByUsername(ctx, "boss")
	require.NoError(t, err)
	require.NoError(t, repo.SetUserRole(ctx, boss.ID, entity.RoleAdmin))

	// Non-admins are refused.
	assert.Equal(t, http.StatusForbidden, user.do(http.MethodGet, "/api/v1/admin/stats", nil).Code)

	media, _, err := repo.CreateMedia(ctx, repo.Pool(), "youtube:bad", "https://youtu.be/bad")
	require.NoError(t, err)
	require.NoError(t, repo.SetMediaReady(ctx, media.ID, nil, 10, "media/"+media.ID.String()+"/"))
	room, err := repo.CreateRoom(ctx, "reported-room", "R", boss.ID, entity.VisibilityPublic, entity.DefaultSettings())
	require.NoError(t, err)
	_, err = repo.AddQueueItem(ctx, repo.Pool(), room.ID, media.ID, nil)
	require.NoError(t, err)

	// Reports.
	assert.Equal(t, http.StatusBadRequest, user.do(http.MethodPost, "/api/v1/media/"+media.ID.String()+"/reports", reportRequest{Reason: "meh"}).Code)
	assert.Equal(t, http.StatusNotFound, user.do(http.MethodPost, "/api/v1/media/"+uuid.New().String()+"/reports", reportRequest{Reason: "nsfw"}).Code)
	assert.Equal(t, http.StatusNoContent, user.do(http.MethodPost, "/api/v1/media/"+media.ID.String()+"/reports", reportRequest{Reason: "nsfw", Comment: "ew", RoomSlug: "reported-room"}).Code)
	assert.Equal(t, http.StatusConflict, user.do(http.MethodPost, "/api/v1/media/"+media.ID.String()+"/reports", reportRequest{Reason: "other"}).Code)

	rec := admin.do(http.MethodGet, "/api/v1/admin/reports", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	reports := decodeBody[[]reportedMediaResponse](t, rec)
	require.Len(t, reports, 1)
	assert.Equal(t, 1, reports[0].Count)
	assert.Equal(t, "alice", reports[0].Reports[0].Reporter)
	assert.Equal(t, "reported-room", reports[0].Reports[0].RoomSlug)
	assert.Equal(t, "R", reports[0].Reports[0].RoomName)
	require.Len(t, reports[0].Placements, 1)
	assert.Equal(t, "reported-room", reports[0].Placements[0].RoomSlug)

	rec = admin.do(http.MethodGet, "/api/v1/admin/stats", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	stats := decodeBody[statsResponse](t, rec)
	assert.Equal(t, 2, stats.Users)
	assert.Equal(t, 1, stats.OpenReports)
	assert.Equal(t, 3, stats.RoomsLoaded)

	// Deleting the media blocks the source, removes objects and queue rows.
	assert.Equal(t, http.StatusNoContent, admin.do(http.MethodDelete, "/api/v1/admin/media/"+media.ID.String(), banRequest{Reason: "dmca"}).Code)
	assert.Equal(t, []string{"media/" + media.ID.String() + "/"}, deleter.prefixes)
	assert.Equal(t, []uuid.UUID{media.ID}, deletedMedia)
	blocked, _ := repo.IsSourceBlocked(ctx, "youtube:bad")
	assert.True(t, blocked)
	items, _ := repo.ListQueue(ctx, room.ID)
	assert.Empty(t, items)
	rec = admin.do(http.MethodGet, "/api/v1/admin/reports", nil)
	assert.Empty(t, decodeBody[[]reportedMediaResponse](t, rec))

	rec = admin.do(http.MethodGet, "/api/v1/admin/blocklist", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, decodeBody[[]blocklistResponse](t, rec), 1)
	assert.Equal(t, http.StatusNoContent, admin.do(http.MethodDelete, "/api/v1/admin/blocklist/youtube:bad", nil).Code)
	assert.Equal(t, http.StatusNotFound, admin.do(http.MethodDelete, "/api/v1/admin/blocklist/youtube:bad", nil).Code)
	assert.Equal(t, http.StatusNoContent, admin.do(http.MethodPost, "/api/v1/admin/blocklist", blockRequest{SourceKey: "youtube:zzz", Reason: "x"}).Code)

	// Users: ban revokes sessions and kicks; admins cannot be banned.
	rec = admin.do(http.MethodGet, "/api/v1/admin/users?q=al", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	users := decodeBody[[]adminUserResponse](t, rec)
	require.Len(t, users, 1)
	alice := users[0]
	assert.Equal(t, http.StatusBadRequest, admin.do(http.MethodPost, "/api/v1/admin/users/"+boss.ID.String()+"/ban", nil).Code)
	assert.Equal(t, http.StatusNoContent, admin.do(http.MethodPost, "/api/v1/admin/users/"+alice.ID.String()+"/ban", banRequest{Reason: "spam"}).Code)
	assert.Equal(t, []uuid.UUID{alice.ID}, kicked)
	assert.Equal(t, http.StatusUnauthorized, user.do(http.MethodGet, "/api/v1/auth/me", nil).Code, "sessions revoked")
	assert.Equal(t, http.StatusForbidden, user.do(http.MethodPost, "/api/v1/auth/login", credentials{Username: "alice", Password: "password-123"}).Code)
	assert.Equal(t, http.StatusNoContent, admin.do(http.MethodPost, "/api/v1/admin/users/"+alice.ID.String()+"/unban", nil).Code)
	rec = user.do(http.MethodPost, "/api/v1/auth/login", credentials{Username: "alice", Password: "password-123"})
	assert.Equal(t, http.StatusOK, rec.Code)

	// Rooms.
	rec = admin.do(http.MethodGet, "/api/v1/admin/rooms?q=rep", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	rooms := decodeBody[[]roomResponse](t, rec)
	require.Len(t, rooms, 1)
	assert.Equal(t, "boss", rooms[0].Owner)
	assert.Equal(t, http.StatusNoContent, admin.do(http.MethodDelete, "/api/v1/admin/rooms/reported-room", nil).Code)
	assert.Equal(t, http.StatusNotFound, admin.do(http.MethodDelete, "/api/v1/admin/rooms/reported-room", nil).Code)

	// Audit trail captured everything.
	rec = admin.do(http.MethodGet, "/api/v1/admin/audit", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	audit := decodeBody[auditPage](t, rec)
	actions := map[string]bool{}
	for _, a := range audit.Entries {
		actions[a.Action] = true
		assert.Equal(t, "boss", a.Actor)
	}
	for _, want := range []string{"media.delete", "source.unblock", "source.block", "user.ban", "user.unban", "room.delete"} {
		assert.True(t, actions[want], want)
	}
	assert.Zero(t, audit.NextBefore, "one page")
	// Filters: action prefix, actor (unknown actor is an empty page).
	page := decodeBody[auditPage](t, admin.do(http.MethodGet, "/api/v1/admin/audit?action=user.", nil))
	require.Len(t, page.Entries, 2)
	assert.Empty(t, decodeBody[auditPage](t, admin.do(http.MethodGet, "/api/v1/admin/audit?actor=alice", nil)).Entries)
	assert.NotEmpty(t, decodeBody[auditPage](t, admin.do(http.MethodGet, "/api/v1/admin/audit?actor=boss", nil)).Entries)
	assert.Empty(t, decodeBody[auditPage](t, admin.do(http.MethodGet, "/api/v1/admin/audit?actor=nobody", nil)).Entries)

	// Storage: a ready item shows up with its size; eviction refuses a
	// queued item, drops an unqueued one and the stale sweep takes what is
	// old enough.
	big, _, err := repo.CreateMedia(ctx, repo.Pool(), "url:big", "https://big")
	require.NoError(t, err)
	require.NoError(t, repo.SetMediaReady(ctx, big.ID, nil, 5000, "p/big"))
	old, _, err := repo.CreateMedia(ctx, repo.Pool(), "url:old", "https://old")
	require.NoError(t, err)
	require.NoError(t, repo.SetMediaReady(ctx, old.ID, nil, 700, "p/old"))
	_, err = repo.Pool().Exec(ctx, `UPDATE media SET last_accessed_at = now() - interval '40 days' WHERE id = $1`, old.ID)
	require.NoError(t, err)
	rec = admin.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "Holder", "slug": "holder"})
	require.Equal(t, http.StatusCreated, rec.Code)
	holder, err := repo.GetRoomBySlug(ctx, "holder")
	require.NoError(t, err)
	_, err = repo.AddQueueItem(ctx, repo.Pool(), holder.ID, big.ID, nil)
	require.NoError(t, err)

	storage := decodeBody[storageResponse](t, admin.do(http.MethodGet, "/api/v1/admin/storage", nil))
	assert.EqualValues(t, 5700, storage.TotalBytes)
	require.Len(t, storage.Media, 2)
	assert.Equal(t, big.ID, storage.Media[0].ID)
	assert.True(t, storage.Media[0].Queued)
	assert.False(t, storage.Media[1].Queued)

	assert.Equal(t, http.StatusConflict, admin.do(http.MethodPost, "/api/v1/admin/media/"+big.ID.String()+"/evict", nil).Code)
	swept := decodeBody[evictResponse](t, admin.do(http.MethodPost, "/api/v1/admin/storage/evict", evictRequest{OlderThanDays: 30}))
	assert.Equal(t, 1, swept.Removed)
	assert.EqualValues(t, 700, swept.Bytes)
	assert.Contains(t, deleter.prefixes, "p/old")
	assert.Equal(t, http.StatusNotFound, admin.do(http.MethodPost, "/api/v1/admin/media/"+old.ID.String()+"/evict", nil).Code)
	require.NoError(t, repo.DeleteQueueItem(ctx, holder.ID, mustFirstItem(t, repo, holder.ID)))
	assert.Equal(t, http.StatusNoContent, admin.do(http.MethodPost, "/api/v1/admin/media/"+big.ID.String()+"/evict", nil).Code)
	assert.Contains(t, deletedMedia, big.ID)
	blocked, err = repo.IsSourceBlocked(ctx, "url:big")
	require.NoError(t, err)
	assert.False(t, blocked, "eviction never blocklists")
	assert.EqualValues(t, 0, decodeBody[storageResponse](t, admin.do(http.MethodGet, "/api/v1/admin/storage", nil)).TotalBytes)
}

func mustFirstItem(t *testing.T, repo *repository.Repo, roomID uuid.UUID) uuid.UUID {
	t.Helper()
	items, err := repo.ListQueue(context.Background(), roomID)
	require.NoError(t, err)
	require.NotEmpty(t, items)
	return items[0].ID
}

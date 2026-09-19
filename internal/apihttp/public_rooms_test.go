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
	"github.com/anesthetised/couchcast/internal/mediastore"
	"github.com/anesthetised/couchcast/internal/metrics"
	"github.com/anesthetised/couchcast/internal/protocol"
	"github.com/anesthetised/couchcast/internal/ratelimit"
	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

type fakeLive struct {
	counts   map[uuid.UUID]int
	playback map[uuid.UUID]protocol.Playback
}

func (f *fakeLive) LiveCounts() map[uuid.UUID]int { return f.counts }
func (f *fakeLive) Playback(id uuid.UUID) (protocol.Playback, bool) {
	pb, ok := f.playback[id]
	return pb, ok
}

func TestPublicRoomsDirectory(t *testing.T) {
	repo := repository.New(repotest.Pool(t))
	ctx := context.Background()
	signer := mediastore.NewSigner("0123456789abcdef0123456789abcdef", time.Hour)
	live := &fakeLive{counts: map[uuid.UUID]int{}, playback: map[uuid.UUID]protocol.Playback{}}

	srv := New(Deps{
		Logger: slog.New(slog.DiscardHandler), DB: repo, Metrics: metrics.New("test"), Static: fstest.MapFS{},
		Users: repo, Rooms: repo, Sessions: auth.NewSessions(repo, time.Hour, false),
		AuthLimiter: ratelimit.New(6000, 1000), LoginLimiter: ratelimit.New(6000, 1000),
		Directory: repo, Live: live, Signer: signer,
	})
	anon := &dbEnv{&testEnv{t: t, handler: srv.Handler()}}

	owner, _ := repo.CreateUser(ctx, "owner", "h")
	ready, _ := repo.CreateRoom(ctx, "ready-room", "Ready", owner.ID, entity.VisibilityPublic, entity.DefaultSettings())
	pending, _ := repo.CreateRoom(ctx, "pending-room", "Pending", owner.ID, entity.VisibilityPublic, entity.DefaultSettings())
	_, err := repo.CreateRoom(ctx, "secret-room", "Secret", owner.ID, entity.VisibilityPrivate, entity.DefaultSettings())
	require.NoError(t, err)
	for i := range 30 {
		_, err := repo.CreateRoom(ctx, "filler-"+itoa(i), "Filler", owner.ID, entity.VisibilityPublic, entity.DefaultSettings())
		require.NoError(t, err)
	}

	m1, _, _ := repo.CreateMedia(ctx, repo.Pool(), "youtube:one", "https://youtu.be/one")
	require.NoError(t, repo.SetMediaProbed(ctx, m1.ID, "One", 60000, "http://t1"))
	require.NoError(t, repo.SetMediaReady(ctx, m1.ID, nil, 1, "p"))
	i1, _ := repo.AddQueueItem(ctx, repo.Pool(), ready.ID, m1.ID, &owner.ID)
	require.NoError(t, repo.UpdateRoomPlayback(ctx, ready.ID, entity.PlaybackState{CurrentItemID: &i1.ID, Playing: true, PositionMs: 1000, PositionAt: time.Now()}))

	m2, _, _ := repo.CreateMedia(ctx, repo.Pool(), "youtube:two", "https://youtu.be/two")
	i2, _ := repo.AddQueueItem(ctx, repo.Pool(), pending.ID, m2.ID, &owner.ID)
	require.NoError(t, repo.UpdateRoomPlayback(ctx, pending.ID, entity.PlaybackState{CurrentItemID: &i2.ID, PositionAt: time.Now()}))

	live.counts[pending.ID] = 2
	live.playback[ready.ID] = protocol.Playback{Playing: false, PositionMs: 4242, AtServerMs: 123}

	// Anonymous, default page: live first, then playing, then fillers; 32 total.
	rec := anon.do(http.MethodGet, "/api/v1/rooms/public", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeBody[directoryResponse](t, rec)
	assert.Equal(t, 32, resp.Total)
	assert.Equal(t, 1, resp.Page)
	assert.Equal(t, 24, resp.PerPage)
	require.Len(t, resp.Rooms, 24)
	assert.Greater(t, resp.ServerNowMs, int64(0))

	first := resp.Rooms[0]
	assert.Equal(t, "pending-room", first.Slug)
	assert.Equal(t, 2, first.Viewers)
	require.NotNil(t, first.Media)
	assert.Empty(t, first.Media.Manifest, "not ready: no manifest or token")
	assert.Empty(t, first.Media.Token)

	second := resp.Rooms[1]
	assert.Equal(t, "ready-room", second.Slug)
	require.NotNil(t, second.Media)
	assert.Equal(t, "/media/"+m1.ID.String()+"/manifest.mpd", second.Media.Manifest)
	assert.True(t, signer.Verify(second.Media.Token, m1.ID, time.Now()))
	require.NotNil(t, second.Playback)
	assert.EqualValues(t, 4242, second.Playback.PositionMs, "live clock wins over the persisted one")
	assert.False(t, second.Playback.Playing)

	assert.Nil(t, resp.Rooms[2].Media)
	for _, rm := range resp.Rooms {
		assert.NotEqual(t, "secret-room", rm.Slug)
	}

	// Pagination and bounds normalisation.
	rec = anon.do(http.MethodGet, "/api/v1/rooms/public?page=2&perPage=999", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	resp = decodeBody[directoryResponse](t, rec)
	assert.Equal(t, 48, resp.PerPage)
	assert.Equal(t, 2, resp.Page)
	assert.Empty(t, resp.Rooms, "page 2 of 48 is past the end")
	rec = anon.do(http.MethodGet, "/api/v1/rooms/public?page=0&perPage=10", nil)
	resp = decodeBody[directoryResponse](t, rec)
	assert.Equal(t, 1, resp.Page)
	assert.Len(t, resp.Rooms, 10)

	// Search and live filter.
	rec = anon.do(http.MethodGet, "/api/v1/rooms/public?q=READY", nil)
	resp = decodeBody[directoryResponse](t, rec)
	assert.Equal(t, 1, resp.Total)
	rec = anon.do(http.MethodGet, "/api/v1/rooms/public?live=1", nil)
	resp = decodeBody[directoryResponse](t, rec)
	assert.Equal(t, 1, resp.Total)
	assert.Equal(t, "pending-room", resp.Rooms[0].Slug)

	// "public" never resolves as a room slug.
	assert.Equal(t, http.StatusNotFound, anon.do(http.MethodGet, "/api/v1/rooms/public/members", nil).Code)
}

func itoa(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return string(digits[i])
	}
	return itoa(i/10) + string(digits[i%10])
}

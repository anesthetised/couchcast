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

func TestDirectory(t *testing.T) {
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
	require.NoError(t, repo.SetMediaProbed(ctx, m1.ID, "One", 60000, "http://t1", nil))
	require.NoError(t, repo.SetMediaReady(ctx, m1.ID, nil, 1, "p"))
	i1, _ := repo.AddQueueItem(ctx, repo.Pool(), ready.ID, m1.ID, &owner.ID)
	require.NoError(t, repo.UpdateRoomPlayback(ctx, ready.ID, entity.PlaybackState{CurrentItemID: &i1.ID, Playing: true, PositionMs: 1000, PositionAt: time.Now()}))

	m2, _, _ := repo.CreateMedia(ctx, repo.Pool(), "youtube:two", "https://youtu.be/two")
	i2, _ := repo.AddQueueItem(ctx, repo.Pool(), pending.ID, m2.ID, &owner.ID)
	require.NoError(t, repo.UpdateRoomPlayback(ctx, pending.ID, entity.PlaybackState{CurrentItemID: &i2.ID, PositionAt: time.Now()}))

	live.counts[pending.ID] = 2
	live.playback[ready.ID] = protocol.Playback{Playing: false, PositionMs: 4242, AtServerMs: 123}

	// Anonymous, default page: the playing room is live and comes first,
	// then the watched-but-pending room, then fillers; 32 total.
	rec := anon.do(http.MethodGet, "/api/v1/rooms", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := decodeBody[directoryResponse](t, rec)
	assert.Equal(t, 32, resp.Total)
	assert.Equal(t, 1, resp.Page)
	assert.Equal(t, 24, resp.PerPage)
	require.Len(t, resp.Rooms, 24)
	assert.Greater(t, resp.ServerNowMs, int64(0))

	first := resp.Rooms[0]
	assert.Equal(t, "ready-room", first.Slug)
	assert.True(t, first.Live)
	assert.Equal(t, 0, first.Viewers)
	require.NotNil(t, first.Media)
	assert.Equal(t, "/media/"+m1.ID.String()+"/manifest.mpd", first.Media.Manifest)
	assert.True(t, signer.Verify(first.Media.Token, m1.ID, time.Now()))
	require.NotNil(t, first.Playback)
	assert.EqualValues(t, 4242, first.Playback.PositionMs, "live clock wins over the persisted one")
	assert.False(t, first.Playback.Playing)

	second := resp.Rooms[1]
	assert.Equal(t, "pending-room", second.Slug)
	assert.False(t, second.Live)
	assert.Equal(t, 2, second.Viewers)
	require.NotNil(t, second.Media)
	assert.Empty(t, second.Media.Manifest, "not ready: no manifest or token")
	assert.Empty(t, second.Media.Token)

	assert.Nil(t, resp.Rooms[2].Media)
	for _, rm := range resp.Rooms {
		assert.NotEqual(t, "secret-room", rm.Slug)
	}

	// Pagination and bounds normalisation.
	rec = anon.do(http.MethodGet, "/api/v1/rooms?page=2&perPage=999", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	resp = decodeBody[directoryResponse](t, rec)
	assert.Equal(t, 48, resp.PerPage)
	assert.Equal(t, 2, resp.Page)
	assert.Empty(t, resp.Rooms, "page 2 of 48 is past the end")
	rec = anon.do(http.MethodGet, "/api/v1/rooms?page=0&perPage=10", nil)
	resp = decodeBody[directoryResponse](t, rec)
	assert.Equal(t, 1, resp.Page)
	assert.Len(t, resp.Rooms, 10)

	// Search and live filter.
	rec = anon.do(http.MethodGet, "/api/v1/rooms?q=READY", nil)
	resp = decodeBody[directoryResponse](t, rec)
	assert.Equal(t, 1, resp.Total)
	rec = anon.do(http.MethodGet, "/api/v1/rooms?live=1", nil)
	resp = decodeBody[directoryResponse](t, rec)
	assert.Equal(t, 1, resp.Total)
	assert.Equal(t, "ready-room", resp.Rooms[0].Slug)

	// Signed-in members see their private rooms and their role; anonymous
	// visitors never do, and private/mine are ignored for them.
	_, err = repo.CreateRoom(ctx, "secret-two", "Secret two", owner.ID, entity.VisibilityPrivate, entity.DefaultSettings())
	require.NoError(t, err)
	member := &dbEnv{&testEnv{t: t, handler: srv.Handler()}}
	member.register("member")
	memberUser, _ := repo.GetUserByUsername(ctx, "member")
	secret, _ := repo.GetRoomBySlug(ctx, "secret-room")
	require.NoError(t, repo.UpsertMember(ctx, secret.ID, memberUser.ID, entity.RoomRoleMember))

	rec = member.do(http.MethodGet, "/api/v1/rooms?private=1", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	resp = decodeBody[directoryResponse](t, rec)
	require.Equal(t, 1, resp.Total)
	assert.Equal(t, "secret-room", resp.Rooms[0].Slug)
	assert.Equal(t, "private", string(resp.Rooms[0].Visibility))
	assert.Equal(t, "member", string(resp.Rooms[0].MyRole))

	rec = member.do(http.MethodGet, "/api/v1/rooms?mine=1", nil)
	resp = decodeBody[directoryResponse](t, rec)
	assert.Equal(t, 1, resp.Total)

	rec = anon.do(http.MethodGet, "/api/v1/rooms?private=1&mine=1", nil)
	resp = decodeBody[directoryResponse](t, rec)
	assert.Equal(t, 32, resp.Total, "filters requiring a viewer are ignored for anonymous")
	for _, rm := range resp.Rooms {
		assert.Equal(t, "public", string(rm.Visibility))
		assert.Empty(t, rm.MyRole)
	}
}

func itoa(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return string(digits[i])
	}
	return itoa(i/10) + string(digits[i%10])
}

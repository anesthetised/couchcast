package apihttp

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/metrics"
	"github.com/anesthetised/couchcast/internal/ratelimit"
	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

func TestRoomMeta(t *testing.T) {
	repo := repository.New(repotest.Pool(t))
	static := fstest.MapFS{"index.html": {Data: []byte("<html><head><title>couchcast</title></head><body></body></html>")}}
	// A fresh router per phase: the meta cache lives in the router, and
	// newDBHandler would reset the database.
	build := func() http.Handler {
		return New(Deps{
			Logger: slog.New(slog.DiscardHandler), DB: repo, Metrics: metrics.New("test"), Static: static,
			Users: repo, Rooms: repo, Meta: repo, Sessions: auth.NewSessions(repo, time.Hour, false),
			AuthLimiter: ratelimit.New(6000, 1000), LoginLimiter: ratelimit.New(6000, 1000),
		}).Handler()
	}
	env := &dbEnv{&testEnv{t: t, handler: build()}}
	env.register("owner")
	ctx := context.Background()
	rec := env.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "Movie <night>", "slug": "movies", "description": "Fridays & more"})
	require.Equal(t, http.StatusCreated, rec.Code)
	rec = env.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "Secret", "slug": "secret", "visibility": "private"})
	require.Equal(t, http.StatusCreated, rec.Code)

	// Public room: escaped tags, title replaced, description used.
	rec = env.do(http.MethodGet, "/r/movies", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `<meta property="og:title" content="Movie &lt;night&gt;">`)
	assert.Contains(t, body, `<meta property="og:description" content="Fridays &amp; more">`)
	assert.Contains(t, body, `<meta property="og:url" content="http://example.com/r/movies">`)
	assert.Contains(t, body, "<title>Movie &lt;night&gt; · couchcast</title>")
	assert.NotContains(t, body, "<title>couchcast</title>")

	// Private and unknown rooms unfurl as the plain shell.
	for _, p := range []string{"/r/secret", "/r/nope", "/r/movies/settings"} {
		rec = env.do(http.MethodGet, p, nil)
		require.Equal(t, http.StatusOK, rec.Code, p)
		assert.NotContains(t, rec.Body.String(), "og:title", p)
		assert.Contains(t, rec.Body.String(), "<title>couchcast</title>", p)
	}

	// With a playing video the description reports it and the image is its
	// thumbnail; the cache means the change shows up on a fresh handler.
	room, err := repo.GetRoomBySlug(ctx, "movies")
	require.NoError(t, err)
	m, _, err := repo.CreateMedia(ctx, repo.Pool(), "url:x", "https://x")
	require.NoError(t, err)
	require.NoError(t, repo.SetMediaProbed(ctx, m.ID, "Big Buck Bunny", 1000, "https://img/bbb.jpg", nil))
	require.NoError(t, repo.SetMediaReady(ctx, m.ID, nil, 1, "p"))
	item, err := repo.AddQueueItem(ctx, repo.Pool(), room.ID, m.ID, nil)
	require.NoError(t, err)
	require.NoError(t, repo.UpdateRoomPlayback(ctx, room.ID, entity.PlaybackState{CurrentItemID: &item.ID, Playing: true}))
	env2 := &dbEnv{&testEnv{t: t, handler: build(), cookies: env.cookies}}
	rec = env2.do(http.MethodGet, "/r/movies", nil)
	body = rec.Body.String()
	assert.Contains(t, body, `content="Fridays &amp; more · Playing “Big Buck Bunny” · 0 watching"`)
	assert.Contains(t, body, `<meta property="og:image" content="https://img/bbb.jpg">`)

	// A self-hosted poster is a path and comes out absolute.
	require.NoError(t, repo.SetMediaThumbnail(ctx, m.ID, "/media/"+m.ID.String()+"/thumb.jpg"))
	env3 := &dbEnv{&testEnv{t: t, handler: build(), cookies: env.cookies}}
	body = env3.do(http.MethodGet, "/r/movies", nil).Body.String()
	assert.Contains(t, body, `<meta property="og:image" content="http://example.com/media/`+m.ID.String()+`/thumb.jpg">`)
}

package apihttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/ingest"
	"github.com/anesthetised/couchcast/internal/ratelimit"
	"github.com/anesthetised/couchcast/internal/source"
)

type fakeProber struct{}

func (fakeProber) Preview(ctx context.Context, raw string) (*ingest.Preview, error) {
	switch raw {
	case "bad":
		return nil, ingest.ErrUnsupportedURL
	case "https://blocked":
		return nil, ingest.ErrBlocked
	case "https://slow":
		return nil, context.DeadlineExceeded
	case "http://10.0.0.1":
		return nil, fmt.Errorf("%w: 10.0.0.1", ingest.ErrPrivateAddress)
	case "https://nowhere":
		return nil, fmt.Errorf("%w: lookup nowhere: no such host", ingest.ErrUnknownHost)
	case "https://private":
		return nil, errors.New("yt-dlp: private video")
	case "https://known":
		return &ingest.Preview{Title: "Known", DurationMs: 1000, Status: entity.MediaReady}, nil
	}
	return &ingest.Preview{Title: "Fresh", DurationMs: 42_000, ThumbnailURL: "https://img/x.jpg"}, nil
}

func (fakeProber) PlaylistURL(raw string) (string, bool, bool) {
	switch raw {
	case "https://list":
		return "https://list", false, true
	case "https://video-in-list":
		return "https://list", true, true
	}
	return "", false, false
}

func (fakeProber) Playlist(ctx context.Context, raw string) (*source.Playlist, error) {
	switch raw {
	case "https://list":
	case "http://10.0.0.1/list":
		return nil, ingest.ErrPrivateAddress
	default:
		return nil, ingest.ErrUnsupportedURL
	}
	return &source.Playlist{Title: "Basics", Total: 9, Entries: []source.PlaylistEntry{{URL: "https://a", Title: "A", DurationMs: 1000}}}, nil
}

func TestPlaylist(t *testing.T) {
	env := newTestEnv(t, fstest.MapFS{}, func(d *Deps) { d.Prober = fakeProber{} })
	assert.Equal(t, http.StatusUnauthorized, env.do(http.MethodGet, "/api/v1/media/playlist?url=https://list", nil).Code)
	assert.Equal(t, http.StatusCreated, env.do(http.MethodPost, "/api/v1/auth/register", credentials{Username: "erin", Password: "password-123"}).Code)

	// A bare playlist link is marked without probing; a video inside a
	// playlist is probed and marked.
	probe := decodeBody[probeResponse](t, env.do(http.MethodGet, "/api/v1/media/probe?url=https://list", nil))
	assert.Equal(t, probeResponse{PlaylistURL: "https://list"}, probe)
	probe = decodeBody[probeResponse](t, env.do(http.MethodGet, "/api/v1/media/probe?url=https://video-in-list", nil))
	assert.Equal(t, "Fresh", probe.Title)
	assert.Equal(t, "https://list", probe.PlaylistURL)

	pl := decodeBody[playlistResponse](t, env.do(http.MethodGet, "/api/v1/media/playlist?url=https://list", nil))
	assert.Equal(t, playlistResponse{Title: "Basics", Total: 9, Entries: []playlistEntryResponse{{URL: "https://a", Title: "A", DurationMs: 1000}}}, pl)
	assert.Equal(t, http.StatusBadRequest, env.do(http.MethodGet, "/api/v1/media/playlist?url=https://fresh", nil).Code)
	rec := env.do(http.MethodGet, "/api/v1/media/playlist?url=http://10.0.0.1/list", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, msgPrivateAddress, decodeBody[errorResponse](t, rec).Error)
}

func TestProbe(t *testing.T) {
	env := newTestEnv(t, fstest.MapFS{}, func(d *Deps) {
		d.Prober = fakeProber{}
		d.ProbeLimiter = ratelimit.New(600, 3)
	})

	rec := env.do(http.MethodGet, "/api/v1/media/probe?url=https://x", nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "signed out")

	rec = env.do(http.MethodPost, "/api/v1/auth/register", credentials{Username: "carol", Password: "password-123"})
	assert.Equal(t, http.StatusCreated, rec.Code)
	cases := []struct {
		url  string
		code int
	}{
		{"", http.StatusBadRequest},
		{"bad", http.StatusBadRequest},
		{"https://blocked", http.StatusBadRequest},
		{"https://slow", http.StatusGatewayTimeout},
	}
	for _, c := range cases {
		rec = env.do(http.MethodGet, "/api/v1/media/probe?url="+c.url, nil)
		assert.Equal(t, c.code, rec.Code, c.url)
	}
	// Budget of three is spent; the fourth call is throttled.
	rec = env.do(http.MethodGet, "/api/v1/media/probe?url=https://private", nil)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)

	env2 := newTestEnv(t, fstest.MapFS{}, func(d *Deps) { d.Prober = fakeProber{} })
	rec = env2.do(http.MethodPost, "/api/v1/auth/register", credentials{Username: "dave", Password: "password-123"})
	assert.Equal(t, http.StatusCreated, rec.Code)
	rec = env2.do(http.MethodGet, "/api/v1/media/probe?url=https://private", nil)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	// Links into the server's own network are refused with a plain
	// answer, never the fetcher's error.
	rec = env2.do(http.MethodGet, "/api/v1/media/probe?url=http://10.0.0.1", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, msgPrivateAddress, decodeBody[errorResponse](t, rec).Error)
	rec = env2.do(http.MethodGet, "/api/v1/media/probe?url=https://nowhere", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, msgUnknownHost, decodeBody[errorResponse](t, rec).Error)

	rec = env2.do(http.MethodGet, "/api/v1/media/probe?url=https://fresh", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, probeResponse{Title: "Fresh", DurationMs: 42_000, ThumbnailURL: "https://img/x.jpg"}, decodeBody[probeResponse](t, rec))

	rec = env2.do(http.MethodGet, "/api/v1/media/probe?url=https://known", nil)
	assert.Equal(t, entity.MediaReady, decodeBody[probeResponse](t, rec).Status)
}

// slowProber blocks previews until released, to occupy slots.
type slowProber struct {
	fakeProber
	started chan struct{}
	release chan struct{}
}

func (p slowProber) Preview(ctx context.Context, raw string) (*ingest.Preview, error) {
	p.started <- struct{}{}
	select {
	case <-p.release:
	case <-ctx.Done():
	}
	return &ingest.Preview{Title: "slow"}, nil
}

func TestProbeBudgets(t *testing.T) {
	t.Run("per address, across accounts", func(t *testing.T) {
		env := newTestEnv(t, fstest.MapFS{}, func(d *Deps) {
			d.Prober = fakeProber{}
			d.ProbeLimiter = ratelimit.New(600, 100)
			d.ProbeIPLimiter = ratelimit.New(1, 2) // two, then one a minute
		})
		for _, name := range []string{"alice", "bob"} {
			user := &testEnv{t: t, store: env.store, handler: env.handler}
			require.Equal(t, http.StatusCreated, user.do(http.MethodPost, "/api/v1/auth/register", credentials{Username: name, Password: "password-123"}).Code)
			for i := 0; i < 2; i++ {
				code := user.do(http.MethodGet, "/api/v1/media/probe?url=https://x", nil).Code
				if name == "alice" {
					assert.Equal(t, http.StatusOK, code)
				} else {
					assert.Equal(t, http.StatusTooManyRequests, code, "same address, new account")
				}
			}
		}
	})

	t.Run("a bounded number at once", func(t *testing.T) {
		p := slowProber{started: make(chan struct{}, 4), release: make(chan struct{})}
		env := newTestEnv(t, fstest.MapFS{}, func(d *Deps) {
			d.Prober = p
			d.ProbeConcurrency = 1
			d.ProbeWait = 50 * time.Millisecond
		})
		require.Equal(t, http.StatusCreated, env.do(http.MethodPost, "/api/v1/auth/register", credentials{Username: "dora", Password: "password-123"}).Code)

		// A second browser of the same user, so the two requests do not
		// share a cookie jar across goroutines.
		other := &testEnv{t: t, store: env.store, handler: env.handler, cookies: append([]*http.Cookie(nil), env.cookies...)}
		first := make(chan int)
		go func() { first <- other.do(http.MethodGet, "/api/v1/media/probe?url=https://slow", nil).Code }()
		<-p.started
		// The only slot is taken: the next one waits, then gives up.
		rec := env.do(http.MethodGet, "/api/v1/media/probe?url=https://slow", nil)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.Equal(t, "5", rec.Header().Get("Retry-After"))

		close(p.release)
		assert.Equal(t, http.StatusOK, <-first)
		// Freed: the next request runs.
		go func() { <-p.started }()
		assert.Equal(t, http.StatusOK, env.do(http.MethodGet, "/api/v1/media/probe?url=https://slow", nil).Code)
	})
}

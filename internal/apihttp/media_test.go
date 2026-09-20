package apihttp

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"

	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/ingest"
	"github.com/anesthetised/couchcast/internal/ratelimit"
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
	case "https://private":
		return nil, errors.New("yt-dlp: private video")
	case "https://known":
		return &ingest.Preview{Title: "Known", DurationMs: 1000, Status: entity.MediaReady}, nil
	}
	return &ingest.Preview{Title: "Fresh", DurationMs: 42_000, ThumbnailURL: "https://img/x.jpg"}, nil
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

	rec = env2.do(http.MethodGet, "/api/v1/media/probe?url=https://fresh", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, probeResponse{Title: "Fresh", DurationMs: 42_000, ThumbnailURL: "https://img/x.jpg"}, decodeBody[probeResponse](t, rec))

	rec = env2.do(http.MethodGet, "/api/v1/media/probe?url=https://known", nil)
	assert.Equal(t, entity.MediaReady, decodeBody[probeResponse](t, rec).Status)
}

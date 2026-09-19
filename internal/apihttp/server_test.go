package apihttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"

	"github.com/anesthetised/couchcast/internal/metrics"
)

type pingerFunc func(context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

func newTestServer(db Pinger, static fstest.MapFS) *Server {
	return New(Deps{
		Logger:  slog.New(slog.DiscardHandler),
		DB:      db,
		Metrics: metrics.New("test"),
		Static:  static,
	})
}

func TestHealthz(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		srv := newTestServer(pingerFunc(func(context.Context) error { return nil }), fstest.MapFS{})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"status":"ok","version":"dev"}`, rec.Body.String())
	})

	t.Run("degraded", func(t *testing.T) {
		srv := newTestServer(pingerFunc(func(context.Context) error { return errors.New("down") }), fstest.MapFS{})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})
}

func TestSPAFallback(t *testing.T) {
	static := fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("js")},
	}
	srv := newTestServer(pingerFunc(func(context.Context) error { return nil }), static)

	get := func(p string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		return rec
	}

	assert.Equal(t, "js", get("/assets/app.js").Body.String())
	assert.Equal(t, "<html>app</html>", get("/r/some-room").Body.String())
	assert.Equal(t, "<html>app</html>", get("/").Body.String())
	assert.Equal(t, http.StatusNotFound, get("/api/v1/nope").Code)
	assert.JSONEq(t, `{"error":"not found"}`, get("/api/v1/nope").Body.String())
}

func TestSPAWithoutBundle(t *testing.T) {
	srv := newTestServer(pingerFunc(func(context.Context) error { return nil }), fstest.MapFS{".gitkeep": {}})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not built")
}

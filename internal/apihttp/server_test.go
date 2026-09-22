package apihttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
)

func TestHealthz(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		env := newTestEnv(t, fstest.MapFS{})
		rec := env.do(http.MethodGet, "/healthz", nil)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"status":"ok","version":"dev"}`, rec.Body.String())
	})

	t.Run("degraded", func(t *testing.T) {
		env := newTestEnv(t, fstest.MapFS{})
		env.store.pingErr = errors.New("down")
		rec := env.do(http.MethodGet, "/healthz", nil)

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})
}

func TestSPAFallback(t *testing.T) {
	static := fstest.MapFS{
		"index.html":           {Data: []byte("<html>app</html>")},
		"assets/app.js":        {Data: []byte("js")},
		"sw.js":                {Data: []byte("sw")},
		"manifest.webmanifest": {Data: []byte("{}")},
	}
	env := newTestEnv(t, static)

	get := func(p string) *httptest.ResponseRecorder { return env.do(http.MethodGet, p, nil) }

	assert.Equal(t, "js", get("/assets/app.js").Body.String())
	// The PWA files are served uncached with the manifest media type.
	assert.Equal(t, "no-cache", get("/sw.js").Header().Get("Cache-Control"))
	manifest := get("/manifest.webmanifest")
	assert.Equal(t, "no-cache", manifest.Header().Get("Cache-Control"))
	assert.Equal(t, "application/manifest+json", manifest.Header().Get("Content-Type"))
	assert.Equal(t, "<html>app</html>", get("/r/some-room").Body.String())
	assert.Equal(t, "<html>app</html>", get("/").Body.String())
	assert.Equal(t, http.StatusNotFound, get("/api/v1/nope").Code)
	assert.JSONEq(t, `{"error":"not found"}`, get("/api/v1/nope").Body.String())
}

func TestSPAWithoutBundle(t *testing.T) {
	env := newTestEnv(t, fstest.MapFS{".gitkeep": {}})
	rec := env.do(http.MethodGet, "/", nil)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "not built")
}

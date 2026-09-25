package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	b, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	return string(b)
}

func TestMetricsAreExported(t *testing.T) {
	m := New("web")
	m.WSConnectionDelta(1)
	m.WSConnectionDelta(1)
	m.WSConnectionDelta(-1)
	m.RegisterRoomsLoaded(func() float64 { return 3 })
	m.IngestJob("done")
	m.IngestStep("download", 1500*time.Millisecond)
	m.MediaProxied(2048)
	assert.NotNil(t, m.Registry())

	// Requests are counted by route pattern, not by concrete path.
	r := chi.NewRouter()
	r.Use(m.HTTPMiddleware)
	r.Get("/rooms/{slug}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	for _, p := range []string{"/rooms/a", "/rooms/b", "/nowhere"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}

	out := scrape(t, m)
	for _, want := range []string{
		`couchcast_ws_connections{process="web"} 1`,
		`couchcast_rooms_loaded 3`,
		`couchcast_ingest_jobs_total{process="web",result="done"} 1`,
		`couchcast_ingest_step_duration_seconds_count{process="web",step="download"} 1`,
		`couchcast_media_proxied_bytes_total{process="web"} 2048`,
		`couchcast_http_requests_total{method="GET",process="web",route="/rooms/{slug}",status="418"} 2`,
		`couchcast_http_requests_total{method="GET",process="web",route="unmatched",status="404"} 1`,
	} {
		assert.Contains(t, out, want)
	}
}

func TestNilMetricsAreSafe(t *testing.T) {
	var m *Metrics
	m.WSConnectionDelta(1)
	m.RegisterRoomsLoaded(func() float64 { return 1 })
	m.IngestJob("failed")
	m.IngestStep("probe", time.Second)
	m.MediaProxied(1)
}

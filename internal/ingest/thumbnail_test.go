package ingest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchThumbnail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpegbytes"))
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>"))
		case "/huge.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(strings.Repeat("x", thumbnailMaxBytes+10)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	ctx := context.Background()

	name, err := fetchThumbnail(ctx, srv.Client(), srv.URL+"/ok.jpg", dir)
	require.NoError(t, err)
	assert.Equal(t, "thumb.jpg", name)
	b, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	assert.Equal(t, "jpegbytes", string(b))

	_, err = fetchThumbnail(ctx, srv.Client(), srv.URL+"/page", dir)
	assert.Error(t, err, "not an image")
	_, err = fetchThumbnail(ctx, srv.Client(), srv.URL+"/missing.jpg", dir)
	assert.Error(t, err)
	_, err = fetchThumbnail(ctx, srv.Client(), srv.URL+"/huge.png", dir)
	assert.Error(t, err, "over the size cap")
	_, err = os.Stat(filepath.Join(dir, "thumb.png"))
	assert.True(t, os.IsNotExist(err), "oversized file removed")
	_, err = fetchThumbnail(ctx, srv.Client(), "", dir)
	assert.Error(t, err)

	assert.Equal(t, "thumb.webp", ThumbnailFile("image/webp; charset=binary"))
	assert.Equal(t, "", ThumbnailFile("image/gif"))
}

func TestProgressEstimate(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := &progressReporter{totalBytes: 100 << 20}

	speed, eta := p.estimate(start, 0)
	assert.Zero(t, speed)
	assert.Zero(t, eta)

	// 10 % in 2 s of a 100 MB file: 5 MB/s, 18 s to go.
	speed, eta = p.estimate(start.Add(2*time.Second), 0.1)
	assert.EqualValues(t, 5<<20, speed)
	assert.EqualValues(t, 18_000, eta)

	// The window drops old samples: a stall then a burst is measured over
	// the recent samples only, not since the beginning.
	speed, eta = p.estimate(start.Add(20*time.Second), 0.1)
	assert.Zero(t, speed, "no progress inside the window")
	assert.Zero(t, eta)
	speed, _ = p.estimate(start.Add(21*time.Second), 0.2)
	assert.EqualValues(t, 10<<20, speed)

	// Without a byte total there is an ETA but no throughput.
	q := &progressReporter{}
	q.estimate(start, 0)
	speed, eta = q.estimate(start.Add(4*time.Second), 0.5)
	assert.Zero(t, speed)
	assert.EqualValues(t, 4_000, eta)
}

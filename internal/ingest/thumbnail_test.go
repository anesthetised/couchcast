package ingest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

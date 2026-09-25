package mediastore

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"uuid"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/config"
	"github.com/anesthetised/couchcast/internal/entity"
)

// testStore connects to the S3 server named by COUCHCAST_TEST_S3_ENDPOINT
// (SeaweedFS in compose and CI) with a fresh bucket that is emptied and
// removed after the test. Without the variable the test is skipped.
func testStore(t *testing.T) *Store {
	t.Helper()
	endpoint := os.Getenv("COUCHCAST_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("COUCHCAST_TEST_S3_ENDPOINT not set")
	}
	s, err := New(config.S3Config{
		Endpoint:  endpoint,
		Bucket:    "test-" + strings.ReplaceAll(uuid.New().String(), "-", "")[:20],
		AccessKey: os.Getenv("COUCHCAST_TEST_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("COUCHCAST_TEST_S3_SECRET_KEY"),
	})
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, s.EnsureBucket(ctx))
	t.Cleanup(func() {
		_ = s.DeletePrefix(ctx, "")
		_ = s.client.RemoveBucket(ctx, s.bucket)
	})
	return s
}

// writeTree creates files (name → content) under a temp dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	}
	return dir
}

func (s *Store) keys(t *testing.T, prefix string) []string {
	t.Helper()
	var out []string
	for obj := range s.client.ListObjects(context.Background(), s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		require.NoError(t, obj.Err)
		out = append(out, obj.Key)
	}
	return out
}

func TestStoreEnsureBucket(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	require.NoError(t, s.EnsureBucket(ctx), "idempotent")

	bad, err := New(config.S3Config{Endpoint: os.Getenv("COUCHCAST_TEST_S3_ENDPOINT"), Bucket: s.bucket, AccessKey: "nobody", SecretKey: "wrong-secret"})
	require.NoError(t, err)
	assert.Error(t, bad.EnsureBucket(ctx), "wrong credentials")
}

func TestStoreUploadOpenDelete(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	dir := writeTree(t, map[string]string{
		"manifest.mpd":      "<MPD/>",
		"chunk-0-00001.m4s": "0123456789",
		"sub/en.vtt":        "WEBVTT",
	})

	n, err := s.UploadDir(ctx, Prefix("one"), dir)
	require.NoError(t, err)
	assert.EqualValues(t, 6+10+6, n)
	_, err = s.UploadDir(ctx, Prefix("two"), writeTree(t, map[string]string{"manifest.mpd": "x"}))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"media/one/manifest.mpd", "media/one/chunk-0-00001.m4s", "media/one/sub/en.vtt"}, s.keys(t, Prefix("one")))

	// Objects keep their media type and read back, also from an offset.
	obj, err := s.Open(ctx, "media/one/chunk-0-00001.m4s")
	require.NoError(t, err)
	info, err := obj.Stat()
	require.NoError(t, err)
	assert.Equal(t, "video/iso.segment", info.ContentType)
	_, err = obj.Seek(4, io.SeekStart)
	require.NoError(t, err)
	rest, err := io.ReadAll(obj)
	require.NoError(t, err)
	assert.Equal(t, "456789", string(rest))
	require.NoError(t, obj.Close())

	// Deleting a prefix leaves other media alone.
	require.NoError(t, s.DeletePrefix(ctx, Prefix("one")))
	assert.Empty(t, s.keys(t, Prefix("one")))
	assert.Len(t, s.keys(t, Prefix("two")), 1)
	require.NoError(t, s.DeletePrefix(ctx, Prefix("missing")))

	_, err = s.UploadDir(ctx, Prefix("x"), filepath.Join(dir, "nope"))
	assert.Error(t, err)
}

type touches struct {
	mu  sync.Mutex
	ids []uuid.UUID
}

func (r *touches) TouchMediaAccess(_ context.Context, id uuid.UUID, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, id)
	return nil
}

func TestHandlerServesFromStorage(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id := uuid.New()
	_, err := s.UploadDir(ctx, Prefix(id.String()), writeTree(t, map[string]string{
		"manifest.mpd":      "<MPD/>",
		"chunk-0-00001.m4s": "0123456789",
		"thumb.jpg":         "jpeg",
	}))
	require.NoError(t, err)

	signer := NewSigner("0123456789abcdef0123456789abcdef", time.Hour)
	rec := &touches{}
	var served int64
	h := NewHandler(s, signer, rec, slog.New(slog.DiscardHandler), func(n int64) { served += n })
	token := signer.Sign(id, time.Now())
	get := func(method, file, tok string, hdr map[string]string) *httptest.ResponseRecorder {
		url := "/media/" + id.String() + "/" + file
		if tok != "" {
			url += "?t=" + tok
		}
		req := httptest.NewRequest(method, url, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	// Segments need the token for this very media.
	assert.Equal(t, http.StatusUnauthorized, get(http.MethodGet, "chunk-0-00001.m4s", "", nil).Code)
	assert.Equal(t, http.StatusUnauthorized, get(http.MethodGet, "chunk-0-00001.m4s", signer.Sign(uuid.New(), time.Now()), nil).Code)

	w := get(http.MethodGet, "manifest.mpd", token, nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "<MPD/>", w.Body.String())
	assert.Equal(t, "application/dash+xml", w.Header().Get("Content-Type"))
	assert.Equal(t, "private, max-age=3600", w.Header().Get("Cache-Control"))
	assert.NotEmpty(t, w.Header().Get("ETag"))
	assert.Equal(t, []uuid.UUID{id}, rec.ids, "the manifest marks the media as used")

	// Players read segments in ranges.
	w = get(http.MethodGet, "chunk-0-00001.m4s", token, map[string]string{"Range": "bytes=2-5"})
	require.Equal(t, http.StatusPartialContent, w.Code)
	assert.Equal(t, "2345", w.Body.String())
	assert.Equal(t, "bytes 2-5/10", w.Header().Get("Content-Range"))
	assert.EqualValues(t, 6+4, served)

	w = get(http.MethodHead, "chunk-0-00001.m4s", token, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String())

	// Posters are public and cacheable.
	w = get(http.MethodGet, "thumb.jpg", "", nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "public, max-age=86400", w.Header().Get("Cache-Control"))

	assert.Equal(t, http.StatusNotFound, get(http.MethodGet, "missing.m4s", token, nil).Code)
	assert.Equal(t, http.StatusNotFound, get(http.MethodGet, "../other/manifest.mpd", token, nil).Code)
	assert.Equal(t, http.StatusMethodNotAllowed, get(http.MethodPost, "manifest.mpd", token, nil).Code)
}

// evictionRepo is an in-memory EvictionStore.
type evictionRepo struct {
	media   []entity.Media
	deleted []uuid.UUID
}

func (r *evictionRepo) SumReadyMediaBytes(context.Context) (int64, error) {
	var n int64
	for _, m := range r.media {
		n += m.SizeBytes
	}
	return n, nil
}

func (r *evictionRepo) ListEvictableMedia(context.Context, int) ([]entity.Media, error) {
	return r.media, nil
}

func (r *evictionRepo) DeleteMedia(_ context.Context, id uuid.UUID) error {
	r.deleted = append(r.deleted, id)
	for i, m := range r.media {
		if m.ID == id {
			r.media = append(r.media[:i], r.media[i+1:]...)
			break
		}
	}
	return nil
}

func TestEvictorKeepsTheCacheUnderBudget(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	old, older, fresh := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{old, older, fresh} {
		_, err := s.UploadDir(ctx, Prefix(id.String()), writeTree(t, map[string]string{"manifest.mpd": "x"}))
		require.NoError(t, err)
	}
	now := time.Now()
	// Least recently used first, as the repository lists them.
	repo := &evictionRepo{media: []entity.Media{
		{ID: fresh, SizeBytes: 400, LastAccessedAt: now},
		{ID: older, SizeBytes: 300, LastAccessedAt: now.Add(-3 * time.Hour)},
		{ID: old, SizeBytes: 300, LastAccessedAt: now.Add(-2 * time.Hour)},
	}}
	logger := slog.New(slog.DiscardHandler)

	disabled, err := NewEvictor(s, repo, 0, logger).Run(ctx)
	require.NoError(t, err)
	assert.Zero(t, disabled)
	under, err := NewEvictor(s, repo, 2000, logger).Run(ctx)
	require.NoError(t, err)
	assert.Zero(t, under)

	// 1000 bytes against a budget of 500: the fresh one is protected, the
	// older one goes first and that is enough.
	n, err := NewEvictor(s, repo, 700, logger).Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, []uuid.UUID{older}, repo.deleted)
	assert.Empty(t, s.keys(t, Prefix(older.String())))
	assert.NotEmpty(t, s.keys(t, Prefix(old.String())))
	assert.NotEmpty(t, s.keys(t, Prefix(fresh.String())))

	// With nothing old enough left, it stops over budget.
	n, err = NewEvictor(s, repo, 100, logger).Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.NotEmpty(t, s.keys(t, Prefix(fresh.String())))
}

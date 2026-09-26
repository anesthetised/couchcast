package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/ingest"
	"github.com/anesthetised/couchcast/internal/jobs"
	"github.com/anesthetised/couchcast/internal/mediastore/storetest"
	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

// env points the commands at the test database and a throwaway bucket,
// as the environment would in a deployment.
func env(t *testing.T) *repository.Repo {
	t.Helper()
	repo := repository.New(repotest.Pool(t))
	s3 := storetest.Config(t)
	for k, v := range map[string]string{
		"COUCHCAST_DATABASE_URL":  os.Getenv("COUCHCAST_TEST_DATABASE_URL"),
		"COUCHCAST_S3_ENDPOINT":   s3.Endpoint,
		"COUCHCAST_S3_BUCKET":     s3.Bucket,
		"COUCHCAST_S3_ACCESS_KEY": s3.AccessKey,
		"COUCHCAST_S3_SECRET_KEY": s3.SecretKey,
		"COUCHCAST_S3_USE_SSL":    "false",
		"COUCHCAST_LOG_LEVEL":     "error",
	} {
		t.Setenv(k, v)
	}
	return repo
}

// freeAddr returns a loopback address nothing listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

// start runs a long-lived command until the test stops it; stop returns
// what the command returned, where context.Canceled is the clean exit
// main expects.
func start(t *testing.T, args ...string) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, args) }()
	return func() error {
		cancel()
		select {
		case err := <-done:
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		case <-time.After(15 * time.Second):
			t.Fatal("command did not stop")
			return nil
		}
	}
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec,noctx // test server
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestAdminCommand(t *testing.T) {
	repo := env(t)
	ctx := context.Background()
	u, err := repo.CreateUser(ctx, "Alice", "hash")
	require.NoError(t, err)

	require.NoError(t, run(ctx, []string{"admin", "grant", " alice "}))
	got, err := repo.GetUserByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.RoleAdmin, got.Role)
	require.NoError(t, run(ctx, []string{"admin", "revoke", "alice"}))
	got, _ = repo.GetUserByID(ctx, u.ID)
	assert.Equal(t, entity.RoleUser, got.Role)

	for _, args := range [][]string{{"admin"}, {"admin", "grant"}, {"admin", "promote", "alice"}, {"admin", "grant", "nobody"}} {
		assert.Error(t, run(ctx, args), args)
	}
}

func TestSimpleCommands(t *testing.T) {
	ctx := context.Background()
	for _, args := range [][]string{{"version"}, {"help"}, {"vapid"}} {
		assert.NoError(t, run(ctx, args), args)
	}
	assert.Error(t, run(ctx, []string{"teleport"}))
}

// A yt-dlp that knows no video, the way it says so.
const missingVideo = "#!/bin/sh\necho 'ERROR: [generic] stub: no such video' >&2\nexit 1\n"

func TestIngestCommand(t *testing.T) {
	repo := env(t)
	ctx := context.Background()
	stub := filepath.Join(t.TempDir(), "yt-dlp")
	require.NoError(t, os.WriteFile(stub, []byte(missingVideo), 0o700)) //nolint:gosec // executable test stub
	metrics := freeAddr(t)
	t.Setenv("COUCHCAST_YTDLP_PATH", stub)
	t.Setenv("COUCHCAST_WORK_DIR", t.TempDir())
	t.Setenv("COUCHCAST_INGEST_WORKERS", "1")
	t.Setenv("COUCHCAST_INGEST_METRICS_ADDR", metrics)

	// A job waiting before the worker starts, as after a deploy.
	m, _, err := repo.CreateMedia(ctx, repo.Pool(), "youtube:aaaaaaaaaaa", "https://www.youtube.com/watch?v=aaaaaaaaaaa")
	require.NoError(t, err)
	_, err = jobs.New(repo.Pool()).Enqueue(ctx, nil, ingest.JobKind, ingest.Payload{MediaID: m.ID}, 1)
	require.NoError(t, err)

	stop := start(t, "ingest")
	require.Eventually(t, func() bool {
		got, err := repo.GetMedia(ctx, m.ID)
		return err == nil && got.Status == entity.MediaFailed
	}, 20*time.Second, 100*time.Millisecond, "the worker claims the job and runs the pipeline")
	got, _ := repo.GetMedia(ctx, m.ID)
	assert.Contains(t, got.Error, "no such video", "yt-dlp's reason reaches the media row")

	code, _ := get(t, "http://"+metrics+"/healthz")
	assert.Equal(t, http.StatusOK, code)
	code, body := get(t, "http://"+metrics+"/metrics")
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `couchcast_ingest_jobs_total{process="ingest",result="failed"} 1`)

	assert.NoError(t, stop(), "stops cleanly on cancel")
	code, _ = get(t, "http://"+metrics+"/healthz")
	assert.Zero(t, code, "the metrics server is gone")

	// Configuration problems stop it before it starts.
	t.Setenv("COUCHCAST_INGEST_WORKERS", "0")
	assert.ErrorContains(t, run(ctx, []string{"ingest"}), "INGEST_WORKERS")
}

// welcome connects a viewer and returns the room's playback from the
// welcome message.
func welcome(t *testing.T, addr string) (*websocket.Conn, map[string]any) {
	t.Helper()
	ctx := context.Background()
	c, resp, err := websocket.Dial(ctx, "ws://"+addr+"/api/v1/rooms/shutdown-room/ws", nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	var w struct {
		Type     string `json:"type"`
		Snapshot struct {
			Playback map[string]any `json:"playback"`
		} `json:"snapshot"`
	}
	require.NoError(t, wsjson.Read(ctx, c, &w))
	require.Equal(t, "welcome", w.Type)
	return c, w.Snapshot.Playback
}

func TestServeStopsCleanlyAndResumes(t *testing.T) {
	repo := env(t)
	ctx := context.Background()
	addr := freeAddr(t)
	t.Setenv("COUCHCAST_ADDR", addr)
	t.Setenv("COUCHCAST_SECURE_COOKIES", "false")
	t.Setenv("COUCHCAST_MEDIA_TOKEN_SECRET", strings.Repeat("s", 32))
	base := "http://" + addr

	// A room that was playing a ready video when the server last ran.
	owner, err := repo.CreateUser(ctx, "host", "hash")
	require.NoError(t, err)
	rm, err := repo.CreateRoom(ctx, "shutdown-room", "Shutdown", owner.ID, entity.VisibilityPublic, entity.DefaultSettings())
	require.NoError(t, err)
	m, _, err := repo.CreateMedia(ctx, repo.Pool(), "youtube:bbbbbbbbbbb", "https://www.youtube.com/watch?v=bbbbbbbbbbb")
	require.NoError(t, err)
	require.NoError(t, repo.SetMediaProbed(ctx, m.ID, "Clip", 600_000, "", nil))
	require.NoError(t, repo.SetMediaReady(ctx, m.ID, []entity.Rendition{{ID: "0", Height: 720}}, 1, "media/x/"))
	item, err := repo.AddQueueItem(ctx, repo.Pool(), rm.ID, m.ID, nil)
	require.NoError(t, err)
	require.NoError(t, repo.UpdateRoomPlayback(ctx, rm.ID, entity.PlaybackState{CurrentItemID: &item.ID, Playing: true, PositionMs: 10_000, PositionAt: time.Now(), Rate: 1}))
	up := func() {
		require.Eventually(t, func() bool { code, _ := get(t, base+"/healthz"); return code == http.StatusOK }, 20*time.Second, 100*time.Millisecond)
	}

	stop := start(t, "serve")
	up()
	code, body := get(t, base+"/api/v1/rooms/shutdown-room")
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `"name":"Shutdown"`)
	c, pb := welcome(t, addr)
	assert.Equal(t, true, pb["playing"], "the room resumed on start")
	assert.Equal(t, item.ID.String(), pb["itemId"])

	// Stopping tells the viewer to reconnect (going away, not a kick) and
	// returns cleanly.
	require.NoError(t, stop())
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var closeErr error
	for closeErr == nil {
		_, _, closeErr = c.Read(readCtx)
	}
	assert.Equal(t, websocket.StatusGoingAway, websocket.CloseStatus(closeErr))
	code, _ = get(t, base+"/healthz")
	assert.Zero(t, code, "nothing listens any more")

	// After a restart the room is still playing, further along.
	time.Sleep(time.Second)
	stop = start(t, "serve")
	up()
	c2, pb2 := welcome(t, addr)
	defer func() { _ = c2.CloseNow() }()
	assert.Equal(t, true, pb2["playing"])
	pos := pb2["positionMs"].(float64) + (float64(time.Now().UnixMilli()) - pb2["atServerMs"].(float64))
	assert.Greater(t, pos, 11_000.0, "the clock kept running across the restart")
	require.NoError(t, stop())
}

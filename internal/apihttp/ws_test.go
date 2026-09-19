package apihttp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"uuid"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/hub"
	"github.com/anesthetised/couchcast/internal/ingest"
	"github.com/anesthetised/couchcast/internal/jobs"
	"github.com/anesthetised/couchcast/internal/mediastore"
	"github.com/anesthetised/couchcast/internal/metrics"
	"github.com/anesthetised/couchcast/internal/protocol"
	"github.com/anesthetised/couchcast/internal/ratelimit"
	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/repository/repotest"
	"github.com/anesthetised/couchcast/internal/room"
	"github.com/anesthetised/couchcast/internal/source/ytdlp"
)

// wsMessage is a loosely typed server message for assertions.
type wsMessage map[string]any

func readMessage(t *testing.T, ctx context.Context, c *websocket.Conn) wsMessage {
	t.Helper()
	var m wsMessage
	require.NoError(t, wsjson.Read(ctx, c, &m))
	return m
}

// readUntil skips messages until one of the given type arrives.
func readUntil(t *testing.T, ctx context.Context, c *websocket.Conn, typ string) wsMessage {
	t.Helper()
	for {
		m := readMessage(t, ctx, c)
		if m["type"] == typ {
			return m
		}
	}
}

func TestWebSocketRoom(t *testing.T) {
	pool := repotest.Pool(t)
	repo := repository.New(pool)
	logger := slog.New(slog.DiscardHandler)

	admit := ingest.NewService(repo, jobs.New(pool), ytdlp.New("yt-dlp", nil, logger))
	rooms := room.NewManager(room.Deps{
		Store: repo, Admit: admit, Signer: mediastore.NewSigner("0123456789abcdef0123456789abcdef", time.Hour), Logger: logger,
	})

	var srv *Server
	h := hub.New(rooms, func(ctx context.Context, rm *entity.Room, u *entity.User) (access.Actor, error) {
		return srv.ActorFor(ctx, rm, u)
	}, logger, metrics.New("test"))

	srv = New(Deps{
		Logger: logger, DB: repo, Metrics: metrics.New("test2"), Static: fstest.MapFS{},
		Users: repo, Rooms: repo, Sessions: auth.NewSessions(repo, time.Hour, false),
		AuthLimiter: ratelimit.New(6000, 1000), LoginLimiter: ratelimit.New(6000, 1000),
		WS:    h,
		OnBan: func(roomID, userID uuid.UUID) { rooms.Kick(roomID, userID, "banned") },
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Register the owner over REST and keep the cookie.
	client := &http.Client{}
	resp, err := client.Post(ts.URL+"/api/v1/auth/register", "application/json",
		strings.NewReader(`{"username":"owner","password":"password-123"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	cookie := resp.Cookies()[0]
	_ = resp.Body.Close()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/v1/rooms", strings.NewReader(`{"name":"WS","slug":"ws-room"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	resp, err = client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	_ = resp.Body.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/rooms/ws-room/ws"

	// Owner connection.
	owner, ownerResp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": {cookie.String()}}})
	require.NoError(t, err)
	defer func() { _ = owner.CloseNow() }()
	if ownerResp != nil && ownerResp.Body != nil {
		_ = ownerResp.Body.Close()
	}

	welcome := readMessage(t, ctx, owner)
	assert.Equal(t, "welcome", welcome["type"])
	assert.Equal(t, "owner", welcome["me"])
	assert.Equal(t, "owner", welcome["role"])

	// Anonymous connection to a public room is allowed but read-only.
	anon, anonResp, err := websocket.Dial(ctx, wsURL, nil)
	require.NoError(t, err)
	defer func() { _ = anon.CloseNow() }()
	if anonResp != nil && anonResp.Body != nil {
		_ = anonResp.Body.Close()
	}
	aw := readMessage(t, ctx, anon)
	assert.Nil(t, aw["me"])
	readUntil(t, ctx, owner, "room.state") // presence update

	// Ping/pong.
	require.NoError(t, wsjson.Write(ctx, anon, map[string]any{"type": "ping", "t0": 12345}))
	pong := readUntil(t, ctx, anon, "pong")
	assert.EqualValues(t, 12345, pong["t0"])
	assert.Greater(t, pong["t1"].(float64), float64(0))

	// Anonymous cannot add; owner can, everyone sees the new queue.
	require.NoError(t, wsjson.Write(ctx, anon, map[string]any{"type": "queue.add", "url": "https://youtu.be/aqz-KE-bpKQ"}))
	e := readUntil(t, ctx, anon, "error")
	assert.Equal(t, "forbidden", e["code"])

	require.NoError(t, wsjson.Write(ctx, owner, map[string]any{"type": "queue.add", "url": "https://youtu.be/aqz-KE-bpKQ"}))
	state := readUntil(t, ctx, anon, "room.state")
	queue := state["queue"].([]any)
	require.Len(t, queue, 1)
	entry := queue[0].(map[string]any)
	assert.Equal(t, true, entry["current"])
	assert.Equal(t, "queued", entry["media"].(map[string]any)["status"])
	assert.Equal(t, "owner", entry["addedBy"])

	// Unsupported link is rejected cleanly.
	require.NoError(t, wsjson.Write(ctx, owner, map[string]any{"type": "queue.add", "url": "nope"}))
	e = readUntil(t, ctx, owner, "error")
	assert.Equal(t, "invalid", e["code"])

	// Play before ready is rejected; malformed JSON too.
	require.NoError(t, wsjson.Write(ctx, owner, map[string]any{"type": "play"}))
	e = readUntil(t, ctx, owner, "error")
	assert.Equal(t, "invalid", e["code"])
	require.NoError(t, owner.Write(ctx, websocket.MessageText, []byte("{bad")))
	e = readUntil(t, ctx, owner, "error")
	assert.Equal(t, "invalid", e["code"])

	// Report toggles the buffering flag in presence.
	require.NoError(t, wsjson.Write(ctx, owner, map[string]any{"type": "report", "state": "buffering", "positionMs": 0}))
	state = readUntil(t, ctx, anon, "room.state")
	members := state["members"].([]any)
	require.Len(t, members, 1)
	assert.Equal(t, true, members[0].(map[string]any)["buffering"])

	// A job was enqueued for the media.
	var jobsCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind = 'ingest'`).Scan(&jobsCount))
	assert.Equal(t, 1, jobsCount)

	// Snapshot JSON shape sanity: playback fields present.
	pb := state["playback"].(map[string]any)
	for _, k := range []string{"itemId", "playing", "positionMs", "atServerMs", "rate", "seq"} {
		_, ok := pb[k]
		assert.True(t, ok, k)
	}
	raw, _ := json.Marshal(state["room"])
	assert.Contains(t, string(raw), `"slug":"ws-room"`)
	_ = protocol.TypeWelcome
}

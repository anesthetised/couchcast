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
	"github.com/jackc/pgx/v5/pgxpool"
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

// newWSServer runs the API with a real room manager and hub over the
// test database.
func newWSServer(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
	t.Helper()
	pool := repotest.Pool(t)
	repo := repository.New(pool)
	logger := slog.New(slog.DiscardHandler)

	admit := ingest.NewService(repo, jobs.New(pool), ytdlp.New("yt-dlp", nil, logger), ingest.SourcePolicy{})
	rooms := room.NewManager(room.Deps{
		Store: repo, Chat: repo, Admit: admit, Signer: mediastore.NewSigner("0123456789abcdef0123456789abcdef", time.Hour), Logger: logger,
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
	t.Cleanup(ts.Close)
	return ts, pool
}

func TestWebSocketRoom(t *testing.T) {
	ts, pool := newWSServer(t)

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

// wsClient is one signed-in (or anonymous) browser for the hub tests.
type wsClient struct {
	t      *testing.T
	ts     *httptest.Server
	cookie *http.Cookie
	c      *websocket.Conn
}

func register(t *testing.T, ts *httptest.Server, name string) *wsClient {
	t.Helper()
	resp, err := http.Post(ts.URL+"/api/v1/auth/register", "application/json",
		strings.NewReader(`{"username":"`+name+`","password":"password-123"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	_ = resp.Body.Close()
	return &wsClient{t: t, ts: ts, cookie: resp.Cookies()[0]}
}

func (w *wsClient) rest(ctx context.Context, method, path, body string) int {
	w.t.Helper()
	req, _ := http.NewRequestWithContext(ctx, method, w.ts.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if w.cookie != nil {
		req.AddCookie(w.cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(w.t, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// dial connects to the room and returns the welcome.
func (w *wsClient) dial(ctx context.Context, slug string) (wsMessage, error) {
	opts := &websocket.DialOptions{}
	if w.cookie != nil {
		opts.HTTPHeader = http.Header{"Cookie": {w.cookie.String()}}
	}
	c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(w.ts.URL, "http")+"/api/v1/rooms/"+slug+"/ws", opts)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	w.t.Cleanup(func() { _ = c.CloseNow() })
	w.c = c
	return readMessage(w.t, ctx, c), nil
}

func (w *wsClient) send(ctx context.Context, msg map[string]any) {
	w.t.Helper()
	require.NoError(w.t, wsjson.Write(ctx, w.c, msg))
}

// until reads until a message of the type arrives.
func (w *wsClient) until(ctx context.Context, typ string) wsMessage {
	w.t.Helper()
	return readUntil(w.t, ctx, w.c, typ)
}

// state reads snapshots until one satisfies ok.
func (w *wsClient) state(ctx context.Context, ok func(wsMessage) bool) wsMessage {
	w.t.Helper()
	for {
		m := readMessage(w.t, ctx, w.c)
		if m["type"] == "room.state" && ok(m) {
			return m
		}
		if m["type"] == "error" {
			w.t.Fatalf("unexpected error: %v", m)
		}
	}
}

// expectError sends a command and returns the error code it gets.
func (w *wsClient) expectError(ctx context.Context, msg map[string]any) string {
	w.t.Helper()
	w.send(ctx, msg)
	return w.until(ctx, "error")["code"].(string)
}

func queueOf(m wsMessage) []map[string]any {
	raw := m["queue"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, q := range raw {
		out = append(out, q.(map[string]any))
	}
	return out
}

func playbackOf(m wsMessage) map[string]any { return m["playback"].(map[string]any) }

// readyVideo stores a playable YouTube media row, as if ingest had run.
func readyVideo(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) string {
	t.Helper()
	url := "https://www.youtube.com/watch?v=" + id
	_, err := pool.Exec(ctx, `
		INSERT INTO media (source_key, source_url, title, duration_ms, status, progress, renditions, s3_prefix)
		VALUES ($1, $2, $3, 60000, 'ready', 1, '[{"id":"0","height":720,"width":1280,"codec":"vp9","bitrate":1}]', $4)`,
		"youtube:"+id, url, "Video "+id, "media/"+id+"/")
	require.NoError(t, err)
	return url
}

func TestWebSocketCommands(t *testing.T) {
	ts, pool := newWSServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	owner := register(t, ts, "host")
	guest := register(t, ts, "friend")
	require.Equal(t, http.StatusCreated, owner.rest(ctx, http.MethodPost, "/api/v1/rooms", `{"name":"Cmds","slug":"cmds"}`))
	a, b, c := readyVideo(t, ctx, pool, "aaaaaaaaaaa"), readyVideo(t, ctx, pool, "bbbbbbbbbbb"), readyVideo(t, ctx, pool, "ccccccccccc")

	_, err := owner.dial(ctx, "cmds")
	require.NoError(t, err)
	gw, err := guest.dial(ctx, "cmds")
	require.NoError(t, err)
	assert.Equal(t, "friend", gw["me"])

	// Queue: add, add many, move, shuffle, remove.
	owner.send(ctx, map[string]any{"type": "queue.add", "url": a})
	st := owner.state(ctx, func(m wsMessage) bool { return len(queueOf(m)) == 1 && playbackOf(m)["playing"] == true })
	first := queueOf(st)[0]["id"].(string)
	owner.send(ctx, map[string]any{"type": "queue.addMany", "urls": []string{b, c}})
	st = owner.state(ctx, func(m wsMessage) bool { return len(queueOf(m)) == 3 })
	second, third := queueOf(st)[1]["id"].(string), queueOf(st)[2]["id"].(string)
	owner.send(ctx, map[string]any{"type": "queue.move", "itemId": third, "afterId": first})
	owner.state(ctx, func(m wsMessage) bool { return queueOf(m)[1]["id"] == third })
	owner.send(ctx, map[string]any{"type": "queue.shuffle"})
	owner.state(ctx, func(m wsMessage) bool { return len(queueOf(m)) == 3 })
	assert.Equal(t, "forbidden", guest.expectError(ctx, map[string]any{"type": "queue.shuffle"}))
	assert.Equal(t, "forbidden", guest.expectError(ctx, map[string]any{"type": "queue.remove", "itemId": second}))

	// Playback: pause, seek, rate, play, next, jump.
	owner.send(ctx, map[string]any{"type": "pause"})
	pb := owner.until(ctx, "playback")
	assert.Equal(t, false, pb["playing"])
	owner.send(ctx, map[string]any{"type": "seek", "positionMs": 12_000})
	pb = owner.until(ctx, "playback")
	assert.EqualValues(t, 12_000, pb["positionMs"])
	owner.send(ctx, map[string]any{"type": "rate.set", "rate": 1.5})
	pb = owner.until(ctx, "playback")
	assert.EqualValues(t, 1.5, pb["rate"])
	owner.send(ctx, map[string]any{"type": "play"})
	pb = owner.until(ctx, "playback")
	assert.Equal(t, true, pb["playing"])
	assert.Equal(t, "forbidden", guest.expectError(ctx, map[string]any{"type": "pause"}))
	owner.send(ctx, map[string]any{"type": "next"})
	st = owner.state(ctx, func(m wsMessage) bool { return len(queueOf(m)) == 2 })
	require.Len(t, st["played"].([]any), 1)
	owner.send(ctx, map[string]any{"type": "jump", "itemId": queueOf(st)[1]["id"]})
	st = owner.state(ctx, func(m wsMessage) bool { return len(queueOf(m)) == 1 })

	// Played list: replay one, then clear it.
	playedID := st["played"].([]any)[0].(map[string]any)["id"].(string)
	owner.send(ctx, map[string]any{"type": "queue.replay", "itemId": playedID})
	owner.state(ctx, func(m wsMessage) bool { return len(queueOf(m)) == 2 })
	owner.send(ctx, map[string]any{"type": "queue.clearPlayed"})
	owner.state(ctx, func(m wsMessage) bool { return len(m["played"].([]any)) == 0 })
	assert.Equal(t, "not_found", owner.expectError(ctx, map[string]any{"type": "queue.retry", "itemId": uuid.New().String()}))

	// Votes: vote mode lets members vote for items and to skip.
	owner.send(ctx, map[string]any{"type": "settings.set", "voteMode": true, "slowModeSec": 0})
	st = owner.state(ctx, func(m wsMessage) bool {
		return m["room"].(map[string]any)["settings"].(map[string]any)["voteMode"] == true
	})
	waiting := queueOf(st)[1]["id"].(string)
	guest.send(ctx, map[string]any{"type": "queue.vote", "itemId": waiting})
	guest.state(ctx, func(m wsMessage) bool { return len(queueOf(m)) == 2 && queueOf(m)[1]["voted"] == true })
	guest.send(ctx, map[string]any{"type": "skip.vote"})
	guest.state(ctx, func(m wsMessage) bool { return m["skipVoted"] == true || len(queueOf(m)) == 1 })

	// Chat: send, typing, react, edit, pin, unpin, delete, clear.
	guest.send(ctx, map[string]any{"type": "chat.send", "body": "helo"})
	line := owner.until(ctx, "chat.message")
	for line["system"] == true {
		line = owner.until(ctx, "chat.message")
	}
	id := line["id"]
	guest.send(ctx, map[string]any{"type": "chat.typing"})
	assert.Equal(t, "friend", owner.until(ctx, "typing")["username"])
	guest.send(ctx, map[string]any{"type": "react", "emoji": "🔥"})
	assert.Equal(t, "🔥", owner.until(ctx, "reaction")["emoji"])
	guest.send(ctx, map[string]any{"type": "chat.edit", "id": id, "body": "hello"})
	assert.Equal(t, "hello", owner.until(ctx, "chat.edited")["message"].(map[string]any)["body"])
	owner.send(ctx, map[string]any{"type": "chat.pin", "id": id})
	assert.NotNil(t, guest.until(ctx, "chat.pinned")["message"])
	owner.send(ctx, map[string]any{"type": "chat.unpin"})
	assert.Nil(t, guest.until(ctx, "chat.pinned")["message"])
	guest.send(ctx, map[string]any{"type": "chat.delete", "id": id})
	assert.Equal(t, id, owner.until(ctx, "chat.deleted")["id"])
	assert.Equal(t, "forbidden", guest.expectError(ctx, map[string]any{"type": "chat.clear"}))
	owner.send(ctx, map[string]any{"type": "chat.clear"})
	guest.until(ctx, "chat.cleared")

	// Unknown commands are refused without closing the connection.
	assert.Equal(t, "invalid", owner.expectError(ctx, map[string]any{"type": "teleport"}))

	// A promotion takes effect on the next command, without reconnecting.
	require.Equal(t, http.StatusNoContent, owner.rest(ctx, http.MethodPut, "/api/v1/rooms/cmds/moderators/friend", ""))
	guest.send(ctx, map[string]any{"type": "pause"})
	assert.Equal(t, false, guest.until(ctx, "playback")["playing"])

	// Ending the session closes every connection: the client gets the
	// kicked message and a policy close carrying the reason.
	owner.send(ctx, map[string]any{"type": "session.end"})
	kicked, closeErr := guest.readToClose(ctx)
	assert.Equal(t, "session ended", kicked)
	assert.Equal(t, websocket.StatusPolicyViolation, websocket.CloseStatus(closeErr))
	var ce websocket.CloseError
	require.ErrorAs(t, closeErr, &ce)
	assert.Equal(t, "session ended", ce.Reason)
}

// readToClose reads until the server closes, returning the kicked reason
// seen on the way and the close error.
func (w *wsClient) readToClose(ctx context.Context) (string, error) {
	w.t.Helper()
	kicked := ""
	for {
		var m wsMessage
		if err := wsjson.Read(ctx, w.c, &m); err != nil {
			return kicked, err
		}
		if m["type"] == "kicked" {
			kicked, _ = m["reason"].(string)
		}
	}
}

func TestWebSocketAccess(t *testing.T) {
	ts, _ := newWSServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	owner := register(t, ts, "keeper")
	friend := register(t, ts, "visitor")
	anon := &wsClient{t: t, ts: ts}
	require.Equal(t, http.StatusCreated, owner.rest(ctx, http.MethodPost, "/api/v1/rooms", `{"name":"Closed","slug":"closed","visibility":"private"}`))
	require.Equal(t, http.StatusCreated, owner.rest(ctx, http.MethodPost, "/api/v1/rooms", `{"name":"Open","slug":"open"}`))

	// A private room refuses outsiders; an unknown room does not exist.
	_, err := anon.dial(ctx, "closed")
	require.Error(t, err)
	_, err = friend.dial(ctx, "closed")
	require.Error(t, err)
	_, err = anon.dial(ctx, "nowhere")
	require.Error(t, err)

	// A ban closes the live connection and keeps the user out.
	_, err = owner.dial(ctx, "open")
	require.NoError(t, err)
	_, err = friend.dial(ctx, "open")
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, owner.rest(ctx, http.MethodPut, "/api/v1/rooms/open/bans/visitor", `{"reason":"spam"}`))
	kicked, closeErr := friend.readToClose(ctx)
	assert.Equal(t, "banned", kicked)
	assert.Equal(t, websocket.StatusPolicyViolation, websocket.CloseStatus(closeErr))
	_, err = friend.dial(ctx, "open")
	require.Error(t, err)
}

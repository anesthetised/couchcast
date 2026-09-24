package apihttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/ratelimit"
	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

func TestBugReports(t *testing.T) {
	repo := repository.New(repotest.Pool(t))
	ctx := context.Background()
	var debugged []uuid.UUID
	handler := newDBHandler(t, func(d *Deps) {
		d.Rooms, d.Users, d.DB, d.Admin, d.BugReports = repo, repo, repo, repo, repo
		d.BugReportLimiter = ratelimit.New(60, 4)
		d.RoomDebug = func(id uuid.UUID) (any, bool) {
			debugged = append(debugged, id)
			return map[string]any{"viewers": 2}, true
		}
	})
	newEnv := func() *dbEnv { return &dbEnv{&testEnv{t: t, handler: handler}} }
	admin, alice, anon := newEnv(), newEnv(), newEnv()
	admin.register("boss")
	alice.register("alice")
	boss, err := repo.GetUserByUsername(ctx, "boss")
	require.NoError(t, err)
	require.NoError(t, repo.SetUserRole(ctx, boss.ID, entity.RoleAdmin))
	require.Equal(t, http.StatusCreated, admin.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "Movies", "slug": "movies"}).Code)
	require.Equal(t, http.StatusCreated, admin.do(http.MethodPost, "/api/v1/rooms", map[string]any{"name": "Secret", "slug": "secret", "visibility": "private"}).Code)
	room, err := repo.GetRoomBySlug(ctx, "movies")
	require.NoError(t, err)

	media, _, err := repo.CreateMedia(ctx, repo.Pool(), "url:bug", "https://bug")
	require.NoError(t, err)
	require.NoError(t, repo.SetMediaReady(ctx, media.ID, []entity.Rendition{{ID: "0", Height: 720}}, 10, "media/x/"))
	_, err = repo.Pool().Exec(ctx, `INSERT INTO jobs (kind, payload, status, attempts, last_error) VALUES ('ingest', $1, 'done', 2, 'first try timed out')`,
		`{"mediaId":"`+media.ID.String()+`"}`)
	require.NoError(t, err)

	frame := base64.StdEncoding.EncodeToString([]byte{0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3})
	good := map[string]any{
		"category": "playback", "description": "  stutters every minute  ", "roomSlug": "movies", "mediaId": media.ID,
		"client": map[string]any{"device": map[string]any{"platform": "macOS"}}, "frame": frame,
	}

	// Signed-in only; validation.
	assert.Equal(t, http.StatusUnauthorized, anon.do(http.MethodPost, "/api/v1/bug-reports", good).Code)
	bad := func(patch map[string]any) int {
		body := map[string]any{}
		for k, v := range good {
			body[k] = v
		}
		for k, v := range patch {
			body[k] = v
		}
		return alice.do(http.MethodPost, "/api/v1/bug-reports", body).Code
	}
	assert.Equal(t, http.StatusBadRequest, bad(map[string]any{"category": "ux"}))
	assert.Equal(t, http.StatusBadRequest, bad(map[string]any{"frame": base64.StdEncoding.EncodeToString([]byte("GIF89a"))}))
	assert.Equal(t, http.StatusBadRequest, bad(map[string]any{"client": []int{1}}))
	assert.Equal(t, http.StatusBadRequest, bad(map[string]any{"description": strings.Repeat("x", 2001)}))
	assert.Equal(t, http.StatusRequestEntityTooLarge, bad(map[string]any{"client": map[string]any{"pad": strings.Repeat("x", 70<<10)}}))
	assert.Equal(t, http.StatusForbidden, bad(map[string]any{"roomSlug": "secret"}), "a private room the reporter cannot view")

	// A good report stores both halves and the frame.
	rec := alice.do(http.MethodPost, "/api/v1/bug-reports", good)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	id := decodeBody[map[string]string](t, rec)["id"]
	assert.Equal(t, []uuid.UUID{room.ID}, debugged)

	// Admin only.
	assert.Equal(t, http.StatusForbidden, alice.do(http.MethodGet, "/api/v1/admin/bug-reports", nil).Code)
	page := decodeBody[bugReportPage](t, admin.do(http.MethodGet, "/api/v1/admin/bug-reports", nil))
	require.Len(t, page.Reports, 1)
	got := page.Reports[0]
	assert.Equal(t, "alice", got.Author)
	assert.Equal(t, "movies", got.RoomSlug)
	assert.Equal(t, "stutters every minute", got.Description)
	assert.True(t, got.HasFrame)
	assert.JSONEq(t, `{"device":{"platform":"macOS"}}`, string(got.Client))

	var server struct {
		Version string `json:"version"`
		Room    struct {
			Slug   string         `json:"slug"`
			Role   string         `json:"role"`
			Loaded bool           `json:"loaded"`
			Live   map[string]any `json:"live"`
		} `json:"room"`
		Media struct {
			Status string `json:"status"`
			Job    struct {
				Attempts  int    `json:"attempts"`
				LastError string `json:"lastError"`
			} `json:"job"`
		} `json:"media"`
	}
	require.NoError(t, json.Unmarshal(got.Server, &server))
	assert.Equal(t, "movies", server.Room.Slug)
	assert.True(t, server.Room.Loaded)
	assert.EqualValues(t, 2, server.Room.Live["viewers"])
	assert.Equal(t, "ready", server.Media.Status)
	assert.Equal(t, 2, server.Media.Job.Attempts)
	assert.Equal(t, "first try timed out", server.Media.Job.LastError)

	frameRec := admin.do(http.MethodGet, "/api/v1/admin/bug-reports/"+id+"/frame", nil)
	require.Equal(t, http.StatusOK, frameRec.Code)
	assert.Equal(t, "image/jpeg", frameRec.Header().Get("Content-Type"))
	assert.Equal(t, []byte{0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3}, frameRec.Body.Bytes())

	// Resolve moves it to the resolved list; twice is a 404.
	assert.Equal(t, http.StatusNoContent, admin.do(http.MethodPost, "/api/v1/admin/bug-reports/"+id+"/resolve", resolveBugRequest{Note: "fixed in abc"}).Code)
	assert.Equal(t, http.StatusNotFound, admin.do(http.MethodPost, "/api/v1/admin/bug-reports/"+id+"/resolve", nil).Code)
	assert.Empty(t, decodeBody[bugReportPage](t, admin.do(http.MethodGet, "/api/v1/admin/bug-reports", nil)).Reports)
	resolved := decodeBody[bugReportPage](t, admin.do(http.MethodGet, "/api/v1/admin/bug-reports?status=resolved", nil)).Reports
	require.Len(t, resolved, 1)
	assert.Equal(t, "fixed in abc", resolved[0].Note)
	assert.NotNil(t, resolved[0].ResolvedAt)

	// Without room or media the report still goes through; the limiter
	// (burst 4) stops a flood.
	codes := make([]int, 0, 5)
	for range 5 {
		codes = append(codes, alice.do(http.MethodPost, "/api/v1/bug-reports", map[string]any{"category": "other", "description": "hm"}).Code)
	}
	assert.Contains(t, codes, http.StatusCreated)
	assert.Equal(t, http.StatusTooManyRequests, codes[len(codes)-1])
}
